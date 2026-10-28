package storage

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// CE-6 preference persistence tests. The store enforces only structural bounds;
// which keys exist and what they may contain is owned by the core API and
// validated there. These tests pin the store's half of that contract: updates
// are transactional, bounded, and never partially applied.

func TestPutSettingsAppliesAndReadsBack(t *testing.T) {
	s, _ := openStore(t, nil)
	ctx := context.Background()

	if err := s.PutSettings(ctx, map[string]string{"theme": "dark"}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	// A second batch updates an existing key and adds a new one in one call.
	if err := s.PutSettings(ctx, map[string]string{"theme": "light", "assess_enabled": "true"}); err != nil {
		t.Fatalf("update: %v", err)
	}
	settings, err := s.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if settings["theme"] != "light" || settings["assess_enabled"] != "true" {
		t.Fatalf("settings = %v, want theme=light assess_enabled=true", settings)
	}
	if len(settings) != 2 {
		t.Fatalf("stored %d settings, want 2", len(settings))
	}
}

func TestPutSettingsIsAtomic(t *testing.T) {
	s, _ := openStore(t, nil)
	ctx := context.Background()
	if err := s.PutSettings(ctx, map[string]string{"theme": "dark"}); err != nil {
		t.Fatal(err)
	}

	// One value in the batch is too long, so nothing from the batch may land —
	// not even the valid keys that were listed alongside it.
	err := s.PutSettings(ctx, map[string]string{"assess_enabled": "true", "assess_interval": strings.Repeat("x", MaxSettingValueLen+1)})
	if !errors.Is(err, ErrInvalidSetting) {
		t.Fatalf("oversized value: err = %v, want ErrInvalidSetting", err)
	}
	settings, err := s.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, present := settings["assess_enabled"]; present {
		t.Fatalf("a rejected batch was partially applied: %v", settings)
	}
	if settings["theme"] != "dark" {
		t.Fatalf("the existing preference changed: %v", settings)
	}
}

func TestSettingsBounds(t *testing.T) {
	s, _ := openStore(t, nil)
	ctx := context.Background()

	cases := []struct {
		name    string
		updates map[string]string
	}{
		{"empty key", map[string]string{"": "value"}},
		{"oversized key", map[string]string{strings.Repeat("k", MaxSettingKeyLen+1): "value"}},
		{"oversized value", map[string]string{"theme": strings.Repeat("v", MaxSettingValueLen+1)}},
	}
	for _, tc := range cases {
		if err := s.PutSettings(ctx, tc.updates); !errors.Is(err, ErrInvalidSetting) {
			t.Fatalf("%s: err = %v, want ErrInvalidSetting", tc.name, err)
		}
	}

	// The key-count ceiling is enforced as well, so the table cannot be used as
	// an unbounded store.
	bulk := map[string]string{}
	for i := 0; i <= MaxSettings; i++ {
		bulk["k"+strings.Repeat("a", i%8)+string(rune('a'+i%26))+string(rune('a'+(i/26)%26))] = "v"
	}
	if err := s.PutSettings(ctx, bulk); !errors.Is(err, ErrInvalidSetting) {
		t.Fatalf("bulk: err = %v, want ErrInvalidSetting", err)
	}
	if settings, err := s.Settings(ctx); err != nil || len(settings) != 0 {
		t.Fatalf("settings = %v (err %v), want none stored", settings, err)
	}
}

func TestPutSettingsWithNoChangesIsANoop(t *testing.T) {
	s, _ := openStore(t, nil)
	if err := s.PutSettings(context.Background(), nil); err != nil {
		t.Fatalf("empty batch: %v", err)
	}
	if n := countRows(t, s, "SELECT count(*) FROM settings"); n != 0 {
		t.Fatalf("settings rows = %d, want 0", n)
	}
}

// TestStorageUsageMeasuresAssessmentLive is the CE-6 storage hardening check for
// the reported meters. The assessment scope changes through many write paths, so
// its figure is measured from the assessment tables rather than read from a
// stored meter. A stale stored value must not be reported.
func TestStorageUsageMeasuresAssessmentLive(t *testing.T) {
	s, _ := openStore(t, nil)
	ctx := context.Background()

	// Plant a deliberately wrong stored meter, as an older database could hold.
	if _, err := s.db.ExecContext(ctx, `UPDATE storage_usage SET bytes=1 WHERE scope=?`, usageAssessment); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE storage_usage SET bytes=999999 WHERE scope=?`, usageInventory); err != nil {
		t.Fatal(err)
	}
	usage, err := s.StorageUsage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if usage[usageAssessment] != 0 {
		t.Fatalf("assessment bytes = %d, want the measured 0 rather than the stale stored 1", usage[usageAssessment])
	}
	if usage[usageInventory] != 0 {
		t.Fatalf("inventory bytes = %d, want the measured 0 rather than the stale stored value", usage[usageInventory])
	}
	if _, present := usage[usageMonitoring]; !present && usage[usageMonitoring] != 0 {
		t.Fatalf("monitoring scope missing from usage: %v", usage)
	}
}
