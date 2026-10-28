package storage

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/radanor/radanor-shield/internal/monitoring"
)

// TestSparrowOpensAnExistingShieldProfileWithoutLosingHistory pins the data
// compatibility claim in the private engineering record: a profile written
// by a pre-rebrand Radanor Shield installation is opened in place by the renamed
// product, and nothing in it is reset, overwritten or dropped.
//
// The fixture is createV3Database, which writes the CE-5 schema and its rows
// with literal SQL instead of through this build's own writers, so it stands in
// for a database this build did not create. The row counts asserted below are
// every category the user can see: file-activity evidence, its checkpoints and
// gaps, and the assessment runs, checks, findings and finding history.
func TestSparrowOpensAnExistingShieldProfileWithoutLosingHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "monitoring.db")
	createV3Database(t, path)

	// The persisted event contract is not part of the rebrand. If this changed,
	// every stored row would be reinterpreted under a new schema version.
	if monitoring.EventSchemaV1 != "shield.event/v1" {
		t.Fatalf("event schema version = %q; existing rows would no longer describe themselves", monitoring.EventSchemaV1)
	}

	store, err := Open(path, testLimits(nil))
	if err != nil {
		t.Fatalf("opening an existing profile failed: %v", err)
	}
	defer store.Close()
	ctx := context.Background()

	for table, want := range map[string]int{
		"events":              1,
		"checkpoints":         1,
		"monitoring_gaps":     1,
		"assessment_runs":     1,
		"observations":        1,
		"check_results":       1,
		"findings":            1,
		"finding_history":     1,
		"settings":            0,
		"inventory_items":     0,
		"inventory_snapshots": 0,
	} {
		if got := countRows(t, store, "SELECT count(*) FROM "+table); got != want {
			t.Errorf("%s = %d, want %d: the upgrade did not preserve stored history", table, got, want)
		}
	}

	// The storage meters are byte counters, so a preserved row count is not
	// enough: the recorded values themselves must survive.
	meters := map[string]int64{}
	rows, err := store.db.QueryContext(ctx, "SELECT scope,bytes FROM storage_usage")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var scope string
		var bytes int64
		if err := rows.Scan(&scope, &bytes); err != nil {
			t.Fatal(err)
		}
		meters[scope] = bytes
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if meters["assessment"] != 12345 {
		t.Errorf("assessment storage meter = %d, want the stored 12345", meters["assessment"])
	}
	if _, ok := meters["monitoring"]; !ok {
		t.Error("the monitoring storage meter was dropped")
	}

	// Monitoring evidence is readable through the product's own reader, not just
	// through SQL, so a schema the product can no longer decode cannot pass.
	events, err := store.Recent(ctx, Query{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("Recent returned %d events, want the 1 stored before the rebrand", len(events))
	}
	if events[0].SchemaVersion != monitoring.EventSchemaV1 {
		t.Errorf("stored event schema version = %q, want %q", events[0].SchemaVersion, monitoring.EventSchemaV1)
	}
	if events[0].Source == "" || events[0].EventID == "" {
		t.Errorf("stored event lost its identity: source=%q event_id=%q", events[0].Source, events[0].EventID)
	}
	gaps, err := store.Gaps(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(gaps) != 1 || gaps[0].Reason == "" {
		t.Errorf("gaps = %+v, want the 1 recorded gap", gaps)
	}

	// The schema version is advanced by the normal migration path, which is the
	// same code that ran before the rebrand; the rebrand itself adds no migration.
	var version int
	if err := store.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != currentSchemaVersion {
		t.Errorf("schema version = %d, want %d", version, currentSchemaVersion)
	}
}

// TestOnlyOneDatabaseFileExistsInTheProfile guards the second half of the
// promise: opening an existing profile must not create a second, empty
// database beside it, which is what a path rename would have done.
func TestOpeningAProfileCreatesNoSecondDatabase(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "monitoring.db")
	createV3Database(t, path)
	store, err := Open(path, testLimits(nil))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	entries, err := filepath.Glob(filepath.Join(dir, "*.db"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0] != path {
		t.Errorf("profile contains %v, want exactly %s", entries, path)
	}
}
