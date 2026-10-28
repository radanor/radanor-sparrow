package inventory

import (
	"encoding/base64"
	"encoding/binary"
	"errors"
	"strings"
)

const cursorPrefix = "!rs1:"
const minCursorPayload = 192 // Encoded tokens exceed every valid legacy name (256 bytes).

// MaxCursorLen bounds an encoded name/version/source key, including framing.
const MaxCursorLen = len(cursorPrefix) + ((6+MaxNameLen+MaxVersionLen+MaxSourceLen+2)/3)*4

type Cursor struct {
	Name, Version, Source string
	Composite             bool
}

// EncodeCursor preserves every component of the inventory primary key.
func EncodeCursor(name, version, source string) string {
	var payload []byte
	for _, part := range []string{name, version, source} {
		payload = binary.LittleEndian.AppendUint16(payload, uint16(len(part)))
		payload = append(payload, part...)
	}
	for len(payload) < minCursorPayload {
		payload = append(payload, 0)
	}
	return cursorPrefix + base64.RawURLEncoding.EncodeToString(payload)
}

// DecodeCursor accepts legacy name cursors and bounded, versioned key cursors.
func DecodeCursor(raw string) (Cursor, error) {
	invalid := errors.New("invalid inventory cursor")
	// Length is the namespace discriminator, not a prefix a real name can contain.
	if len(raw) <= MaxNameLen {
		return Cursor{Name: raw}, nil
	}
	if !strings.HasPrefix(raw, cursorPrefix) || len(raw) > MaxCursorLen {
		return Cursor{}, invalid
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(raw, cursorPrefix))
	if err != nil {
		return Cursor{}, invalid
	}
	var parts [3]string
	for i, bound := range []int{MaxNameLen, MaxVersionLen, MaxSourceLen} {
		if len(payload) < 2 {
			return Cursor{}, invalid
		}
		n := int(binary.LittleEndian.Uint16(payload))
		payload = payload[2:]
		if n > bound || n > len(payload) || (i == 0 && n == 0) {
			return Cursor{}, invalid
		}
		parts[i] = string(payload[:n])
		payload = payload[n:]
	}
	// Re-encoding checks padding, canonical base64 and trailing bytes together.
	if EncodeCursor(parts[0], parts[1], parts[2]) != raw {
		return Cursor{}, invalid
	}
	return Cursor{Name: parts[0], Version: parts[1], Source: parts[2], Composite: true}, nil
}
