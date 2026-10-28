//go:build windows

package windowseventlog

import (
	"context"
	"errors"
	"runtime"
	"strconv"
	"time"
	"unsafe"

	"github.com/radanor/radanor-shield/internal/monitoring"
	"golang.org/x/sys/windows"
)

const SourceID = "native.windows_defender_events"
const channel = "Microsoft-Windows-Windows Defender/Operational"
const query = "*[System[(EventID=1006 or EventID=1116 or EventID=1117 or EventID=1118 or EventID=1119 or EventID=5001 or EventID=5007)]]"
const (
	evtSubscribeFuture        = 1
	evtSubscribeAfterBookmark = 3
	evtSubscribeStrict        = 0x10000
	evtRenderBookmark         = 2
)

var wevtapi = windows.NewLazySystemDLL("wevtapi.dll")
var procSubscribe = wevtapi.NewProc("EvtSubscribe")
var procNext = wevtapi.NewProc("EvtNext")
var procRender = wevtapi.NewProc("EvtRender")
var procClose = wevtapi.NewProc("EvtClose")
var procCreateBookmark = wevtapi.NewProc("EvtCreateBookmark")
var procUpdateBookmark = wevtapi.NewProc("EvtUpdateBookmark")
var procCreateRenderContext = wevtapi.NewProc("EvtCreateRenderContext")

type Source struct{}

func New() *Source { return &Source{} }
func (*Source) Descriptor() monitoring.Descriptor {
	return monitoring.Descriptor{ContractVersion: monitoring.SourceContractV1, ID: SourceID, Platforms: []string{"windows-11"}, Capabilities: []string{"filtered Windows Defender operational events"}, RequiredAccess: monitoring.PrivilegeUser, EventTypes: []string{"defender.detection", "defender.action", "defender.protection_changed", "defender.configuration_changed"}, SchemaVersion: monitoring.EventSchemaV1, Recovery: monitoring.RecoveryCheckpoint, StartupBehavior: "opens a strict pull subscription for future allowlisted records or after a bookmark", ShutdownBehavior: "cancellation closes subscription, bookmark, and wait handles", HealthReporting: "confirms only after EvtSubscribe succeeds", Limitations: []string{"channel availability and ACL vary", "checkpoints are persisted atomically with events by the core; stale bookmarks produce explicit gaps", "event message payloads are not collected"}}
}

type eventXML struct {
	System struct {
		Provider struct {
			Name string `xml:"Name,attr"`
		} `xml:"Provider"`
		EventID     uint32 `xml:"EventID"`
		RecordID    uint64 `xml:"EventRecordID"`
		Channel     string `xml:"Channel"`
		TimeCreated struct {
			SystemTime string `xml:"SystemTime,attr"`
		} `xml:"TimeCreated"`
	} `xml:"System"`
}

