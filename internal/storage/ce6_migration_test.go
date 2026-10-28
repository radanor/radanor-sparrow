package storage

import (
	"context"
	"database/sql"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/radanor/radanor-shield/internal/inventory"
)

// createV3Database builds the CE-5 schema exactly as version 3 shipped it, with
// monitoring evidence and posture history already stored. It pins the historical
// schema so the CE-6 upgrade is tested against a real older database rather than
// against one this build creates.
func createV3Database(t *testing.T, path string) {
	t.Helper()
	createV1Database(t, path)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	statements := []string{
		// v2: the CE-5 assessment tables.
		`CREATE TABLE assessment_runs(id INTEGER PRIMARY KEY, trigger TEXT NOT NULL, status TEXT NOT NULL, started_ns INTEGER NOT NULL, finished_ns INTEGER NOT NULL, duration_ms INTEGER NOT NULL, checks_total INTEGER NOT NULL, passed INTEGER NOT NULL, failed INTEGER NOT NULL, unknown INTEGER NOT NULL, not_supported INTEGER NOT NULL, stale INTEGER NOT NULL, collectors_run INTEGER NOT NULL, collectors_failed INTEGER NOT NULL, new_findings INTEGER NOT NULL, open_findings INTEGER NOT NULL)`,
		`CREATE INDEX runs_started_idx ON assessment_runs(started_ns DESC, id DESC)`,
		`CREATE TABLE observations(id INTEGER PRIMARY KEY, run_id INTEGER NOT NULL REFERENCES assessment_runs(id) ON DELETE CASCADE, collector TEXT NOT NULL, platform TEXT NOT NULL, collected_ns INTEGER NOT NULL, status TEXT NOT NULL, source TEXT NOT NULL, values_json BLOB NOT NULL CHECK(length(values_json)<=8192), error TEXT NOT NULL, UNIQUE(run_id, collector))`,
		`CREATE INDEX observations_run_idx ON observations(run_id)`,
		`CREATE TABLE check_results(id INTEGER PRIMARY KEY, run_id INTEGER NOT NULL REFERENCES assessment_runs(id) ON DELETE CASCADE, check_id TEXT NOT NULL, check_version TEXT NOT NULL, category TEXT NOT NULL, title TEXT NOT NULL, platform TEXT NOT NULL, result TEXT NOT NULL, severity TEXT NOT NULL, detail TEXT NOT NULL, explanation TEXT NOT NULL, action TEXT NOT NULL, verify_method TEXT NOT NULL, limitations_json BLOB NOT NULL, collected_ns INTEGER NOT NULL, evaluated_ns INTEGER NOT NULL, fresh INTEGER NOT NULL, evidence_json BLOB NOT NULL CHECK(length(evidence_json)<=8192), UNIQUE(run_id, check_id))`,
		`CREATE INDEX check_results_run_idx ON check_results(run_id)`,
		`CREATE INDEX check_results_finding_idx ON check_results(check_id, result)`,
		`CREATE TABLE findings(id INTEGER PRIMARY KEY, fingerprint TEXT NOT NULL UNIQUE, check_id TEXT NOT NULL, check_version TEXT NOT NULL, platform TEXT NOT NULL, severity TEXT NOT NULL, lifecycle TEXT NOT NULL, title TEXT NOT NULL, summary TEXT NOT NULL, action TEXT NOT NULL, verify_method TEXT NOT NULL, limitations_json BLOB NOT NULL, evidence_json BLOB NOT NULL CHECK(length(evidence_json)<=8192), first_seen_ns INTEGER NOT NULL, last_seen_ns INTEGER NOT NULL, resolved_ns INTEGER, reopened_ns INTEGER, run_id INTEGER NOT NULL)`,
		`CREATE INDEX findings_lifecycle_idx ON findings(lifecycle, severity)`,
		`CREATE INDEX findings_last_seen_idx ON findings(last_seen_ns DESC)`,
		`CREATE TABLE finding_history(id INTEGER PRIMARY KEY, fingerprint TEXT NOT NULL, run_id INTEGER NOT NULL, lifecycle TEXT NOT NULL, result TEXT NOT NULL, detail TEXT NOT NULL, recorded_ns INTEGER NOT NULL)`,
		`CREATE INDEX finding_history_fingerprint_idx ON finding_history(fingerprint, recorded_ns DESC)`,
		`PRAGMA user_version=2`,
		// v3: the independent storage meters.
		`CREATE TABLE storage_usage(scope TEXT PRIMARY KEY, bytes INTEGER NOT NULL CHECK(bytes>=0))`,
		`INSERT INTO storage_usage(scope,bytes) VALUES('monitoring',0)`,
		`INSERT INTO storage_usage(scope,bytes) VALUES('assessment',12345)`,
		`PRAGMA user_version=3`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("v3 setup %q: %v", statement, err)
		}
	}
	now := time.Now().UnixNano()
	if _, err := db.Exec(`INSERT INTO assessment_runs(trigger,status,started_ns,finished_ns,duration_ms,checks_total,passed,failed,unknown,not_supported,stale,collectors_run,collectors_failed,new_findings,open_findings) VALUES('manual','completed',?,?,10,3,1,1,1,0,0,3,0,1,1)`, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO observations(run_id,collector,platform,collected_ns,status,source,values_json,error) VALUES(1,'platform','ubuntu',?,'collected','uname','[]','')`, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO check_results(run_id,check_id,check_version,category,title,platform,result,severity,detail,explanation,action,verify_method,limitations_json,collected_ns,evaluated_ns,fresh,evidence_json) VALUES(1,'ssh.password_auth','1.0.0','remote','SSH password authentication','ubuntu','fail','high','PasswordAuthentication yes','Passwords can be guessed.','Set PasswordAuthentication no and reload the daemon.','Re-run the check.','[]',?,?,1,'{}')`, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO findings(fingerprint,check_id,check_version,platform,severity,lifecycle,title,summary,action,verify_method,limitations_json,evidence_json,first_seen_ns,last_seen_ns,run_id) VALUES('abc','ssh.password_auth','1.0.0','ubuntu','high','new','SSH password authentication','password auth enabled','disable it','re-run','[]','{}',?,?,1)`, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO finding_history(fingerprint,run_id,lifecycle,result,detail,recorded_ns) VALUES('abc',1,'new','fail','first seen',?)`, now); err != nil {
		t.Fatal(err)
	}
}

