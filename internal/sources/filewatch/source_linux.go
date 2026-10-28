//go:build linux

package filewatch

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/radanor/radanor-shield/internal/monitoring"
	"golang.org/x/sys/unix"
)

const linuxMask = unix.IN_CREATE | unix.IN_MODIFY | unix.IN_CLOSE_WRITE | unix.IN_DELETE | unix.IN_MOVED_FROM | unix.IN_MOVED_TO | unix.IN_DELETE_SELF | unix.IN_MOVE_SELF | unix.IN_IGNORED | unix.IN_Q_OVERFLOW

func (s *Source) Run(ctx context.Context, _ []byte, emit monitoring.EmitFunc, report monitoring.Reporter) error {
	fd, err := unix.InotifyInit1(unix.IN_CLOEXEC | unix.IN_NONBLOCK)
	if err != nil {
		return classify(err)
	}
	defer unix.Close(fd)
	labels := map[int]string{}
	for _, root := range s.roots {
		wd, addErr := unix.InotifyAddWatch(fd, root.Path, linuxMask|unix.IN_DONT_FOLLOW|unix.IN_ONLYDIR)
		if addErr != nil {
			return classify(addErr)
		}
		labels[wd] = root.Label
	}
	report.ConfirmHealthy()
	var sequence atomic.Uint64
	buf := make([]byte, 64*1024)
	poll := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			{
			}
		}
		ready, pollErr := unix.Poll(poll, 250)
		if pollErr != nil {
			if errors.Is(pollErr, unix.EINTR) {
				continue
			}
			return pollErr
		}
		if ready == 0 {
			continue
		}
		n, readErr := unix.Read(fd, buf)
		if readErr != nil {
			if errors.Is(readErr, unix.EAGAIN) {
				continue
			}
			return readErr
		}
		offset := 0
		for offset+unix.SizeofInotifyEvent <= n {
			raw := (*unix.InotifyEvent)(unsafe.Pointer(&buf[offset]))
			size := unix.SizeofInotifyEvent + int(raw.Len)
			if size <= 0 || offset+size > n {
				return errors.New("malformed inotify record")
			}
			name := strings.TrimRight(string(buf[offset+unix.SizeofInotifyEvent:offset+size]), "\x00")
			offset += size
			now := time.Now()
			if raw.Mask&unix.IN_Q_OVERFLOW != 0 {
				report.ReportGap(now, now, "inotify queue overflow; events potentially missed")
				continue
			}
			label, ok := labels[int(raw.Wd)]
			if !ok {
				continue
			}
			if raw.Mask&(unix.IN_IGNORED|unix.IN_DELETE_SELF|unix.IN_MOVE_SELF) != 0 {
				report.ReportGap(now, now, "inotify watch removed or root moved")
				if raw.Mask&unix.IN_IGNORED != 0 {
					delete(labels, int(raw.Wd))
				}
				continue
			}
			typ := linuxEventType(raw.Mask)
			if typ == "" {
				continue
			}
			seq := sequence.Add(1)
			metadata := map[string]string{"root": label, "name": filepath.Base(name), "native_mask": fmt.Sprintf("0x%x", raw.Mask)}
			if raw.Cookie != 0 {
				metadata["rename_cookie"] = strconv.FormatUint(uint64(raw.Cookie), 10)
			}
			event := monitoring.Event{ID: s.eventID("inotify:", seq), Source: SourceID, Type: typ, Platform: "linux", OccurredAt: now, ObservedAt: now, Evidence: monitoring.EvidenceTelemetry, SchemaVersion: monitoring.EventSchemaV1, SourceSequence: strconv.FormatUint(seq, 10), Metadata: metadata}
			if err := emit(ctx, event); err != nil && ctx.Err() != nil {
				return ctx.Err()
			}
		}
	}
}
func linuxEventType(mask uint32) string {
	switch {
	case mask&unix.IN_MOVED_FROM != 0:
		return "file.renamed_from"
	case mask&unix.IN_MOVED_TO != 0:
		return "file.renamed_to"
	case mask&unix.IN_CREATE != 0:
		return "file.created"
	case mask&unix.IN_DELETE != 0:
		return "file.deleted"
	case mask&(unix.IN_CLOSE_WRITE|unix.IN_MODIFY) != 0:
		return "file.modified"
	default:
		return ""
	}
}
func classify(err error) error {
	if errors.Is(err, unix.EACCES) || errors.Is(err, unix.EPERM) {
		return monitoring.ErrPermissionDenied
	}
	if errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EINVAL) {
		return monitoring.ErrUnsupported
	}
	return err
}
