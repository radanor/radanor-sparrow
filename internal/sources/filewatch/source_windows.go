//go:build windows

package filewatch

import (
	"context"
	"encoding/binary"
	"errors"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf16"

	"github.com/radanor/radanor-shield/internal/monitoring"
	"golang.org/x/sys/windows"
)

const windowsNotifyMask = windows.FILE_NOTIFY_CHANGE_FILE_NAME | windows.FILE_NOTIFY_CHANGE_DIR_NAME | windows.FILE_NOTIFY_CHANGE_LAST_WRITE | windows.FILE_NOTIFY_CHANGE_SIZE

type watchedHandle struct {
	label  string
	handle windows.Handle
}

func (s *Source) Run(ctx context.Context, _ []byte, emit monitoring.EmitFunc, report monitoring.Reporter) error {
	handles := make([]watchedHandle, 0, len(s.roots))
	for _, root := range s.roots {
		path, err := windows.UTF16PtrFromString(root.Path)
		if err != nil {
			return err
		}
		handle, err := windows.CreateFile(path, windows.FILE_LIST_DIRECTORY, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
		if err != nil {
			for _, h := range handles {
				windows.CloseHandle(h.handle)
			}
			if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
				return monitoring.ErrPermissionDenied
			}
			return err
		}
		handles = append(handles, watchedHandle{label: root.Label, handle: handle})
	}
	report.ConfirmHealthy()
	var wg sync.WaitGroup
	var sequence atomic.Uint64
	errs := make(chan error, len(handles))
	for _, item := range handles {
		wg.Add(1)
		go func(w watchedHandle) { defer wg.Done(); errs <- watchWindows(ctx, s, w, &sequence, emit, report) }(item)
	}
	select {
	case <-ctx.Done():
		for _, h := range handles {
			_ = windows.CancelIoEx(h.handle, nil)
			_ = windows.CloseHandle(h.handle)
		}
		wg.Wait()
		return ctx.Err()
	case err := <-errs:
		for _, h := range handles {
			_ = windows.CancelIoEx(h.handle, nil)
			_ = windows.CloseHandle(h.handle)
		}
		wg.Wait()
		return err
	}
}
func watchWindows(ctx context.Context, s *Source, w watchedHandle, sequence *atomic.Uint64, emit monitoring.EmitFunc, report monitoring.Reporter) error {
	buf := make([]byte, 64*1024)
	for {
		var n uint32
		err := windows.ReadDirectoryChanges(w.handle, &buf[0], uint32(len(buf)), false, windowsNotifyMask, &n, nil, 0)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, windows.ERROR_OPERATION_ABORTED) || errors.Is(err, windows.ERROR_INVALID_HANDLE) {
				return ctx.Err()
			}
			if errors.Is(err, windows.ERROR_NOTIFY_ENUM_DIR) {
				now := time.Now()
				report.ReportGap(now, now, "directory notification overflow; events potentially missed")
				continue
			}
			return err
		}
		if n == 0 {
			now := time.Now()
			report.ReportGap(now, now, "directory notification buffer overflow; events potentially missed")
			continue
		}
		offset := uint32(0)
		for offset+12 <= n {
			next := binary.LittleEndian.Uint32(buf[offset:])
			action := binary.LittleEndian.Uint32(buf[offset+4:])
			nameBytes := binary.LittleEndian.Uint32(buf[offset+8:])
			if nameBytes%2 != 0 || offset+12+nameBytes > n {
				return errors.New("malformed directory notification")
			}
			units := make([]uint16, nameBytes/2)
			for i := range units {
				units[i] = binary.LittleEndian.Uint16(buf[offset+12+uint32(i*2):])
			}
			name := filepath.Base(string(utf16.Decode(units)))
			typ := windowsEventType(action)
			if typ != "" {
				now := time.Now()
				seq := sequence.Add(1)
				event := monitoring.Event{ID: s.eventID("rdcw:", seq), Source: SourceID, Type: typ, Platform: "windows", OccurredAt: now, ObservedAt: now, Evidence: monitoring.EvidenceTelemetry, SchemaVersion: monitoring.EventSchemaV1, SourceSequence: strconv.FormatUint(seq, 10), Metadata: map[string]string{"root": w.label, "name": name, "native_action": strconv.FormatUint(uint64(action), 10)}}
				if err := emit(ctx, event); err != nil && ctx.Err() != nil {
					return ctx.Err()
				}
			}
			if next == 0 {
				break
			}
			if next%4 != 0 || offset+next >= n {
				return errors.New("malformed directory notification offset")
			}
			offset += next
		}
	}
}
func windowsEventType(action uint32) string {
	switch action {
	case 1:
		return "file.created"
	case 2:
		return "file.deleted"
	case 3:
		return "file.modified"
	case 4:
		return "file.renamed_from"
	case 5:
		return "file.renamed_to"
	default:
		return ""
	}
}