func TestMigrateFromV3PreservesDataAndAddsCe6Storage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ce5.db")
	createV3Database(t, path)

	s, err := Open(path, testLimits(nil))
	if err != nil {
		t.Fatalf("open after v3: %v", err)
	}
	defer s.Close()
	ctx := context.Background()

	var version int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != currentSchemaVersion {
		t.Fatalf("schema version = %d, want %d", version, currentSchemaVersion)
	}

	// CE-4 monitoring evidence and CE-5 posture history are untouched.
	if n := countRows(t, s, "SELECT count(*) FROM events"); n != 1 {
		t.Fatalf("events = %d, want 1", n)
	}
	if n := countRows(t, s, "SELECT count(*) FROM checkpoints"); n != 1 {
		t.Fatalf("checkpoints = %d, want 1", n)
	}
	if n := countRows(t, s, "SELECT count(*) FROM monitoring_gaps"); n != 1 {
		t.Fatalf("monitoring gaps = %d, want 1", n)
	}
	if n := countRows(t, s, "SELECT count(*) FROM assessment_runs"); n != 1 {
		t.Fatalf("assessment runs = %d, want 1", n)
	}
	if n := countRows(t, s, "SELECT count(*) FROM check_results"); n != 1 {
		t.Fatalf("check results = %d, want 1", n)
	}
	if n := countRows(t, s, "SELECT count(*) FROM findings"); n != 1 {
		t.Fatalf("findings = %d, want 1", n)
	}
	if n := countRows(t, s, "SELECT count(*) FROM finding_history"); n != 1 {
		t.Fatalf("finding history = %d, want 1", n)
	}

	// The new tables exist and start empty.
	if n := countRows(t, s, "SELECT count(*) FROM inventory_items"); n != 0 {
		t.Fatalf("inventory items = %d, want 0", n)
	}
	if n := countRows(t, s, "SELECT count(*) FROM inventory_snapshots"); n != 0 {
		t.Fatalf("inventory snapshots = %d, want 0", n)
	}
	if n := countRows(t, s, "SELECT count(*) FROM settings"); n != 0 {
		t.Fatalf("settings = %d, want 0", n)
	}

	// The assessment figure is measured from the tables, so the deliberately
	// wrong value the v3 database carried (12345) must not be reported.
	usage, err := s.StorageUsage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if usage[usageAssessment] == 12345 {
		t.Fatal("the stale stored assessment meter was reported instead of a measurement")
	}
	if usage[usageAssessment] <= 0 {
		t.Fatalf("assessment bytes = %d, want the measured history size", usage[usageAssessment])
	}
	if _, present := usage[usageInventory]; !present {
		t.Fatalf("the inventory scope is missing after the upgrade: %v", usage)
	}

	// The upgraded database is fully usable: settings persist and the inventory
	// can be replaced without disturbing the preserved evidence.
	if err := s.PutSettings(ctx, map[string]string{"theme": "dark"}); err != nil {
		t.Fatalf("settings after upgrade: %v", err)
	}
	snap := testInventorySnapshot(time.Now(), inventoryPart(inventory.KindSoftware, []inventory.Item{softwareItem("bash", "5.2")}))
	if err := s.ReplaceInventory(ctx, snap); err != nil {
		t.Fatalf("inventory after upgrade: %v", err)
	}
	if n := countRows(t, s, "SELECT count(*) FROM events"); n != 1 {
		t.Fatalf("events = %d after CE-6 writes, want 1", n)
	}
	if err := s.ReplaceInventory(ctx, snap); err != nil {
		t.Fatalf("second inventory replace: %v", err)
	}
	if n := countRows(t, s, "SELECT count(*) FROM inventory_items"); n != 1 {
		t.Fatalf("inventory items = %d, want 1", n)
	}
}

// TestMigrateIsIdempotentAndRefusesANewerSchema pins the two boundary
// behaviours: reopening an already-upgraded database changes nothing, and a
// database written by a newer build is refused rather than downgraded.
func TestMigrateIsIdempotentAndRefusesANewerSchema(t *testing.T) {
	s, path := openStore(t, nil)
	ctx := context.Background()
	if err := s.PutSettings(ctx, map[string]string{"theme": "light"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(path, testLimits(nil))
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	settings, err := reopened.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if settings["theme"] != "light" {
		t.Fatalf("settings lost across reopen: %v", settings)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("PRAGMA user_version=" + strconv.Itoa(currentSchemaVersion+1)); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, err := Open(path, testLimits(nil)); err == nil {
		t.Fatal("a database from a newer build must be refused")
	}
}