func (s *Source) Run(ctx context.Context, checkpoint []byte, emit monitoring.EmitFunc, report monitoring.Reporter) error {
	signal, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(signal)
	path, _ := windows.UTF16PtrFromString(channel)
	filter, _ := windows.UTF16PtrFromString(query)
	var bookmark uintptr
	flags := uintptr(evtSubscribeFuture | evtSubscribeStrict)
	if len(checkpoint) > 0 {
		value, convErr := windows.UTF16PtrFromString(string(checkpoint))
		if convErr != nil {
			return monitoring.ErrInvalidCheckpoint
		}
		var createErr error
		bookmark, _, createErr = procCreateBookmark.Call(uintptr(unsafe.Pointer(value)))
		if bookmark == 0 {
			return classifyBookmarkCreation(createErr)
		}
		defer procClose.Call(bookmark)
		flags = evtSubscribeAfterBookmark | evtSubscribeStrict
	}
	sub, _, callErr := procSubscribe.Call(0, uintptr(signal), uintptr(unsafe.Pointer(path)), uintptr(unsafe.Pointer(filter)), bookmark, 0, 0, flags)
	if sub == 0 {
		return classifySubscription(callErr, len(checkpoint) > 0)
	}
	defer procClose.Call(sub)
	report.ConfirmHealthy()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			{
			}
		}
		wait, waitErr := windows.WaitForSingleObject(signal, 250)
		if waitErr != nil {
			return waitErr
		}
		if wait == uint32(windows.WAIT_TIMEOUT) {
			continue
		}
		_ = windows.ResetEvent(signal)
		for {
			var event uintptr
			var returned uint32
			r, _, nextErr := procNext.Call(sub, 1, uintptr(unsafe.Pointer(&event)), 0, 0, uintptr(unsafe.Pointer(&returned)))
			if r == 0 {
				if errors.Is(nextErr, windows.ERROR_NO_MORE_ITEMS) {
					break
				}
				if errors.Is(nextErr, windows.Errno(0x3aa3)) {
					now := time.Now()
					report.ReportGap(now, now, "event log subscription became stale")
					return monitoring.ErrInvalidCheckpoint
				}
				return classifyWindows(nextErr)
			}
			if returned != 1 {
				break
			}
			processErr := func() error {
				defer procClose.Call(event)
				return processEvent(ctx, event, renderSystem, bookmarkFor, emit, report)
			}()
			if processErr != nil {
				return processErr
			}
		}
	}
}
func render(handle uintptr, flag uintptr) (string, error) {
	var used, props uint32
	procRender.Call(0, handle, flag, 0, 0, uintptr(unsafe.Pointer(&used)), uintptr(unsafe.Pointer(&props)))
	if used == 0 {
		return "", errors.New("EvtRender size unavailable")
	}
	buf := make([]uint16, (used+1)/2)
	r, _, err := procRender.Call(0, handle, flag, uintptr(used), uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&used)), uintptr(unsafe.Pointer(&props)))
	if r == 0 {
		return "", err
	}
	return windows.UTF16ToString(buf), nil
}
func bookmarkFor(event uintptr) (string, error) {
	bookmark, _, err := procCreateBookmark.Call(0)
	if bookmark == 0 {
		return "", err
	}
	defer procClose.Call(bookmark)
	r, _, err := procUpdateBookmark.Call(bookmark, event)
	if r == 0 {
		return "", err
	}
	return render(bookmark, evtRenderBookmark)
}
func classifyWindows(err error) error {
	if errors.Is(err, windows.Errno(0x3aa3)) {
		return monitoring.ErrInvalidCheckpoint
	}
	if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		return monitoring.ErrPermissionDenied
	}
	if errors.Is(err, windows.ERROR_EVT_CHANNEL_NOT_FOUND) {
		return monitoring.ErrUnsupported
	}
	return err
}

func classifySubscription(err error, hasCheckpoint bool) error {
	// EvtSubscribeStrict documents ERROR_NOT_FOUND for a missing subscribed
	// bookmark position. Invalid-query is not proof of a stale bookmark.
	if hasCheckpoint && errors.Is(err, windows.ERROR_NOT_FOUND) {
		return monitoring.ErrInvalidCheckpoint
	}
	return classifyWindows(err)
}

func classifyBookmarkCreation(err error) error {
	// Fixed API arguments leave the stored XML as the variable input. Preserve
	// resource/access errors; only explicit format errors justify invalidation.
	if errors.Is(err, windows.ERROR_INVALID_DATA) || errors.Is(err, windows.ERROR_INVALID_PARAMETER) || errors.Is(err, windows.ERROR_EVT_MALFORMED_XML_TEXT) {
		return monitoring.ErrInvalidCheckpoint
	}
	return classifyWindows(err)
}

// processEvent stops consumption on any unreadable record. The engine retries
// from durable storage; a later event cannot silently skip this record.
func processEvent(ctx context.Context, handle uintptr, read func(uintptr) (eventXML, error), bookmark func(uintptr) (string, error), emit monitoring.EmitFunc, report monitoring.Reporter) error {
	fail := func(reason string) error {
		now := time.Now()
		report.ReportGap(now, now, reason)
		return errors.New(reason)
	}
	parsed, err := read(handle)
	if err != nil {
		return fail("event system-property rendering or decoding failed")
	}
	if !allowed(parsed.System.EventID) {
		return nil
	}
	if parsed.System.Channel != channel || parsed.System.Provider.Name != "Microsoft-Windows-Windows Defender" || parsed.System.RecordID == 0 {
		return fail("event system identity is missing or inconsistent")
	}
	occurred, err := time.Parse(time.RFC3339Nano, parsed.System.TimeCreated.SystemTime)
	if err != nil {
		return fail("event system timestamp is invalid")
	}
	checkpoint, err := bookmark(handle)
	if err != nil || checkpoint == "" {
		return fail("event bookmark generation failed")
	}
	record := RecordSequence(parsed.System.RecordID)
	event := monitoring.Event{ID: EventIdentity(parsed.System.Channel, parsed.System.RecordID, parsed.System.TimeCreated.SystemTime),
		Source: SourceID, Type: eventType(parsed.System.EventID), Platform: "windows", OccurredAt: occurred, ObservedAt: time.Now(), Evidence: monitoring.EvidenceAudit,
		SchemaVersion: monitoring.EventSchemaV1, SourceSequence: record, Checkpoint: []byte(checkpoint),
		Metadata: map[string]string{"channel": channel, "provider": parsed.System.Provider.Name, "event_id": strconv.FormatUint(uint64(parsed.System.EventID), 10), "record_id": record}}
	if err := emit(ctx, event); err != nil {
		if ctx.Err() == nil {
			now := time.Now()
			report.ReportGap(now, now, "event delivery failed")
		}
		return err
	}
	return nil
}

