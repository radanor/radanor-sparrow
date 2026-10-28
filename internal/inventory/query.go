package inventory

import (
	"encoding/base64"
	"errors"
)

// DecodeQueryFilter keeps legacy JSON strings valid while permitting a bounded
// base64 transport form whose size does not depend on JSON character escaping.
func DecodeQueryFilter(legacy string, encoded *string, bound int) (string, error) {
	if encoded == nil {
		if len(legacy) > bound {
			return "", errors.New("invalid inventory filter")
		}
		return legacy, nil
	}
	if legacy != "" || len(*encoded) > base64.RawURLEncoding.EncodedLen(bound) {
		return "", errors.New("invalid inventory filter")
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(*encoded)
	if err != nil || len(decoded) > bound || base64.RawURLEncoding.EncodeToString(decoded) != *encoded {
		return "", errors.New("invalid inventory filter")
	}
	return string(decoded), nil
}
