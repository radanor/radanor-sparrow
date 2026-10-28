package gateway

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/radanor/radanor-shield/internal/inventory"
	"github.com/radanor/radanor-shield/internal/ipc"
)

func TestInventoryCursorTransportBounds(t *testing.T) {
	after := inventory.EncodeCursor(strings.Repeat("n", 256), strings.Repeat("v", 128), strings.Repeat("s", 64))
	for _, filter := range []string{strings.Repeat("&", 128), strings.Repeat("\x00", 128), strings.Repeat("\"", 128), strings.Repeat("<", 128)} {
		for _, after := range []string{after, strings.Repeat("\x00", 256), "!rs1:widget"} {
			query := url.Values{"search": {filter}, "source": {filter[:64]}, "after": {after}, "limit": {"50"}}
			payload, err := inventoryQuery(httptest.NewRequest("GET", "/?"+query.Encode(), nil))
			if err != nil || len(payload) > 1024 {
				t.Fatalf("accepted query does not fit IPC: %d %v", len(payload), err)
			}
			var wire struct {
				SearchBase64 *string `json:"search_b64"`
				SourceBase64 *string `json:"source_b64"`
				AfterBase64  *string `json:"after_b64"`
				After        string  `json:"after"`
			}
			if err := json.Unmarshal([]byte(payload), &wire); err != nil {
				t.Fatal(err)
			}
			decoded, err := inventory.DecodeQueryFilter("", wire.SearchBase64, 128)
			if err != nil || decoded != filter {
				t.Fatalf("filter changed: %q %v", decoded, err)
			}
			decodedSource, err := inventory.DecodeQueryFilter("", wire.SourceBase64, 64)
			if err != nil || decodedSource != filter[:64] {
				t.Fatal("source filter changed")
			}
			decodedAfter := wire.After
			if wire.AfterBase64 != nil {
				decodedAfter, err = inventory.DecodeQueryFilter("", wire.AfterBase64, 256)
			}
			if err != nil || decodedAfter != after {
				t.Fatal("cursor changed")
			}
			secret := []byte("01234567890123456789012345678901")
			request, err := ipc.NewRequest(secret, "inventory_software", payload)
			if err != nil {
				t.Fatal(err)
			}
			if err := ipc.Verify(secret, request, time.Now()); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, after := range []string{"!rs1:widget", "!rs1:" + strings.Repeat("x", 251)} {
		if _, err := inventoryQuery(httptest.NewRequest("GET", "/?"+url.Values{"after": {after}}.Encode(), nil)); err != nil {
			t.Fatal(err)
		}
	}
}
