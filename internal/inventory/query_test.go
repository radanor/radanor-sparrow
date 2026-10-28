package inventory

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestQueryFilterTransportValidation(t *testing.T) {
	for _, raw := range []string{"", strings.Repeat("\x00", 128), strings.Repeat("&", 128)} {
		encoded := base64.RawURLEncoding.EncodeToString([]byte(raw))
		got, err := DecodeQueryFilter("", &encoded, 128)
		if err != nil || got != raw {
			t.Fatalf("roundtrip: %q %v", got, err)
		}
	}
	for _, encoded := range []string{"!", "YQ==", "YR", base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("x", 129)))} {
		if _, err := DecodeQueryFilter("", &encoded, 128); err == nil {
			t.Fatal("invalid encoding accepted")
		}
	}
	encoded := "YQ"
	if _, err := DecodeQueryFilter("legacy", &encoded, 128); err == nil {
		t.Fatal("conflicting fields accepted")
	}
	if got, err := DecodeQueryFilter("!rs1:widget", nil, 128); err != nil || got != "!rs1:widget" {
		t.Fatal("legacy filter rejected")
	}
}
