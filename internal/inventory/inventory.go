// Package inventory holds the shared, platform-neutral model for the locally
// observed software and network/service inventories.
//
// An inventory item is an observation, never a security verdict. The package
// deliberately performs no vulnerability matching: matching a package version
// against an advisory database offline would misrepresent distributions that
// backport fixes under an unchanged upstream version.
package inventory

import (
	"context"
	"errors"
	"sort"
	"strings"
)

// Kind classifies one inventory item.
type Kind string

const (
	// KindSoftware is an installed package or application.
	KindSoftware Kind = "software"
	// KindEndpoint is a locally observed listening socket.
	KindEndpoint Kind = "endpoint"
	// KindService is a locally observed service/unit.
	KindService Kind = "service"
)

// Known reports whether k is one of the kinds the store accepts. The set is a
// closed allowlist so no caller can mint an unbounded kind space.
func (k Kind) Known() bool {
	switch k {
	case KindSoftware, KindEndpoint, KindService:
		return true
	}
	return false
}

// Bounds. Inventory is a *current-state* view rather than history, so growth is
// bounded by these caps in addition to the storage byte budget. A snapshot that
// would exceed a cap is truncated and marked as truncated rather than silently
// dropped.
const (
	// MaxNameLen bounds an item name.
	MaxNameLen = 256
	// MaxVersionLen bounds a version string.
	MaxVersionLen = 128
	// MaxSourceLen bounds a source label.
	MaxSourceLen = 64
	// MaxFieldLen bounds any single extra field value.
	MaxFieldLen = 256
	// MaxFieldCount bounds the number of extra fields per item.
	MaxFieldCount = 12
	// MaxKindItems bounds how many items of one kind a snapshot may hold.
	MaxKindItems = 20000
)

// ErrInvalidItem reports a malformed item that must never be stored.
var ErrInvalidItem = errors.New("invalid inventory item")

// Item is one observed inventory record.
type Item struct {
	Kind    Kind
	Name    string
	Version string
	Source  string
	// Fields carries kind-specific evidence such as address, port, transport,
	// state, process or attribution. Values are bounded and untyped strings so
	// the model stays platform-neutral.
	Fields map[string]string
}

// Normalize returns a bounded, canonical copy of the item, or ErrInvalidItem
// when the item is structurally unusable. Every persisted item passes through
// this so no collector output can inflate stored records without bound.
func (i Item) Normalize() (Item, error) {
	name := strings.TrimSpace(i.Name)
	if name == "" || len(name) > MaxNameLen {
		return Item{}, ErrInvalidItem
	}
	if !i.Kind.Known() {
		return Item{}, ErrInvalidItem
	}
	out := Item{
		Kind:    i.Kind,
		Name:    name,
		Version: bound(strings.TrimSpace(i.Version), MaxVersionLen),
		Source:  bound(strings.TrimSpace(i.Source), MaxSourceLen),
	}
	if len(i.Fields) > 0 {
		fields := make(map[string]string, len(i.Fields))
		keys := make([]string, 0, len(i.Fields))
		for k := range i.Fields {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if len(fields) >= MaxFieldCount {
				break
			}
			key := bound(strings.TrimSpace(k), 32)
			if key == "" {
				continue
			}
			fields[key] = bound(strings.TrimSpace(i.Fields[k]), MaxFieldLen)
		}
		out.Fields = fields
	}
	return out, nil
}

func bound(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return s[:limit]
}

// Snapshot is one atomic inventory replacement: the software, endpoint and
// service items observed together at one moment, plus the metadata the UI needs
// to explain coverage honestly.
type Snapshot struct {
	// ObservedAt is when the inventory was collected.
	ObservedAt int64 // unix nanos
	// Items holds every normalized item across all kinds.
	Items []Item
	// Total counts items observed before truncation, per kind.
	Total map[Kind]int
	// Stored counts items actually persisted, per kind.
	Stored map[Kind]int
	// Sources names the collectors that contributed, per kind.
	Sources map[Kind]string
	// Notes records a kind-specific coverage limitation (empty when none).
	Notes map[Kind]string
	// TruncatedKinds marks a kind whose reading was cut short by a collector's
	// own output limit, independently of the per-kind storage cap. It exists so
	// truncation can be represented even when the number of records that were
	// observed equals the number that were stored: a clipped listing yields a
	// partial count, and a partial count is not coverage.
	TruncatedKinds map[Kind]bool
}

