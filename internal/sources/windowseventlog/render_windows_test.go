//go:build windows

package windowseventlog

import (
	"errors"
	"runtime"
	"testing"
	"time"
	"unsafe"

	"github.com/radanor/radanor-shield/internal/monitoring"
	"golang.org/x/sys/windows"
)

func systemFixture(t *testing.T) ([]uint64, uint32) {
	t.Helper()
	buffer := make([]uint64, 128)
	values := unsafe.Slice((*systemVariant)(unsafe.Pointer(&buffer[0])), 18)
	offset := 18 * int(unsafe.Sizeof(systemVariant{}))
	for _, field := range []struct {
		index int
		text  string
	}{{0, "Microsoft-Windows-Windows Defender"}, {14, channel}} {
		text, err := windows.UTF16FromString(field.text)
		if err != nil {
			t.Fatal(err)
		}
		pointer := (*uint16)(unsafe.Add(unsafe.Pointer(&buffer[0]), offset))
		copy(unsafe.Slice(pointer, len(text)), text)
		values[field.index].Type = 1
		*(**uint16)(unsafe.Pointer(&values[field.index].Value)) = pointer
		offset += len(text) * 2
	}
	values[2] = systemVariant{Value: 5007, Type: 6}
	values[9] = systemVariant{Value: 499, Type: 10}
	stamp := time.Date(2026, 10, 12, 12, 34, 56, 123456700, time.UTC)
	values[8] = systemVariant{Value: uint64(stamp.Unix()+11644473600)*10000000 + uint64(stamp.Nanosecond()/100), Type: 17}
	return buffer, uint32(offset)
}

func TestSystemVariantLayoutAndFiletimePrecision(t *testing.T) {
	if unsafe.Sizeof(systemVariant{}) != 16 || unsafe.Offsetof(systemVariant{}.Count) != 8 || unsafe.Offsetof(systemVariant{}.Type) != 12 {
		t.Fatal("EVT_VARIANT native layout mismatch")
	}
	for _, nanos := range []int{0, 100, 123456700, 999999900} {
		buffer, used := systemFixture(t)
		values := unsafe.Slice((*systemVariant)(unsafe.Pointer(&buffer[0])), 18)
		stamp := time.Date(2026, 10, 12, 12, 34, 56, nanos, time.UTC)
		values[8].Value = uint64(stamp.Unix()+11644473600)*10000000 + uint64(nanos/100)
		header, err := decodeSystem(buffer, used, 18)
		if err != nil {
			t.Fatal(err)
		}
		want := stamp.Format("2006-01-02T15:04:05.0000000Z")
		if header.System.TimeCreated.SystemTime != want || header.System.EventID != 5007 || header.System.RecordID != 499 {
			t.Fatal("system property or 100ns precision changed")
		}
		if EventIdentity(channel, 499, want) != EventIdentity(header.System.Channel, header.System.RecordID, header.System.TimeCreated.SystemTime) {
			t.Fatal("legacy seven-digit identity changed")
		}
	}
}

func TestMalformedSystemBufferRejected(t *testing.T) {
	for _, name := range []string{"short returned size", "oversized returned size", "odd size", "missing properties", "null pointer", "outside pointer", "header pointer", "unterminated string", "array string", "null type", "wrong ID type", "wrong record type", "wrong time type", "zero time"} {
		t.Run(name, func(t *testing.T) {
			buffer, used := systemFixture(t)
			props := uint32(18)
			values := unsafe.Slice((*systemVariant)(unsafe.Pointer(&buffer[0])), 18)
			other := []uint16{'x', 0}
			switch name {
			case "short returned size":
				used = 16
			case "oversized returned size":
				used = uint32(len(buffer)*8 + 2)
			case "odd size":
				used--
			case "missing properties":
				props = 17
			case "null pointer":
				values[0].Value = 0
			case "outside pointer":
				*(**uint16)(unsafe.Pointer(&values[0].Value)) = &other[0]
			case "header pointer":
				*(**uint16)(unsafe.Pointer(&values[0].Value)) = (*uint16)(unsafe.Pointer(&buffer[0]))
			case "unterminated string":
				values[14].Value = 0
				*(**uint16)(unsafe.Pointer(&values[14].Value)) = (*uint16)(unsafe.Add(unsafe.Pointer(&buffer[0]), uintptr(used)-2))
				*(*uint16)(unsafe.Add(unsafe.Pointer(&buffer[0]), uintptr(used)-2)) = 'x'
			case "array string":
				values[0].Type = 0x81
			case "null type":
				values[0].Type = 0
			case "wrong ID type":
				values[2].Type = 8
			case "wrong record type":
				values[9].Type = 8
			case "wrong time type":
				values[8].Type = 10
			case "zero time":
				values[8].Value = 0
			}
			if _, err := decodeSystem(buffer, used, props); err == nil {
				t.Fatalf("malformed frame accepted: %s", name)
			}
			runtime.KeepAlive(other)
		})
	}
	if _, err := decodeSystem(nil, 0, 18); err == nil {
		t.Fatal("nil buffer accepted")
	}
}

func TestBookmarkCreationFailureClassification(t *testing.T) {
	for _, err := range []error{windows.ERROR_ACCESS_DENIED, windows.ERROR_NOT_ENOUGH_MEMORY, windows.ERROR_EVT_INVALID_QUERY} {
		got := classifyBookmarkCreation(err)
		if errors.Is(got, monitoring.ErrInvalidCheckpoint) {
			t.Fatalf("native error invalidates valid state: %v", err)
		}
	}
	if !errors.Is(classifyBookmarkCreation(windows.ERROR_ACCESS_DENIED), monitoring.ErrPermissionDenied) {
		t.Fatal("access denied lost")
	}
	for _, err := range []error{windows.ERROR_INVALID_DATA, windows.ERROR_EVT_MALFORMED_XML_TEXT} {
		if !errors.Is(classifyBookmarkCreation(err), monitoring.ErrInvalidCheckpoint) {
			t.Fatal("malformed bookmark did not recover")
		}
	}
}

func TestUnrepresentableFiletimeRejected(t *testing.T) {
	for _, stamp := range []uint64{1, ^uint64(0)} {
		buffer, used := systemFixture(t)
		values := unsafe.Slice((*systemVariant)(unsafe.Pointer(&buffer[0])), 18)
		values[8].Value = stamp
		if _, err := decodeSystem(buffer, used, 18); err == nil {
			t.Errorf("FILETIME outside SQLite Unix-nanosecond range accepted: %d", stamp)
		}
	}
}
