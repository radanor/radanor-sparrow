package storage

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/radanor/radanor-shield/internal/inventory"
)

// Persistence for the CE-6 locally observed software/network inventory.
//
// Inventory is a current-state snapshot, not history: ReplaceInventory swaps the
// whole snapshot in one transaction. It is bounded three ways — per-kind item
// caps in the inventory package, the store's MaxInventoryItems, and the
// independent inventory byte budget — and it charges the inventory scope only,
// so it can never consume the room reserved for monitoring events or posture
// assessment history.

// ErrInventoryPressure reports that a snapshot cannot be stored inside the
// inventory byte budget even after pruning. It is inventory-scoped: monitoring
// ingestion and assessment history are unaffected while it holds.
var ErrInventoryPressure = errors.New("inventory storage limit reached")

// inventoryBytesExpr measures inventory-owned payload plus a per-row reserve for
// the fixed-width columns and index entries, deliberately over-counting so the
// budget cannot be exceeded silently.
var inventoryBytesExpr = sumTextExpr("inventory_items", assessmentRowReserveBytes, "kind", "name", "version", "source", "platform", "fields_json")

// ItemRecord is one stored inventory item.
type ItemRecord struct {
	Kind       inventory.Kind    `json:"kind"`
	Name       string            `json:"name"`
	Version    string            `json:"version,omitempty"`
	Source     string            `json:"source"`
	Platform   string            `json:"platform"`
	Fields     map[string]string `json:"fields,omitempty"`
	ObservedAt time.Time         `json:"observed_at"`
}

// SnapshotRecord describes the coverage of one stored kind.
type SnapshotRecord struct {
	Kind       inventory.Kind `json:"kind"`
	ObservedAt time.Time      `json:"observed_at"`
	Total      int            `json:"total"`
	Stored     int            `json:"stored"`
	Truncated  bool           `json:"truncated"`
	Source     string         `json:"source"`
	Note       string         `json:"note,omitempty"`
}

// InventoryStatus is the honest coverage summary for the Software and Network &
// Services pages.
type InventoryStatus struct {
	HasSnapshot   bool             `json:"has_snapshot"`
	Snapshots     []SnapshotRecord `json:"snapshots"`
	StoredBytes   int64            `json:"stored_bytes"`
	MaxBytes      int64            `json:"max_bytes"`
	MaxItems      int              `json:"max_items_per_kind"`
	Pressure      bool             `json:"storage_pressure"`
	GeneratedAt   time.Time        `json:"generated_at"`
	Limitations   []string         `json:"limitations"`
	SoftwareCount int              `json:"software_count"`
	EndpointCount int              `json:"endpoint_count"`
	ServiceCount  int              `json:"service_count"`
}