// Merge folds a per-kind plan into the snapshot, replacing any existing entry
// for that kind so a snapshot holds at most one plan per kind.
func (s *Snapshot) Merge(part Snapshot) {
	if s.Total == nil {
		s.Total = map[Kind]int{}
	}
	if s.Stored == nil {
		s.Stored = map[Kind]int{}
	}
	if s.Sources == nil {
		s.Sources = map[Kind]string{}
	}
	if s.Notes == nil {
		s.Notes = map[Kind]string{}
	}
	if s.TruncatedKinds == nil {
		s.TruncatedKinds = map[Kind]bool{}
	}
	if s.ObservedAt == 0 {
		s.ObservedAt = part.ObservedAt
	}
	for _, item := range part.Items {
		s.Items = append(s.Items, item)
	}
	for kind, value := range part.Total {
		s.Total[kind] = value
		s.Stored[kind] = part.Stored[kind]
		s.Sources[kind] = part.Sources[kind]
		s.Notes[kind] = part.Notes[kind]
		s.TruncatedKinds[kind] = part.TruncatedKinds[kind]
	}
}

// Truncated reports whether any kind lost items to a cap, either because the
// number observed exceeded the number stored or because the collector's own
// reading was cut short at its output limit.
func (s Snapshot) Truncated() bool {
	for kind := range s.TruncatedKinds {
		if s.TruncatedKinds[kind] {
			return true
		}
	}
	for kind, total := range s.Total {
		if total > s.Stored[kind] {
			return true
		}
	}
	return false
}

// Status mirrors the collection-status vocabulary used by the posture
// assessment, so an inventory that could not be read is never presented as an
// empty inventory.
type Status string

const (
	// Collected means usable items were gathered.
	Collected Status = "collected"
	// PermissionDenied means the OS refused the required access.
	PermissionDenied Status = "permission_denied"
	// Unsupported means the interface genuinely does not apply on this platform.
	Unsupported Status = "unsupported"
	// Incomplete means some but not all items could be gathered.
	Incomplete Status = "incomplete"
	// Failed means the collection errored unexpectedly.
	Failed Status = "failed"
)

// Collection is one collector's inventory result, including the honest status
// that explains why a kind may be empty.
type Collection struct {
	Kind   Kind
	Status Status
	Items  []Item
	Total  int
	Source string
	Note   string
	Error  string
	// Truncated reports that the collector's own command output was clipped, so
	// Total and Items are a lower bound on what exists. A collector that was cut
	// short must set it together with an Incomplete status.
	Truncated bool
}

// Collector contributes one kind of inventory. Implementations return facts
// only, must respect ctx, and must never execute caller-supplied input.
type Collector interface {
	Kind() Kind
	CollectInventory(ctx context.Context) Collection
}

// truncationDisclosure is appended by Plan to a kind's note whenever that kind's
// reading was clipped, so the disclosure travels with the stored snapshot rather
// than depending on every collector remembering to write it.
const truncationDisclosure = "the listing was cut off at the collection output limit, so it is partial: fewer records were observed than exist"

// Plan bounds one collector's collection into a storable snapshot, applying the
// per-kind cap deterministically (items are kept in the order supplied, which
// each collector sorts). It never returns an error for size: oversize is
// represented as truncation, which the UI must surface.
//
// It takes the whole Collection rather than loose arguments so a collector's
// truncation flag and note cannot be dropped in transit.
func Plan(result Collection, observedAt int64) Snapshot {
	kind := result.Kind
	items := result.Items
	total := result.Total
	if total < len(items) {
		total = len(items)
	}
	note := result.Note
	if result.Truncated {
		if note != "" {
			note = note + "; " + truncationDisclosure
		} else {
			note = truncationDisclosure
		}
	}
	snap := Snapshot{
		ObservedAt:     observedAt,
		Total:          map[Kind]int{kind: total},
		Stored:         map[Kind]int{kind: 0},
		Sources:        map[Kind]string{kind: result.Source},
		Notes:          map[Kind]string{kind: note},
		TruncatedKinds: map[Kind]bool{kind: result.Truncated},
	}
	kept := items
	if len(kept) > MaxKindItems {
		kept = kept[:MaxKindItems]
	}
	for _, item := range kept {
		normalized, err := item.Normalize()
		if err != nil {
			continue
		}
		snap.Items = append(snap.Items, normalized)
	}
	snap.Stored[kind] = len(snap.Items)
	return snap
}
