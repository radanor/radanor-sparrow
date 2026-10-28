// Package winres builds the Windows resource section for the distribution
// executable: the approved Radanor application icon and the product's version
// information.
//
// It emits a COFF object file — the `.syso` that the Go linker folds into the
// executable as its `.rsrc` section. That is the documented way to give a Go
// Windows binary an icon and a version resource, and it keeps the build
// toolchain to Go alone: no Resource Compiler, no MinGW, no C compiler, on
// either the Linux or the Windows build path.
//
// The object it writes matches the contract the linker actually implements
// (cmd/link/internal/loadpe plus addpersrc in cmd/link/internal/ld/pe.go), not
// a recalled description of it:
//
//   - Two sections, `.rsrc$01` and `.rsrc$02`, in that order. `.rsrc$01` holds
//     the resource directory; `.rsrc$02` holds the resource data. The linker
//     concatenates them into one `.rsrc` section in the final image, adding the
//     length of the first section to every relocated value, so an addend in
//     `.rsrc$01` is an offset into `.rsrc$02`.
//   - Section characteristics are exactly `IMAGE_SCN_CNT_INITIALIZED_DATA |
//     IMAGE_SCN_MEM_READ`. Anything else is rejected by the loader.
//   - Each relocated 32-bit field carries the offset of its data inside
//     `.rsrc$02`, and a relocation of type `ADDR32NB` (R_PEIMAGEOFF) pointing at
//     the `.rsrc$02` section symbol.
//
// The artwork is consumed byte-for-byte from the approved file. Nothing here
// resamples, recolours, crops or re-encodes it: the icon images are copied into
// the resource unchanged.
package winres

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// IconImage is one image inside an .ico file: a device-independent bitmap
// exactly as the file stores it.
type IconImage struct {
	Width    uint16
	Height   uint16
	Colors   uint8
	Planes   uint16
	BitCount uint16
	Data     []byte
}

// ParseICO reads the images out of an .ico file.
//
// It accepts only uncompressed device-independent bitmaps. A PNG-compressed
// entry is rejected with an explicit error rather than being copied through,
// because the icon this builds has to be verifiable against the approved
// artwork's exact bytes, and a silently encoder-dependent payload would not be.
// The approved file holds 32-bit DIBs, so nothing is lost by being strict.
func ParseICO(data []byte) ([]IconImage, error) {
	if len(data) < 6 {
		return nil, errors.New("icon file is too short to hold a header")
	}
	if reserved := binary.LittleEndian.Uint16(data[0:2]); reserved != 0 {
		return nil, fmt.Errorf("icon file has a non-zero reserved field (%d)", reserved)
	}
	if kind := binary.LittleEndian.Uint16(data[2:4]); kind != 1 {
		return nil, fmt.Errorf("icon file has type %d, want 1 (icon); a cursor file is not accepted", kind)
	}
	count := int(binary.LittleEndian.Uint16(data[4:6]))
	if count == 0 {
		return nil, errors.New("icon file contains no images")
	}
	if count > 64 {
		return nil, fmt.Errorf("icon file declares %d images, which is implausible", count)
	}
	if len(data) < 6+count*16 {
		return nil, errors.New("icon directory is truncated")
	}

	images := make([]IconImage, 0, count)
	for index := 0; index < count; index++ {
		entry := 6 + index*16
		width := uint16(data[entry])
		height := uint16(data[entry+1])
		// A stored zero means 256: the field is one byte wide.
		if width == 0 {
			width = 256
		}
		if height == 0 {
			height = 256
		}
		size := int(binary.LittleEndian.Uint32(data[entry+8 : entry+12]))
		offset := int(binary.LittleEndian.Uint32(data[entry+12 : entry+16]))
		if size <= 0 || offset < 0 || offset > len(data) || size > len(data)-offset {
			return nil, fmt.Errorf("icon entry %d has an out-of-range payload (offset %d, size %d, file %d bytes)", index, offset, size, len(data))
		}
		payload := data[offset : offset+size]
		if !isDeviceIndependentBitmap(payload) {
			return nil, fmt.Errorf("icon entry %d (%dx%d) is not an uncompressed bitmap; PNG-compressed icons are not accepted here", index, width, height)
		}
		images = append(images, IconImage{
			Width:    width,
			Height:   height,
			Colors:   data[entry+2],
			Planes:   binary.LittleEndian.Uint16(data[entry+4 : entry+6]),
			BitCount: binary.LittleEndian.Uint16(data[entry+6 : entry+8]),
			Data:     payload,
		})
	}
	return images, nil
}

// isDeviceIndependentBitmap reports whether the payload starts with a
// BITMAPINFOHEADER (or a larger variant of it) that states its own size. An
// icon entry's header doubles the declared height, because the XOR and AND
// masks are stacked, so the dimensions are not used to gate this.
func isDeviceIndependentBitmap(payload []byte) bool {
	if len(payload) < 16 {
		return false
	}
	switch binary.LittleEndian.Uint32(payload[0:4]) {
	case 12, 40, 52, 56, 108, 124:
		return true
	}
	return false
}

// groupIcon builds the RT_GROUP_ICON payload: the directory that tells Windows
// which of the RT_ICON resources belong to this icon, in which size. Byte and
// bit layouts follow the documented GRPICONDIR and GRPICONDIRENTRY structures.
func groupIcon(images []IconImage, ids []uint16) []byte {
	const entrySize = 14
	out := make([]byte, 6+entrySize*len(images))
	binary.LittleEndian.PutUint16(out[0:2], 0) // idReserved
	binary.LittleEndian.PutUint16(out[2:4], 1) // idType: icon
	binary.LittleEndian.PutUint16(out[4:6], uint16(len(images)))
	for index, image := range images {
		offset := 6 + index*entrySize
		out[offset] = storedDimension(image.Width)
		out[offset+1] = storedDimension(image.Height)
		out[offset+2] = image.Colors
		out[offset+3] = 0 // bReserved
		planes, bits := image.Planes, image.BitCount
		if planes == 0 {
			planes = 1
		}
		if bits == 0 {
			bits = 32
		}
		binary.LittleEndian.PutUint16(out[offset+4:offset+6], planes)
		binary.LittleEndian.PutUint16(out[offset+6:offset+8], bits)
		binary.LittleEndian.PutUint32(out[offset+8:offset+12], uint32(len(image.Data)))
		binary.LittleEndian.PutUint16(out[offset+12:offset+14], ids[index])
	}
	return out
}

// storedDimension is the one-byte width or height a directory records, where a
// stored zero stands for 256.
func storedDimension(value uint16) uint8 {
	if value >= 256 {
		return 0
	}
	return uint8(value)
}
