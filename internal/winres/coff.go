package winres

import (
	"encoding/binary"
	"fmt"
)

// COFF object-file constants, from the PE/COFF specification.
const (
	coffHeaderSize  = 20
	coffSectionSize = 40
	coffSymbolSize  = 18
	coffRelocSize   = 10

	imageScnCntInitializedData = 0x00000040
	imageScnMemRead            = 0x40000000
	imageSymClassStatic        = 3

	// Symbol indices in the object's symbol table.
	symbolSectionData = 1
)

// machineFor maps an architecture name to its COFF machine type and to the
// relocation type the Go linker accepts for a 32-bit image-relative address.
// Both of those relocation types map to R_PEIMAGEOFF, which is what a resource
// directory's addresses are.
func machineFor(architecture string) (machine, relocationType uint16, ok bool) {
	switch architecture {
	case "amd64":
		return 0x8664, 0x0003, true // IMAGE_REL_AMD64_ADDR32NB
	case "arm64":
		return 0xAA64, 0x0002, true // IMAGE_REL_ARM64_ADDR32NB
	}
	return 0, 0, false
}

// writeCOFF assembles the object file: a header, two section headers, the
// resource directory, its relocations, the resource data, the section symbols
// and a (empty) string table.
//
// The section characteristics matter more than they look. The linker accepts a
// resource section only when its characteristics are exactly
// IMAGE_SCN_CNT_INITIALIZED_DATA|IMAGE_SCN_MEM_READ; anything else is rejected
// with an error rather than being linked.
func writeCOFF(architecture string, directory, data []byte, fixups []fixup) ([]byte, error) {
	machine, relocationType, ok := machineFor(architecture)
	if !ok {
		return nil, fmt.Errorf("unsupported architecture %q: the Windows resources are built for amd64 or arm64", architecture)
	}

	sections := []struct {
		name string
		body []byte
	}{
		{".rsrc$01", directory},
		{".rsrc$02", data},
	}
	const sectionCount = 2
	relocationCount := [sectionCount]int{}
	relocationCount[0] = len(fixups)

	bodyOffset := [sectionCount]int{}
	relocationOffset := [sectionCount]int{}
	cursor := coffHeaderSize + coffSectionSize*sectionCount
	for index, section := range sections {
		bodyOffset[index] = cursor
		cursor += len(section.body)
		if relocationCount[index] > 0 {
			relocationOffset[index] = cursor
			cursor += coffRelocSize * relocationCount[index]
		}
	}
	symbolTableOffset := cursor
	cursor += coffSymbolSize * sectionCount
	stringTableOffset := cursor
	cursor += 4

	out := make([]byte, cursor)
	binary.LittleEndian.PutUint16(out[0:2], machine)
	binary.LittleEndian.PutUint16(out[2:4], sectionCount)
	// Zero timestamp and no optional header: the object is reproducible.
	binary.LittleEndian.PutUint32(out[8:12], uint32(symbolTableOffset))
	binary.LittleEndian.PutUint32(out[12:16], sectionCount)

	for index, section := range sections {
		header := coffHeaderSize + index*coffSectionSize
		copy(out[header:header+8], section.name)
		binary.LittleEndian.PutUint32(out[header+16:header+20], uint32(len(section.body)))
		binary.LittleEndian.PutUint32(out[header+20:header+24], uint32(bodyOffset[index]))
		if relocationCount[index] > 0 {
			binary.LittleEndian.PutUint32(out[header+24:header+28], uint32(relocationOffset[index]))
			binary.LittleEndian.PutUint16(out[header+32:header+34], uint16(relocationCount[index]))
		}
		binary.LittleEndian.PutUint32(out[header+36:header+40], imageScnCntInitializedData|imageScnMemRead)
		copy(out[bodyOffset[index]:], section.body)
	}

	for index, item := range fixups {
		entry := relocationOffset[0] + index*coffRelocSize
		binary.LittleEndian.PutUint32(out[entry:entry+4], uint32(item.offset))
		binary.LittleEndian.PutUint32(out[entry+4:entry+8], symbolSectionData)
		binary.LittleEndian.PutUint16(out[entry+8:entry+10], relocationType)
	}

	// One static section symbol per section, with no auxiliary records: the
	// loader only reads an auxiliary record for a COMDAT section, and neither
	// of these is one.
	for index, section := range sections {
		entry := symbolTableOffset + index*coffSymbolSize
		copy(out[entry:entry+8], section.name)
		binary.LittleEndian.PutUint16(out[entry+12:entry+14], uint16(index+1))
		out[entry+16] = imageSymClassStatic
	}

	binary.LittleEndian.PutUint32(out[stringTableOffset:stringTableOffset+4], 4)
	return out, nil
}