// ReplaceInventory atomically replaces the stored snapshot for the given kinds.
// Every kind present in items is replaced; kinds absent from the call keep their
// previous snapshot so a partial refresh never blanks a page it did not cover.
func (s *Store) ReplaceInventory(ctx context.Context, snap inventory.Snapshot) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	byKind := map[inventory.Kind][]inventory.Item{}
	for _, item := range snap.Items {
		byKind[item.Kind] = append(byKind[item.Kind], item)
	}
	kinds := make([]inventory.Kind, 0, len(snap.Total))
	for kind := range snap.Total {
		kinds = append(kinds, kind)
	}
	// Deterministic order so a failure mid-transaction is reproducible.
	sortKinds(kinds)
	for _, kind := range kinds {
		if _, err := tx.ExecContext(ctx, `DELETE FROM inventory_items WHERE kind=?`, string(kind)); err != nil {
			return err
		}
		// Enforce the configured per-kind cap here as well as in the inventory
		// package, so the limit the settings page reports is the limit actually
		// applied. Cutting a kind down marks the snapshot as truncated.
		kindItems := byKind[kind]
		capped := false
		if len(kindItems) > s.limits.MaxInventoryItems {
			kindItems = kindItems[:s.limits.MaxInventoryItems]
			capped = true
		}
		stmt, err := tx.PrepareContext(ctx, `INSERT OR REPLACE INTO inventory_items(kind,name,version,source,platform,fields_json,observed_ns) VALUES(?,?,?,?,?,?,?)`)
		if err != nil {
			return err
		}
		for _, item := range kindItems {
			encoded, err := json.Marshal(item.Fields)
			if err != nil || len(encoded) > 4096 {
				return errors.New("inventory item fields exceed the size limit")
			}
			if _, err := stmt.ExecContext(ctx, string(kind), item.Name, item.Version, item.Source, "", encoded, snap.ObservedAt); err != nil {
				stmt.Close()
				return err
			}
		}
		stmt.Close()
		total := snap.Total[kind]
		// The stored count is read back from the table rather than assumed to be
		// the number of items supplied: two observations that share a name,
		// version and source are one row (the primary key), which really does
		// happen — the socket collector can see the same endpoint more than once.
		// Reporting the supplied count would claim entries the store does not hold.
		var stored int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM inventory_items WHERE kind=?`, string(kind)).Scan(&stored); err != nil {
			return err
		}
		// Three independent reasons a kind is not a complete reading, and each is
		// recorded on its own: the collector's own output was clipped (so even
		// observed==stored is partial), the per-kind cap was applied, or stored
		// rows were fewer than the observations supplied. Reporting only the last
		// would let a clipped reading whose partial count equals its stored count
		// claim completeness.
		truncated := 0
		if total > stored || capped || snap.TruncatedKinds[kind] {
			truncated = 1
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO inventory_snapshots(kind,observed_ns,total,stored,source,note,truncated) VALUES(?,?,?,?,?,?,?) ON CONFLICT(kind) DO UPDATE SET observed_ns=excluded.observed_ns,total=excluded.total,stored=excluded.stored,source=excluded.source,note=excluded.note,truncated=excluded.truncated`,
			string(kind), snap.ObservedAt, total, stored, snap.Sources[kind], snap.Notes[kind], truncated); err != nil {
			return err
		}
	}
	// Recompute the inventory meter from the data itself, so it can never drift.
	if _, err := tx.ExecContext(ctx, `UPDATE storage_usage SET bytes=(`+inventoryBytesExpr+`) WHERE scope=?`, usageInventory); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO storage_usage(scope,bytes) SELECT ?, (`+inventoryBytesExpr+`) WHERE NOT EXISTS (SELECT 1 FROM storage_usage WHERE scope=?)`, usageInventory, usageInventory); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	// Prune to the inventory budget after commit; the snapshot is already
	// consistent, and pruning only ever removes inventory rows.
	return s.pruneInventory(ctx)
}

// pruneInventory bounds the stored snapshot by the inventory byte budget,
// removing the largest-payload rows first until it fits. It never touches
// monitoring or assessment tables, and it is best-effort by design: a database
// that cannot be pruned stays over budget and reports pressure rather than
// blocking monitoring.
//
// Pruning is the fourth way a kind becomes incomplete, and it is recorded as
// such: every kind the prune touched is marked truncated even when the budget was
// never met, because records the store no longer holds must not keep being
// counted as available.
func (s *Store) pruneInventory(ctx context.Context) error {
	const batch = 500
	removedAny := false
	for pass := 0; pass < 200; pass++ {
		used, err := s.inventoryBytes(ctx)
		if err != nil {
			return err
		}
		if used <= s.limits.MaxInventoryBytes {
			break
		}
		res, err := s.db.ExecContext(ctx, `DELETE FROM inventory_items WHERE rowid IN (SELECT rowid FROM inventory_items ORDER BY length(fields_json)+length(name)+length(version) DESC LIMIT ?)`, batch)
		if err != nil {
			return err
		}
		removed, aerr := res.RowsAffected()
		if aerr != nil || removed == 0 {
			// Nothing left to remove: the budget cannot be met.
			break
		}
		removedAny = true
	}
	if removedAny {
		// Keep the recorded coverage honest: a pruned kind must not keep
		// claiming items the store no longer holds. The mark of truncation is
		// set, never cleared, so a kind that was cut down stays disclosed, and
		// reading stored back from the table covers the case where a prune
		// removed every row of a kind: it then reports zero stored against a
		// non-zero observed count rather than an empty, complete inventory.
		if _, err := s.db.ExecContext(ctx, `UPDATE inventory_snapshots SET stored=(SELECT count(*) FROM inventory_items WHERE inventory_items.kind=inventory_snapshots.kind), truncated=CASE WHEN total > (SELECT count(*) FROM inventory_items WHERE inventory_items.kind=inventory_snapshots.kind) THEN 1 ELSE truncated END`); err != nil {
			return err
		}
	}
	// A prune that could not bring the inventory inside its budget is reported
	// as pressure; it is not allowed to masquerade as a complete reading.
	used, err := s.inventoryBytes(ctx)
	if err != nil {
		return err
	}
	if used > s.limits.MaxInventoryBytes {
		return ErrInventoryPressure
	}
	return nil
}

func (s *Store) inventoryBytes(ctx context.Context) (int64, error) {
	var n int64
	if err := s.db.QueryRowContext(ctx, `SELECT `+inventoryBytesExpr).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// InventoryQuery is a bounded inventory read.
type InventoryQuery struct {
	Kind   inventory.Kind
	Search string
	Source string
	// After is a stable name/version/source cursor; legacy name cursors remain valid.
	After string
	Limit int
}

// InventoryItems returns one bounded page of stored inventory items plus the
// total number matching the filter. Search is a case-insensitive prefix match,
// which the (kind,name) index can serve; it is never an unbounded scan and the
// pattern is escaped so a caller cannot widen it with SQL wildcards.
func (s *Store) InventoryItems(ctx context.Context, q InventoryQuery) ([]ItemRecord, int, error) {
	if !q.Kind.Known() {
		return nil, 0, errors.New("invalid inventory kind")
	}
	limit := q.Limit
	if limit < 0 {
		return nil, 0, errors.New("invalid page limit")
	}
	if limit == 0 {
		limit = 25
	}
	if limit > s.limits.MaxPageSize {
		return nil, 0, errors.New("page too large")
	}
	if len(q.Search) > 128 || len(q.Source) > inventory.MaxSourceLen {
		return nil, 0, errors.New("invalid inventory query")
	}
	cursor, err := inventory.DecodeCursor(q.After)
	if err != nil {
		return nil, 0, err
	}
	where := `kind=?`
	args := []any{string(q.Kind)}
	if q.Source != "" {
		where += ` AND source=?`
		args = append(args, q.Source)
	}
	if q.Search != "" {
		where += ` AND name LIKE ? ESCAPE '\'`
		args = append(args, escapeLike(strings.ToLower(q.Search))+"%")
	}
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM inventory_items WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	pageWhere := where
	pageArgs := append([]any{}, args...)
	if cursor.Composite {
		pageWhere += ` AND (name,version,source) > (?,?,?)`
		pageArgs = append(pageArgs, cursor.Name, cursor.Version, cursor.Source)
	} else {
		pageWhere += ` AND name > ?`
		pageArgs = append(pageArgs, cursor.Name)
	}
	pageArgs = append(pageArgs, limit)
	rows, err := s.db.QueryContext(ctx, `SELECT kind,name,version,source,platform,fields_json,observed_ns FROM inventory_items WHERE `+pageWhere+` ORDER BY name ASC,version ASC,source ASC LIMIT ?`, pageArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []ItemRecord
	for rows.Next() {
		var r ItemRecord
		var kind string
		var fields []byte
		var observed int64
		if err := rows.Scan(&kind, &r.Name, &r.Version, &r.Source, &r.Platform, &fields, &observed); err != nil {
			return nil, 0, err
		}
		r.Kind = inventory.Kind(kind)
		r.ObservedAt = time.Unix(0, observed)
		if len(fields) > 0 {
			_ = json.Unmarshal(fields, &r.Fields)
		}
		out = append(out, r)
	}
	return out, total, rows.Err()
}

