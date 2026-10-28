package winres

import (
	"debug/pe"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
)

// resourceDirectoryIndex is IMAGE_DIRECTORY_ENTRY_RESOURCE in the PE optional
// header's data directory.
const resourceDirectoryIndex = 2

// Options describe the resource to build.
type Options struct {
	// Icon is the approved .ico file, used byte-for-byte.
	Icon []byte
	// Architecture is the Go architecture name the object is built for.
	Architecture string
	Company      string
	Product      string
	Description  string
	// Version is the human-readable version, including any pre-release or build
	// identifier. It appears in the version resource's string fields.
	Version string
	// Copyright is written to `LegalCopyright`, the field that conventionally
	// names the copyright holder. Callers pass the holder the Business Source
	// License names (branding.CopyrightNotice); an empty value omits the field
	// entirely rather than writing a wrong or empty attribution.
	Copyright string
	// Numeric is the four 16-bit fields VS_FIXEDFILEINFO can represent. It
	// cannot hold a pre-release suffix, which is why Version is separate.
	Numeric          [4]uint16
	OriginalFilename string
}

// Build returns the COFF object that gives the Windows executable its @icon and
// its version information. The caller's Copyright is written as LegalCopyright,
// the field that names the copyright holder, and omitted when it is empty, so a
// build with no verified holder attributes the file to nobody rather than to a
// guessed name.
func Build(options Options) ([]byte, error) {
	if len(options.Version) == 0 {
		return nil, errors.New("no version was supplied; the version resource would be empty")
	}
	if _, _, ok := machineFor(options.Architecture); !ok {
		return nil, fmt.Errorf("unsupported architecture %q", options.Architecture)
	}
	images, err := ParseICO(options.Icon)
	if err != nil {
		return nil, err
	}
	if len(images) == 0 {
		return nil, errors.New("the icon contains no images")
	}

	ids := make([]uint16, len(images))
	resources := make([]resource, 0, len(images)+2)
	for index, image := range images {
		ids[index] = uint16(index + 1)
		resources = append(resources, resource{kind: resourceTypeIcon, name: uint32(ids[index]), data: image.Data})
	}
	resources = append(resources, resource{kind: resourceTypeGroupIcon, name: 1, data: groupIcon(images, ids)})
	resources = append(resources, resource{kind: resourceTypeVersion, name: 1, data: buildVersionInfo(options.Numeric, options.versionStrings())})

	directory, data, fixups := buildResources(resources)
	return writeCOFF(options.Architecture, directory, data, fixups)
}

// versionStrings are the named values Windows shows in a file's Properties
// dialog. The same version string is reported as both the file and the product
// version, because they are built from one value.
func (options Options) versionStrings() []VersionString {
	strings := []VersionString{
		{Key: "CompanyName", Value: options.Company},
		{Key: "FileDescription", Value: options.Description},
		{Key: "FileVersion", Value: options.Version},
		{Key: "InternalName", Value: options.Product},
		{Key: "OriginalFilename", Value: options.OriginalFilename},
		{Key: "ProductName", Value: options.Product},
		{Key: "ProductVersion", Value: options.Version},
	}
	// LegalCopyright is the one string that must never appear empty: a blank
	// attribution in a file's Properties dialog is worse than none, and it would
	// look like a claim about the holder. Absent, it simply says nothing.
	if options.Copyright != "" {
		strings = append(strings, VersionString{Key: "LegalCopyright", Value: options.Copyright})
	}
	return strings
}

// Resource type identifiers, as they are filed in a resource directory. They
// are exported so the packaging tests — which live beside the distribution
// rather than beside this package — can assert on a built executable's tree
// without restating the numbers.
const (
	ResourceIcon      = resourceTypeIcon      // RT_ICON
	ResourceGroupIcon = resourceTypeGroupIcon // RT_GROUP_ICON
	ResourceVersion   = resourceTypeVersion   // RT_VERSION
)

// Entry is one resource found in a built executable.
type Entry struct {
	Type     uint32
	Name     uint32
	Language uint32
	Data     []byte
}

