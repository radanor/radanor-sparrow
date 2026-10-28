//go:build windows

package windowseventlog

import (
	"errors"
	"os"
	"runtime"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Opt-in read-only system headers. Never requests XML, messages or EventData.
func TestReadOnlyNativeSystemProperties(t *testing.T) {
	if os.Getenv("RADANOR_TEST_EVENTLOG_READONLY") != "1" {
		t.Skip("native read-only event-log probe is opt-in")
	}
	path, _ := windows.UTF16PtrFromString(channel)
	filter, _ := windows.UTF16PtrFromString(query)
	result, _, err := wevtapi.NewProc("EvtQuery").Call(0, uintptr(unsafe.Pointer(path)), uintptr(unsafe.Pointer(filter)), 0x201)
	if result == 0 {
		t.Skipf("native channel query unavailable: %v", err)
	}
	defer procClose.Call(result)
	var handle uintptr
	var returned uint32
	ok, _, err := procNext.Call(result, 1, uintptr(unsafe.Pointer(&handle)), 0, 0, uintptr(unsafe.Pointer(&returned)))
	if ok == 0 || returned != 1 {
		t.Skipf("no eligible existing record available: %v", err)
	}
	defer procClose.Call(handle)
	header, err := renderSystem(handle)
	if err != nil {
		t.Fatal(err)
	}
	if header.System.Channel != channel || !allowed(header.System.EventID) {
		t.Fatal("native system identity mismatch")
	}
	// Read only the selected system timestamp, never the rest of the event.
	// Windows normally returns a FILETIME variant for this attribute.
	xpath, _ := windows.UTF16PtrFromString("Event/System/TimeCreated/@SystemTime")
	paths := []*uint16{xpath}
	context, _, err := procCreateRenderContext.Call(1, uintptr(unsafe.Pointer(&paths[0])), 0)
	if context == 0 {
		t.Fatal(err)
	}
	defer procClose.Call(context)
	var used, properties uint32
	_, _, probeErr := procRender.Call(context, handle, 0, 0, 0, uintptr(unsafe.Pointer(&used)), uintptr(unsafe.Pointer(&properties)))
	if !errors.Is(probeErr, windows.ERROR_INSUFFICIENT_BUFFER) || used < 16 || used > 64*1024 {
		t.Fatal("invalid system attribute buffer probe")
	}
	buffer := make([]uint64, (used+7)/8)
	var pin runtime.Pinner
	pin.Pin(&buffer[0])
	defer pin.Unpin()
	if len(buffer) == 0 {
		t.Fatal("empty system attribute")
	}
	ok, _, err = procRender.Call(context, handle, 0, uintptr(len(buffer)*8), uintptr(unsafe.Pointer(&buffer[0])), uintptr(unsafe.Pointer(&used)), uintptr(unsafe.Pointer(&properties)))
	if ok == 0 {
		t.Fatal(err)
	}
	if used < 16 || used > uint32(len(buffer)*8) || properties != 1 {
		t.Fatal("invalid system attribute buffer")
	}
	value := (*systemVariant)(unsafe.Pointer(&buffer[0]))
	if value.Type != 17 {
		t.Fatal("unexpected system timestamp variant type")
	}
	stamp, err := filetimeTimestamp(value.Value)
	if err != nil || stamp != header.System.TimeCreated.SystemTime {
		t.Fatal("system timestamp precision changed")
	}
	t.Logf("NATIVE system-only render: event_id=%d record_id=%d", header.System.EventID, header.System.RecordID)
}
