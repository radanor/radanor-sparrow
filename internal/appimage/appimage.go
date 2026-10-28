// Package appimage reads the type-2 AppImage container that
// scripts/build-appimage.sh produces.
//
// The format is an ELF runtime with a squashfs image appended immediately after
// its section-header table. An AppImage can therefore be inspected without FUSE
// or mounting: the filesystem starts at the computed offset, and unsquashfs (or
// any squashfs reader) can read it from there. The tests use this to validate the
// artifact that was actually built.
package appimage

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
)

// Info describes a type-2 AppImage on disk.
type Info struct {
	// Path is the artifact.
	Path string
	// Size is the whole artifact in bytes.
	Size int64
	// Offset is where the appended squashfs image starts.
	Offset int64
}

// Read validates the ELF runtime and the AppImage magic, computes the squashfs
// offset the same way the runtime does, and confirms the squashfs magic is there.
// A file that is merely an ELF or merely a squashfs is refused.
func Read(path string) (Info, error) {
	file, err := os.Open(path)
	if err != nil {
		return Info{}, err
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		return Info{}, err
	}
	info := Info{Path: path, Size: stat.Size()}

	header := make([]byte, 64)
	if _, err := io.ReadFull(file, header); err != nil {
		return Info{}, fmt.Errorf("read ELF header: %w", err)
	}
	if !bytes.Equal(header[:4], []byte{0x7f, 'E', 'L', 'F'}) {
		return Info{}, errors.New("not an ELF file, so not a type-2 AppImage")
	}
	if header[4] != 2 || header[5] != 1 {
		return Info{}, errors.New("not a 64-bit little-endian ELF, which is what the AppImage runtime must be")
	}
	// The runtime marks the file as an AppImage type 2 in the EI_PAD bytes.
	if !bytes.Equal(header[8:11], []byte{'A', 'I', 2}) {
		return Info{}, fmt.Errorf("missing the AppImage type-2 magic at offset 8 (found %x)", header[8:11])
	}

	order := binary.LittleEndian
	sectionOffset := int64(order.Uint64(header[0x28:0x30]))
	sectionSize := int64(order.Uint16(header[0x3a:0x3c]))
	sectionCount := int64(order.Uint16(header[0x3c:0x3e]))
	if sectionOffset <= 0 || sectionSize <= 0 {
		return Info{}, errors.New("the ELF runtime has no section table, so the image cannot be located")
	}
	info.Offset = sectionOffset + sectionSize*sectionCount
	if info.Offset <= 0 || info.Offset+4 > info.Size {
		return Info{}, fmt.Errorf("the squashfs offset %d is outside the %d-byte artifact", info.Offset, info.Size)
	}

	magic := make([]byte, 4)
	if _, err := file.ReadAt(magic, info.Offset); err != nil {
		return Info{}, fmt.Errorf("read squashfs magic: %w", err)
	}
	// "hsqs" is squashfs's little-endian magic; the appended image is always
	// uncompressed at this point, so it is readable directly.
	if !bytes.Equal(magic, []byte{'h', 's', 'q', 's'}) {
		return Info{}, fmt.Errorf("no squashfs image at offset %d (found %x)", info.Offset, magic)
	}
	return info, nil
}