// ReadResources parses the resource directory out of a built Windows
// executable. It exists so the packaging tests can assert what the linked image
// actually contains, rather than trusting that the object was written
// correctly. Most of it is the inverse of buildResources, and it is checked
// against an independent parser in the test suite.
func ReadResources(path string) ([]Entry, error) {
	file, err := pe.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var directory pe.DataDirectory
	switch header := file.OptionalHeader.(type) {
	case *pe.OptionalHeader64:
		directory = header.DataDirectory[resourceDirectoryIndex]
	case *pe.OptionalHeader32:
		directory = header.DataDirectory[resourceDirectoryIndex]
	default:
		return nil, errors.New("the executable has no PE optional header")
	}
	if directory.VirtualAddress == 0 || directory.Size == 0 {
		return nil, errors.New("the executable has no resource directory: it carries no icon and no version information")
	}

	data, base, err := sectionBytesForRVA(file, directory.VirtualAddress)
	if err != nil {
		return nil, err
	}
	var entries []Entry
	if err := walkResourceDirectory(file, data, base, base, 0, nil, &entries); err != nil {
		return nil, err
	}
	return entries, nil
}

// sectionBytesForRVA returns a section's contents and the offset of an address
// inside it.
func sectionBytesForRVA(file *pe.File, rva uint32) ([]byte, int, error) {
	for _, section := range file.Sections {
		size := section.VirtualSize
		if size == 0 {
			size = section.Size
		}
		if rva < section.VirtualAddress || rva >= section.VirtualAddress+size {
			continue
		}
		data, err := section.Data()
		if err != nil {
			return nil, 0, err
		}
		offset := int(rva - section.VirtualAddress)
		if offset > len(data) {
			return nil, 0, fmt.Errorf("address %#x is past the end of section %s", rva, section.Name)
		}
		return data, offset, nil
	}
	return nil, 0, fmt.Errorf("no section contains address %#x", rva)
}

// resourceEntry is one name/ID entry of a resource directory.
type resourceEntry struct {
	id     uint32
	child  uint32
	isLeaf bool
}

func directoryEntriesAt(data []byte, offset int) ([]resourceEntry, error) {
	if offset < 0 || offset+16 > len(data) {
		return nil, fmt.Errorf("resource directory at %d is out of range", offset)
	}
	named := int(binary.LittleEndian.Uint16(data[offset+12 : offset+14]))
	byID := int(binary.LittleEndian.Uint16(data[offset+14 : offset+16]))
	if named != 0 {
		return nil, fmt.Errorf("resource directory at %d has %d string-named entries; this build uses numeric identifiers only", offset, named)
	}
	entries := make([]resourceEntry, 0, byID)
	for index := 0; index < byID; index++ {
		at := offset + 16 + index*8
		if at+8 > len(data) {
			return nil, fmt.Errorf("resource directory at %d is truncated", offset)
		}
		name := binary.LittleEndian.Uint32(data[at : at+4])
		child := binary.LittleEndian.Uint32(data[at+4 : at+8])
		entry := resourceEntry{id: name, isLeaf: child&0x80000000 == 0}
		if child&0x80000000 != 0 {
			entry.child = child &^ 0x80000000
		} else {
			entry.child = child
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func walkResourceDirectory(file *pe.File, data []byte, directoryBase, offset, depth int, path []uint32, out *[]Entry) error {
	entries, err := directoryEntriesAt(data, offset)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.isLeaf {
			if depth >= 2 {
				return fmt.Errorf("resource tree beneath %v descends deeper than type, name and language", path)
			}
			if err := walkResourceDirectory(file, data, directoryBase, directoryBase+int(entry.child), depth+1, append(append([]uint32{}, path...), entry.id), out); err != nil {
				return err
			}
			continue
		}
		next := append(append([]uint32{}, path...), entry.id)
		if len(next) != 3 {
			return fmt.Errorf("resource entry %v has %d identifiers, want type, name and language", next, len(next))
		}
		leaf := directoryBase + int(entry.child)
		if leaf < 0 || leaf+16 > len(data) {
			return fmt.Errorf("data entry for %v is out of range", next)
		}
		address := binary.LittleEndian.Uint32(data[leaf : leaf+4])
		size := binary.LittleEndian.Uint32(data[leaf+4 : leaf+8])
		blob, blobOffset, err := sectionBytesForRVA(file, address)
		if err != nil {
			return fmt.Errorf("resource %v: %w", next, err)
		}
		if uint64(blobOffset)+uint64(size) > uint64(len(blob)) {
			return fmt.Errorf("resource %v at %#x declares %d bytes, past the end of its section", next, address, size)
		}
		*out = append(*out, Entry{
			Type:     next[0],
			Name:     next[1],
			Language: next[2],
			Data:     blob[blobOffset : blobOffset+int(size)],
		})
	}
	return nil
}

// ReadIconFile is a small convenience for callers that have a path rather than
// bytes, so a missing or unreadable approved icon fails with the file named.
func ReadIconFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read the approved icon: %w", err)
	}
	return data, nil
}