// InventorySources lists the distinct sources recorded for a kind, so the UI can
// offer a truthful filter without inventing one.
func (s *Store) InventorySources(ctx context.Context, kind inventory.Kind) ([]string, error) {
	if !kind.Known() {
		return nil, errors.New("invalid inventory kind")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT source FROM inventory_items WHERE kind=? ORDER BY source LIMIT 64`, string(kind))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var source string
		if err := rows.Scan(&source); err != nil {
			return nil, err
		}
		out = append(out, source)
	}
	return out, rows.Err()
}

// InventoryStatus reports the coverage and storage state of the stored snapshot.
func (s *Store) InventoryStatus(ctx context.Context) (InventoryStatus, error) {
	status := InventoryStatus{
		GeneratedAt: time.Now(),
		MaxBytes:    s.limits.MaxInventoryBytes,
		MaxItems:    s.limits.MaxInventoryItems,
		Snapshots:   []SnapshotRecord{},
		Limitations: []string{
			"the software inventory lists installed packages and applications as reported by the platform; it is not a catalogue of every file on the system",
			"no vulnerability matching is performed: a package version is never labelled vulnerable without authoritative advisory evidence",
			"a listening endpoint is a local observation, not a statement that the port is reachable from another machine",
			"a service or endpoint whose owning process could not be read is reported as unattributed rather than guessed",
		},
	}
	rows, err := s.db.QueryContext(ctx, `SELECT kind,observed_ns,total,stored,source,note,truncated FROM inventory_snapshots ORDER BY kind`)
	if err != nil {
		return status, err
	}
	defer rows.Close()
	for rows.Next() {
		var r SnapshotRecord
		var kind string
		var observed int64
		var truncated int
		if err := rows.Scan(&kind, &observed, &r.Total, &r.Stored, &r.Source, &r.Note, &truncated); err != nil {
			return status, err
		}
		r.Kind = inventory.Kind(kind)
		r.ObservedAt = time.Unix(0, observed)
		r.Truncated = truncated == 1
		status.HasSnapshot = true
		status.Snapshots = append(status.Snapshots, r)
		switch r.Kind {
		case inventory.KindSoftware:
			status.SoftwareCount = r.Stored
		case inventory.KindEndpoint:
			status.EndpointCount = r.Stored
		case inventory.KindService:
			status.ServiceCount = r.Stored
		}
	}
	if err := rows.Err(); err != nil {
		return status, err
	}
	used, err := s.inventoryBytes(ctx)
	if err != nil {
		return status, err
	}
	status.StoredBytes = used
	status.Pressure = used >= s.limits.MaxInventoryBytes
	// Retention can bring the inventory back inside its byte budget by deleting
	// records, including every record of a kind. A kind left with nothing stored
	// against a non-zero observed count is reported as pressure with an explicit
	// limitation, so "retention removed this" is never presented as "there was
	// nothing to report".
	for _, r := range status.Snapshots {
		if r.Stored == 0 && r.Total > 0 {
			status.Pressure = true
			status.Limitations = append(status.Limitations, string(r.Kind)+": retention removed every stored record of this kind, so nothing is shown for it even though records were observed")
		}
	}
	return status, nil
}

// escapeLike escapes the SQL LIKE metacharacters so a caller-supplied search term
// is matched literally and cannot widen the pattern.
func escapeLike(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return replacer.Replace(value)
}

func sortKinds(kinds []inventory.Kind) {
	sort.Slice(kinds, func(i, j int) bool { return kinds[i] < kinds[j] })
}
