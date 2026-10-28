//go:build windows

package coreapp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/radanor/radanor-shield/internal/auth"
	"github.com/radanor/radanor-shield/internal/gateway"
	"github.com/radanor/radanor-shield/internal/inventory"
	"github.com/radanor/radanor-shield/internal/ipc"
	"github.com/radanor/radanor-shield/internal/storage"
)

// Fixture-derived collection input, not a claim of native uninstall-registry
// evidence. All later boundaries (SQLite reopen, IPC, auth and HTTP) are real.
func unicodeFixtureAPI(t *testing.T) (*inventoryAPI, map[string]inventory.Item) {
	t.Helper()
	names := []string{"हिन्दी", "Café", "日本語", "Supplementary 😀 𠀋 𝄞", "Cafe\u0301", strings.Repeat("a", 252) + "𠀋", "Café"}
	var items []inventory.Item
	expected := map[string]inventory.Item{}
	for i, name := range names {
		// The same UTF-16 decoder used by x/sys/windows/registry.GetStringValue.
		decoded := syscall.UTF16ToString(append(utf16.Encode([]rune(name)), 0))
		if decoded != name || !utf8.ValidString(decoded) {
			t.Fatalf("UTF-16 fixture corrupted: %q", decoded)
		}
		version := "版本१é😀"
		if i == len(names)-1 {
			version += "-2"
		}
		item := inventory.Item{Kind: inventory.KindSoftware, Name: decoded, Version: version, Source: "registry-uninstall", Fields: map[string]string{"publisher": "प्रकाशक Café 日本語 😀", "origin": "windows-application"}}
		items = append(items, item)
		expected[item.Name+"\x00"+item.Version] = item
	}
	encoded, err := json.Marshal(items)
	if err != nil {
		t.Fatal(err)
	}
	var decodedItems []inventory.Item
	if err := json.Unmarshal(encoded, &decodedItems); err != nil {
		t.Fatal(err)
	}
	collector := &stubCollector{kind: inventory.KindSoftware, source: "registry-uninstall", items: decodedItems}
	databasePath := filepath.Join(t.TempDir(), "inventory.db")
	store, err := storage.Open(databasePath, storage.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	api := &inventoryAPI{store: store, collectors: []inventory.Collector{collector}, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if err := api.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Reopen the real database to rule out an in-memory-only round trip.
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := storage.Open(databasePath, storage.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	api.store = reopened
	t.Cleanup(func() { _ = reopened.Close() })
	rows, total, err := reopened.InventoryItems(context.Background(), storage.InventoryQuery{Kind: inventory.KindSoftware, Limit: 50})
	if err != nil || total != len(expected) || len(rows) != len(expected) {
		t.Fatalf("SQLite round trip: rows=%d total=%d err=%v", len(rows), total, err)
	}
	for _, row := range rows {
		assertUnicodeInventoryItem(t, row, expected)
	}

	return api, expected
}

func TestWindowsUnicodeInventorySQLiteRoundTrip(t *testing.T) {
	_, expected := unicodeFixtureAPI(t)
	t.Logf("SIMULATED collection fixtures: %d Unicode identities preserved through UTF-16 decoding, JSON and SQLite reopen", len(expected))
}

func TestWindowsUnicodeInventoryAuthenticatedRoundTrip(t *testing.T) {
	// The production Windows endpoint has a fixed pipe name. Explicit opt-in
	// keeps default parallel-package CI from racing the internal/ipc tests.
	if os.Getenv("RADANOR_TEST_UNICODE_IPC") != "1" {
		t.Skip("set RADANOR_TEST_UNICODE_IPC=1 and use go test -p 1 for native authenticated IPC integration")
	}
	api, expected := unicodeFixtureAPI(t)
	dir := t.TempDir()
	secret := []byte("unicode-fixture-01234567890123456")
	tickets := auth.NewTicketStore()
	ctx, cancel := context.WithCancel(context.Background())
	core, err := ipc.Listen(dir, secret, func(req ipc.Request) ipc.Response {
		switch req.Operation {
		case "issue_ticket":
			ticket, err := tickets.Issue()
			if err != nil {
				return ipc.Response{Error: "ticket unavailable"}
			}
			return ipc.Response{OK: true, Value: ticket}
		case "redeem_ticket":
			return ipc.Response{OK: tickets.Redeem(req.Value)}
		default:
			return api.handle(req)
		}
	})
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); _ = core.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("IPC did not stop")
		}
	})
	server, err := gateway.New(dir, secret, api.logger)
	if err != nil {
		t.Fatal(err)
	}
	go server.Serve()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	})
	bare := &http.Client{Timeout: 5 * time.Second}
	response, err := bare.Get(server.Origin() + "/api/v1/inventory/software")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 401 {
		t.Fatalf("unauthenticated status=%d", response.StatusCode)
	}
	ticket, err := ipc.Call(context.Background(), dir, secret, "issue_ticket", "")
	if err != nil || !ticket.OK {
		t.Fatal("ticket issue failed")
	}
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 5 * time.Second}
	body, _ := json.Marshal(map[string]string{"ticket": ticket.Value})
	request, _ := http.NewRequest("POST", server.Origin()+"/api/v1/session/exchange", bytes.NewReader(body))
	request.Header.Set("Origin", server.Origin())
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal("ticket exchange failed")
	}
	seen := map[string]bool{}
	after := ""
	for pages := 0; pages < 10; pages++ {
		query := url.Values{"limit": {"2"}}
		if after != "" {
			query.Set("after", after)
		}
		response, err := client.Get(server.Origin() + "/api/v1/inventory/software?" + query.Encode())
		if err != nil {
			t.Fatal(err)
		}
		raw, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || response.StatusCode != 200 || !utf8.Valid(raw) {
			t.Fatalf("inventory HTTP response: status=%d err=%v", response.StatusCode, err)
		}
		var page struct {
			Items []storage.ItemRecord `json:"items"`
			Total int                  `json:"total"`
			Next  string               `json:"next_after"`
		}
		if err := json.Unmarshal(raw, &page); err != nil {
			t.Fatal(err)
		}
		if page.Total != len(expected) {
			t.Fatalf("API total=%d", page.Total)
		}
		for _, item := range page.Items {
			assertUnicodeInventoryItem(t, item, expected)
			key := item.Name + "\x00" + item.Version
			if seen[key] {
				t.Fatalf("duplicate Unicode row %q", key)
			}
			seen[key] = true
		}
		if page.Next == "" {
			if len(seen) != len(expected) {
				t.Fatalf("Unicode rows lost: %d/%d", len(seen), len(expected))
			}
			t.Logf("SIMULATED collection fixtures: %d rows persisted and retrieved across %d authenticated native HTTP pages", len(seen), pages+1)
			return
		}
		if page.Next == after {
			t.Fatal("cursor did not advance")
		}
		after = page.Next
	}
	t.Fatal("pagination did not terminate")
}

func assertUnicodeInventoryItem(t *testing.T, got storage.ItemRecord, expected map[string]inventory.Item) {
	t.Helper()
	want, ok := expected[got.Name+"\x00"+got.Version]
	if !ok || got.Source != want.Source || got.Fields["publisher"] != want.Fields["publisher"] || !utf8.ValidString(got.Name) {
		t.Fatalf("Unicode row changed: %+v", got)
	}
}
