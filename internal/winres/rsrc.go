package winres

import (
	"encoding/binary"
	"sort"
)

// Resource type identifiers from the Windows headers.
const (
	resourceTypeIcon      = 3
	resourceTypeGroupIcon = 14
	resourceTypeVersion   = 16
)

// resourceLanguage is the language every resource is registered under, chosen
// to match the version resource's Translation value.
const resourceLanguage = versionLanguage

// resource is one blob of resource data with the type and name it is filed
// under.
type resource struct {
	kind uint32
	name uint32
	data []byte
}

// fixup marks a 32-bit field in the resource directory that must become the
// address of resource data. addend is that data's offset inside `.rsrc$02`;
// the linker adds the section's address and the length of `.rsrc$01` to it when
// it lays the sections out.
type fixup struct {
	offset int
	addend int
}

// buildResources lays out a three-level resource directory — type, then name,
// then language, then a data entry per leaf — together with the data section
// it refers to.
//
// The layout is deterministic: types and names are sorted, so the same input
// produces byte-identical output and the resulting object is reproducible.
func buildResources(resources []resource) ([]byte, []byte, []fixup) {
	grouped := map[uint32][]resource{}
	var kinds []uint32
	for _, item := range resources {
		if _, seen := grouped[item.kind]; !seen {
			kinds = append(kinds, item.kind)
		}
		grouped[item.kind] = append(grouped[item.kind], item)
	}
	sort.Slice(kinds, func(i, j int) bool { return kinds[i] < kinds[j] })
	for _, kind := range kinds {
		entries := grouped[kind]
		sort.Slice(entries, func(i, j int) bool { return entries[i].name < entries[j].name })
		grouped[kind] = entries
	}

	// Offsets are relative to the start of the resource directory: the
	// directories first, then one data entry per leaf. Every size here is a
	// multiple of four, so no padding is needed and every offset stays aligned.
	kindOffset := make(map[uint32]int, len(kinds))
	nameOffset := make(map[[2]uint32]int, len(resources))
	dataEntryOffset := make(map[[3]uint32]int, len(resources))
	cursor := 16 + 8*len(kinds)
	for _, kind := range kinds {
		kindOffset[kind] = cursor
		cursor += 16 + 8*len(grouped[kind])
	}
	for _, kind := range kinds {
		for _, item := range grouped[kind] {
			nameOffset[[2]uint32{kind, item.name}] = cursor
			cursor += 16 + 8 // exactly one language entry per name
		}
	}
	for _, kind := range kinds {
		for _, item := range grouped[kind] {
			dataEntryOffset[[3]uint32{kind, item.name, resourceLanguage}] = cursor
			cursor += 16
		}
	}

	directory := make([]byte, cursor)
	binary.LittleEndian.PutUint16(directory[14:16], uint16(len(kinds)))
	for index, kind := range kinds {
		entry := 16 + index*8
		binary.LittleEndian.PutUint32(directory[entry:entry+4], kind)
		binary.LittleEndian.PutUint32(directory[entry+4:entry+8], uint32(kindOffset[kind])|0x80000000)
	}
	for _, kind := range kinds {
		base := kindOffset[kind]
		binary.LittleEndian.PutUint16(directory[base+14:base+16], uint16(len(grouped[kind])))
		for index, item := range grouped[kind] {
			entry := base + 16 + index*8
			binary.LittleEndian.PutUint32(directory[entry:entry+4], item.name)
			binary.LittleEndian.PutUint32(directory[entry+4:entry+8], uint32(nameOffset[[2]uint32{kind, item.name}])|0x80000000)
		}
	}
	for _, kind := range kinds {
		for _, item := range grouped[kind] {
			base := nameOffset[[2]uint32{kind, item.name}]
			binary.LittleEndian.PutUint16(directory[base+14:base+16], 1)
			binary.LittleEndian.PutUint32(directory[base+16:base+20], resourceLanguage)
			// IMAGE_RESOURCE_DIRECTORY_ENTRY sets the high bit for a
			// subdirectory; a language leaf points to data with it clear.
			binary.LittleEndian.PutUint32(directory[base+20:base+24], uint32(dataEntryOffset[[3]uint32{kind, item.name, resourceLanguage}]))
		}
	}

	var data []byte
	var fixups []fixup
	for _, kind := range kinds {
		for _, item := range grouped[kind] {
			// Keep each blob four-byte aligned, the way a resource compiler
			// lays them out.
			data = alignToFour(data)
			offset := dataEntryOffset[[3]uint32{kind, item.name, resourceLanguage}]
			addend := len(data)
			// The value written here is the relocation's addend; the linker
			// replaces it with the address of the data.
			binary.LittleEndian.PutUint32(directory[offset:offset+4], uint32(addend))
			binary.LittleEndian.PutUint32(directory[offset+4:offset+8], uint32(len(item.data)))
			fixups = append(fixups, fixup{offset: offset, addend: addend})
			data = append(data, item.data...)
		}
	}
	return directory, data, fixups
}
