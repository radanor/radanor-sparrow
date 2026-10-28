package winres

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

// The parser is the inverse of the writer, so a round trip proves the writer
// produces the documented structure and that a linked executable's resource can
// be read back field by field instead of guessed at.
func TestParseVersionInfoReadsWhatWasWritten(t *testing.T) {
	blob := buildVersionInfo([4]uint16{1, 2, 3, 4}, []VersionString{
		{Key: "FileVersion", Value: "1.2.3-dev.0+gabcdef012345.dirty"},
		{Key: "ProductVersion", Value: "1.2.3-dev.0+gabcdef012345.dirty"},
		{Key: "ProductName", Value: "Radanor Sparrow"},
		{Key: "OriginalFilename", Value: "RadanorSparrow.exe"},
	})
	info, err := ParseVersionInfo(blob)
	if err != nil {
		t.Fatalf("the version resource the writer produced could not be parsed: %v", err)
	}
	if info.FileVersion != [4]uint16{1, 2, 3, 4} {
		t.Errorf("file version = %v, want [1 2 3 4]", info.FileVersion)
	}
	if info.ProductVersion != [4]uint16{1, 2, 3, 4} {
		t.Errorf("product version = %v, want [1 2 3 4]", info.ProductVersion)
	}
	for key, want := range map[string]string{
		"FileVersion":      "1.2.3-dev.0+gabcdef012345.dirty",
		"ProductVersion":   "1.2.3-dev.0+gabcdef012345.dirty",
		"ProductName":      "Radanor Sparrow",
		"OriginalFilename": "RadanorSparrow.exe",
	} {
		if got := info.String(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	if got := info.String("CompanyName"); got != "" {
		t.Errorf("CompanyName = %q, want it absent", got)
	}
}

// A malformed resource has to be an error. Returning an empty VersionInfo would
// let a packaging check compare nothing and pass.
func TestParseVersionInfoRejectsMalformedResources(t *testing.T) {
	valid := buildVersionInfo([4]uint16{9, 9, 9, 9}, []VersionString{{Key: "FileVersion", Value: "9.9.9"}})

	brokenSignature := append([]byte(nil), valid...)
	if at := bytes.Index(brokenSignature, []byte{0xBD, 0x04, 0xEF, 0xFE}); at >= 0 {
		binary.LittleEndian.PutUint32(brokenSignature[at:at+4], 0x12345678)
	} else {
		t.Fatal("the written resource has no VS_FIXEDFILEINFO signature")
	}

	overlongNode := append([]byte(nil), valid...)
	binary.LittleEndian.PutUint16(overlongNode[0:2], 0xFFF0)

	for name, blob := range map[string][]byte{
		"empty":                        nil,
		"shorter than a header":        valid[:5],
		"declared length past the end": overlongNode,
		"truncated mid-resource":       valid[:len(valid)-8],
		"wrong fixed-info signature":   brokenSignature,
		"no string table":              buildVersionInfo([4]uint16{1, 0, 0, 0}, nil),
	} {
		if _, err := ParseVersionInfo(blob); err == nil {
			t.Errorf("%s: the malformed version resource was accepted", name)
		} else if strings.TrimSpace(err.Error()) == "" {
			t.Errorf("%s: the error does not say what is wrong", name)
		}
	}
}
