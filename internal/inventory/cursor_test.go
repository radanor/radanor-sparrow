package inventory

import (
	"strings"
	"testing"
)

func TestInventoryCursorRoundTripAndBounds(t *testing.T) {
	name, version, source := strings.Repeat("n", MaxNameLen), strings.Repeat("v", MaxVersionLen), strings.Repeat("s", MaxSourceLen)
	encoded := EncodeCursor(name, version, source)
	if len(encoded) > MaxCursorLen {
		t.Fatal("maximum storable key exceeds cursor bound")
	}
	got, err := DecodeCursor(encoded)
	if err != nil || !got.Composite || got.Name != name || got.Version != version || got.Source != source {
		t.Fatalf("cursor lost its key: %+v %v", got, err)
	}
	for _, raw := range []string{cursorPrefix + strings.Repeat("!", 252), encoded + "AA", encoded[:len(encoded)-1] + "!", EncodeCursor("", "", ""), EncodeCursor(name+"n", version, source), EncodeCursor(name, version+"v", source), strings.Repeat("a", MaxNameLen+1)} {
		if _, err := DecodeCursor(raw); err == nil {
			t.Fatalf("invalid cursor accepted: %q", raw)
		}
	}
	for _, name := range []string{"!rs1:widget", cursorPrefix, "!rs1:" + strings.Repeat("x", 251), strings.Repeat("z", 256)} {
		got, err := DecodeCursor(name)
		if err != nil || got.Composite || got.Name != name {
			t.Fatalf("legacy namespace changed: %+v %v", got, err)
		}
	}
	short := EncodeCursor("a", "", "")
	if len(short) <= MaxNameLen {
		t.Fatal("opaque cursor overlaps the legacy namespace")
	}
	if _, err := DecodeCursor(short[:len(short)-1]); err == nil {
		t.Fatal("truncated opaque padding accepted")
	}
	legacy, err := DecodeCursor("legacy-name")
	if err != nil || legacy.Composite || legacy.Name != "legacy-name" {
		t.Fatalf("legacy name cursor: %+v %v", legacy, err)
	}
	unicode := EncodeCursor("应用程序\x00name", "1", "用户")
	if got, err := DecodeCursor(unicode); err != nil || got.Name != "应用程序\x00name" {
		t.Fatalf("framing lost unicode or embedded delimiter: %+v %v", got, err)
	}
}
