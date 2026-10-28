package winres

import (
	"encoding/binary"
	"errors"
	"fmt"
	"unicode/utf16"
)

// VersionInfo is a version resource that was read back from an executable,
// rather than the one this package wrote. The packaging tests read the artifact
// the linker produced, so a resource that was never linked in, or one whose
// fields drifted from the build identity, fails instead of being implied by a
// byte sequence that happens to occur somewhere in the file.
type VersionInfo struct {
	// FileVersion and ProductVersion are the four 16-bit numbers stored in
	// VS_FIXEDFILEINFO, which is the only part of the resource Windows compares
	// when it decides whether one file is newer than another.
	FileVersion    [4]uint16
	ProductVersion [4]uint16
	// Strings are the named values from every StringFileInfo table, such as
	// FileVersion and ProductName. A key that appears in more than one table
	// keeps the value from the first, because the tables describe one build.
	Strings map[string]string
}

// String returns a named value, or the empty string when the resource does not
// carry it.
func (info VersionInfo) String(key string) string {
	return info.Strings[key]
}

// ParseVersionInfo parses a VS_VERSIONINFO resource blob, as returned in the
// RT_VERSION entry of a PE resource directory.
//
// The structure is a tree of length-prefixed, four-byte-aligned nodes: the root
// VS_VERSION_INFO, a VarFileInfo sibling and a StringFileInfo subtree holding
// the named values. The parser checks every length it follows against the bytes
// actually present, so a truncated or malformed resource is an error rather than
// an empty result that a caller could mistake for a match.
func ParseVersionInfo(blob []byte) (VersionInfo, error) {
	root, err := readVersionNode(blob, "VS_VERSION_INFO")
	if err != nil {
		return VersionInfo{}, err
	}
	if root.key != "VS_VERSION_INFO" {
		return VersionInfo{}, fmt.Errorf("the version resource's root node is %q, want VS_VERSION_INFO", root.key)
	}
	if len(root.value) != fixedFileInfoSize {
		return VersionInfo{}, fmt.Errorf("VS_FIXEDFILEINFO is %d bytes, want %d", len(root.value), fixedFileInfoSize)
	}
	if signature := binary.LittleEndian.Uint32(root.value[0:4]); signature != 0xFEEF04BD {
		return VersionInfo{}, fmt.Errorf("VS_FIXEDFILEINFO signature is 0x%08x, want 0xfeef04bd", signature)
	}

	info := VersionInfo{Strings: map[string]string{}}
	info.FileVersion = fixedVersion(root.value[8:12], root.value[12:16])
	info.ProductVersion = fixedVersion(root.value[16:20], root.value[20:24])
	for _, child := range root.children {
		if child.key != "StringFileInfo" {
			continue
		}
		for _, table := range child.children {
			for _, entry := range table.children {
				// A value is text only when the node says so; a binary value
				// with the same key would not be a version string.
				if entry.typ != 1 {
					continue
				}
				if _, seen := info.Strings[entry.key]; !seen {
					info.Strings[entry.key] = decodeVersionString(entry.value)
				}
			}
		}
	}
	if len(info.Strings) == 0 {
		return VersionInfo{}, errors.New("the version resource carries no StringFileInfo values")
	}
	return info, nil
}

// fixedVersion joins a version pair's high and low words.
func fixedVersion(high, low []byte) [4]uint16 {
	first := binary.LittleEndian.Uint32(high)
	second := binary.LittleEndian.Uint32(low)
	return [4]uint16{uint16(first >> 16), uint16(first), uint16(second >> 16), uint16(second)}
}

// versionNodeView is one parsed VS_VERSIONINFO node.
type versionNodeView struct {
	key      string
	typ      uint16
	value    []byte
	children []versionNodeView
}

// readVersionNode parses one node and, recursively, its children. path names
// the node in an error message, because a malformed resource is otherwise only a
// byte offset.
func readVersionNode(data []byte, path string) (versionNodeView, error) {
	if len(data) < 6 {
		return versionNodeView{}, fmt.Errorf("%s: %d bytes, want at least the 6-byte node header", path, len(data))
	}
	length := int(binary.LittleEndian.Uint16(data[0:2]))
	valueLength := int(binary.LittleEndian.Uint16(data[2:4]))
	nodeType := binary.LittleEndian.Uint16(data[4:6])
	if length < 6 {
		return versionNodeView{}, fmt.Errorf("%s: node length %d is shorter than its header", path, length)
	}
	if length > len(data) {
		return versionNodeView{}, fmt.Errorf("%s: node declares %d bytes, only %d remain", path, length, len(data))
	}
	node := data[:length]

	// The key is a null-terminated UTF-16 string immediately after the header.
	keyEnd := -1
	for offset := 6; offset+1 < len(node); offset += 2 {
		if node[offset] == 0 && node[offset+1] == 0 {
			keyEnd = offset
			break
		}
	}
	if keyEnd < 0 {
		return versionNodeView{}, fmt.Errorf("%s: the node key is not null-terminated", path)
	}
	key := decodeVersionString(node[6 : keyEnd+2])

	// The value follows the key and the padding that aligns it to four bytes.
	valueStart := align(keyEnd + 2)
	valueBytes := valueLength
	if nodeType == 1 {
		// A text value's length is a count of UTF-16 characters, including the
		// terminator, not a byte count.
		valueBytes *= 2
	}
	if valueStart+valueBytes > len(node) {
		return versionNodeView{}, fmt.Errorf("%s: value declares %d bytes, only %d remain", path, valueBytes, len(node)-valueStart)
	}
	value := node[valueStart : valueStart+valueBytes]

	children := make([]versionNodeView, 0, 2)
	for offset := align(valueStart + valueBytes); offset+6 <= len(node); {
		childLength := int(binary.LittleEndian.Uint16(node[offset : offset+2]))
		if childLength == 0 {
			break
		}
		if offset+childLength > len(node) {
			return versionNodeView{}, fmt.Errorf("%s: child at %d declares %d bytes, only %d remain", path, offset, childLength, len(node)-offset)
		}
		childPath := path + "/" + versionNodeKey(node[offset:offset+childLength])
		child, err := readVersionNode(node[offset:offset+childLength], childPath)
		if err != nil {
			return versionNodeView{}, err
		}
		children = append(children, child)
		offset = align(offset + childLength)
	}
	return versionNodeView{key: key, typ: nodeType, value: value, children: children}, nil
}

// align rounds a byte offset up to the four-byte boundary these structures use.
func align(offset int) int {
	return (offset + 3) &^ 3
}

// versionNodeKey reads a node's key without validating the rest of it, so an
// error inside a child can be reported under that child's name rather than
// under its position.
func versionNodeKey(data []byte) string {
	if len(data) < 8 {
		return "?"
	}
	for offset := 6; offset+1 < len(data); offset += 2 {
		if data[offset] == 0 && data[offset+1] == 0 {
			return decodeVersionString(data[6 : offset+2])
		}
	}
	return "?"
}

// decodeVersionString reads a little-endian UTF-16 string and drops its
// terminator.
func decodeVersionString(data []byte) string {
	units := make([]uint16, 0, len(data)/2)
	for offset := 0; offset+1 < len(data); offset += 2 {
		units = append(units, binary.LittleEndian.Uint16(data[offset:offset+2]))
	}
	for len(units) > 0 && units[len(units)-1] == 0 {
		units = units[:len(units)-1]
	}
	return string(utf16.Decode(units))
}