type systemVariant struct {
	Value       uint64
	Count, Type uint32
}

// EvtRenderContextSystem reads only system properties, never EventData,
// UserData, rendered messages, threat paths or detection payloads.
func renderSystem(event uintptr) (eventXML, error) {
	var record eventXML
	context, _, err := procCreateRenderContext.Call(0, 0, 1) // EvtRenderContextSystem
	if context == 0 {
		return record, err
	}
	defer procClose.Call(context)
	var used, properties uint32
	_, _, probeErr := procRender.Call(context, event, 0, 0, 0, uintptr(unsafe.Pointer(&used)), uintptr(unsafe.Pointer(&properties)))
	if !errors.Is(probeErr, windows.ERROR_INSUFFICIENT_BUFFER) {
		return record, errors.New("system-property buffer probe failed")
	}
	if used == 0 || used > 64*1024 {
		return record, errors.New("invalid system-property buffer size")
	}
	buffer := make([]uint64, (used+7)/8)
	var pin runtime.Pinner
	pin.Pin(&buffer[0])
	defer pin.Unpin()
	result, _, err := procRender.Call(context, event, 0, uintptr(len(buffer)*8), uintptr(unsafe.Pointer(&buffer[0])), uintptr(unsafe.Pointer(&used)), uintptr(unsafe.Pointer(&properties)))
	if result == 0 {
		return record, err
	}
	return decodeSystem(buffer, used, properties)
}

func decodeSystem(buffer []uint64, used, properties uint32) (eventXML, error) {
	var record eventXML
	var err error
	defer runtime.KeepAlive(buffer)
	if used == 0 || used > uint32(len(buffer)*8) || used%2 != 0 || properties < 18 || uint64(properties)*uint64(unsafe.Sizeof(systemVariant{})) > uint64(used) {
		return record, errors.New("incomplete system-property buffer")
	}
	values := unsafe.Slice((*systemVariant)(unsafe.Pointer(&buffer[0])), int(properties))
	base := uintptr(unsafe.Pointer(&buffer[0]))
	firstString := base + uintptr(properties)*unsafe.Sizeof(systemVariant{})
	end := base + uintptr(used)
	text := func(index int) (string, error) {
		if values[index].Type != 1 {
			return "", errors.New("missing string system property")
		}
		pointer := *(**uint16)(unsafe.Pointer(&values[index].Value))
		if pointer == nil {
			return "", errors.New("null string system property")
		}
		address := uintptr(unsafe.Pointer(pointer))
		if address < firstString || address >= end || address%2 != 0 {
			return "", errors.New("string system property outside render buffer")
		}
		text := unsafe.Slice(pointer, int((end-address)/2))
		for i, char := range text {
			if char == 0 {
				return windows.UTF16ToString(text[:i]), nil
			}
		}
		return "", errors.New("unterminated string system property")
	}
	if record.System.Provider.Name, err = text(0); err != nil {
		return record, err
	}
	if record.System.Channel, err = text(14); err != nil {
		return record, err
	}
	if values[2].Type != 6 || values[9].Type != 10 || values[8].Type != 17 {
		return record, errors.New("invalid numeric system property types")
	}
	record.System.EventID = uint32(values[2].Value & 0xffff)
	record.System.RecordID = values[9].Value
	if values[8].Value == 0 {
		return record, errors.New("missing system timestamp")
	}
	record.System.TimeCreated.SystemTime, err = filetimeTimestamp(values[8].Value)
	return record, err
}

// Seven digits preserve FILETIME precision and standard seven-digit legacy
// identities. Other historical XML spellings require native replay validation.
func filetimeTimestamp(stamp uint64) (string, error) {
	if stamp == 0 {
		return "", errors.New("missing system timestamp")
	}
	seconds := int64(stamp/10000000) - 11644473600 // FILETIME epoch (1601) to Unix (1970).
	nanoseconds := int64(stamp%10000000) * 100
	value := time.Unix(seconds, nanoseconds).UTC()
	// SQLite stores OccurredAt as signed Unix nanoseconds; reject overflow.
	if !time.Unix(0, value.UnixNano()).Equal(value) {
		return "", errors.New("system timestamp outside storage range")
	}
	return value.Format("2006-01-02T15:04:05.0000000Z"), nil
}
