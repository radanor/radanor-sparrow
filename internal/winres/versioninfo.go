package winres

import (
	"encoding/binary"
	"fmt"
	"unicode/utf16"
)

// Version strings are written as US English Unicode, which is what the
// Translation value and the StringTable key have to agree about.
const (
	versionLanguage = 0x0409
	versionCodePage = 0x04B0
)

// fixedFileInfoSize is sizeof(VS_FIXEDFILEINFO): thirteen DWORDs.
const fixedFileInfoSize = 52

// VersionString is one named value in the version resource's StringFileInfo
// table, such as CompanyName or FileVersion.
type VersionString struct {
	Key   string
	Value string
}

// buildVersionInfo returns a complete VS_VERSIONINFO structure.
//
// The layout follows the documented structure: a header with the key
// "VS_VERSION_INFO", a binary VS_FIXEDFILEINFO value, then the StringFileInfo
// and VarFileInfo children. Every length is in bytes and covers the structure's
// children; every field is padded so the next one starts on a four-byte
// boundary.
//
// numeric carries the four 16-bit version fields Windows compares
// lexically. The human-readable version, including any pre-release or build
// identifier, belongs in the StringFileInfo entries: VS_FIXEDFILEINFO cannot
// represent one, so putting it there would be a fabricated number.
func buildVersionInfo(numeric [4]uint16, entries []VersionString) []byte {
	table := make([][]byte, 0, len(entries))
	for _, entry := range entries {
		value := utf16Bytes(entry.Value, true)
		// A text value's wValueLength counts characters, including the
		// terminating null.
		table = append(table, versionNode(entry.Key, utf16RuneCount(entry.Value)+1, 1, value))
	}
	stringTable := versionNode(versionTableKey(), 0, 1, nil, table...)
	stringFileInfo := versionNode("StringFileInfo", 0, 1, nil, stringTable)

	translation := make([]byte, 4)
	// The low-order word is the language and the high-order word the code page,
	// which is what a resource compiler emits for `0x409, 0x04B0`.
	binary.LittleEndian.PutUint32(translation, uint32(versionLanguage)|uint32(versionCodePage)<<16)
	varFileInfo := versionNode("VarFileInfo", 0, 1, nil, versionNode("Translation", 4, 0, translation))

	return versionNode("VS_VERSION_INFO", fixedFileInfoSize, 0, fixedFileInfo(numeric), stringFileInfo, varFileInfo)
}

// versionTableKey is the eight hexadecimal digits naming the language and code
// page of the string table.
func versionTableKey() string {
	return fmt.Sprintf("%04X%04X", versionLanguage, versionCodePage)
}

// versionNode assembles one structure: a six-byte header, a null-terminated
// UTF-16 key, a value, and any child structures, with four-byte padding
// wherever the next field would otherwise start misaligned.
//
// valueLength is the documented wValueLength, which is a character count for a
// text value and a byte count for a binary one.
func versionNode(key string, valueLength int, valueType uint16, value []byte, children ...[]byte) []byte {
	out := make([]byte, 0, 64+len(value))
	out = append(out, 0, 0, 0, 0, 0, 0) // wLength, wValueLength, wType
	out = append(out, utf16Bytes(key, true)...)
	out = alignToFour(out)
	out = append(out, value...)
	out = alignToFour(out)
	for _, child := range children {
		out = append(out, child...)
	}
	binary.LittleEndian.PutUint16(out[0:2], uint16(len(out)))
	binary.LittleEndian.PutUint16(out[2:4], uint16(valueLength))
	binary.LittleEndian.PutUint16(out[4:6], valueType)
	return out
}

// fixedFileInfo returns VS_FIXEDFILEINFO. dwSignature and dwStrucVersion are
// the constants the structure defines; dwFileFlagsMask reports that no
// per-file flags are set; dwFileOS and dwFileType describe a Windows 32-bit
// application.
func fixedFileInfo(numeric [4]uint16) []byte {
	out := make([]byte, fixedFileInfoSize)
	binary.LittleEndian.PutUint32(out[0:4], 0xFEEF04BD)
	binary.LittleEndian.PutUint32(out[4:8], 0x00010000)
	binary.LittleEndian.PutUint32(out[8:12], uint32(numeric[0])<<16|uint32(numeric[1]))
	binary.LittleEndian.PutUint32(out[12:16], uint32(numeric[2])<<16|uint32(numeric[3]))
	binary.LittleEndian.PutUint32(out[16:20], uint32(numeric[0])<<16|uint32(numeric[1]))
	binary.LittleEndian.PutUint32(out[20:24], uint32(numeric[2])<<16|uint32(numeric[3]))
	binary.LittleEndian.PutUint32(out[24:28], 0x3F)       // dwFileFlagsMask
	binary.LittleEndian.PutUint32(out[28:32], 0)          // dwFileFlags
	binary.LittleEndian.PutUint32(out[32:36], 0x00040004) // dwFileOS: VOS_NT_WINDOWS32
	binary.LittleEndian.PutUint32(out[36:40], 0x00000001) // dwFileType: VFT_APP
	return out
}

func alignToFour(buffer []byte) []byte {
	for len(buffer)%4 != 0 {
		buffer = append(buffer, 0)
	}
	return buffer
}

func utf16Bytes(value string, terminate bool) []byte {
	units := utf16.Encode([]rune(value))
	if terminate {
		units = append(units, 0)
	}
	out := make([]byte, len(units)*2)
	for index, unit := range units {
		binary.LittleEndian.PutUint16(out[index*2:index*2+2], unit)
	}
	return out
}

// utf16RuneCount counts the UTF-16 code units a string occupies, which is what
// a version-resource length counts. It is not the same as the number of runes
// once a character falls outside the basic multilingual plane.
func utf16RuneCount(value string) int {
	return len(utf16.Encode([]rune(value)))
}
