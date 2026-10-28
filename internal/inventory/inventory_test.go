package inventory

import (
	"strings"
	"testing"
)

func TestOnlyKnownKindsAreAccepted(t *testing.T) {
	for _, kind := range []Kind{KindSoftware, KindEndpoint, KindService} {
		if !kind.Known() {
			t.Errorf("%q should be a known kind", kind)
		}
	}
	for _, kind := range []Kind{"", "SOFTWARE", "packages", "endpoint "} {
		if kind.Known() {
			t.Errorf("%q must not be accepted as a kind", kind)
		}
	}
}

func TestNormalizeBoundsEveryField(t *testing.T) {
	item := Item{
		Kind:    KindSoftware,
		Name:    "  bash  ",
		Version: strings.Repeat("v", MaxVersionLen+10),
		Source:  strings.Repeat("s", MaxSourceLen+10),
		Fields:  map[string]string{"origin": strings.Repeat("o", MaxFieldLen+10)},
	}
	normalized, err := item.Normalize()
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if normalized.Name != "bash" {
		t.Fatalf("name = %q, want it trimmed", normalized.Name)
	}
	if len(normalized.Version) != MaxVersionLen || len(normalized.Source) != MaxSourceLen {
		t.Fatalf("version/source lengths = %d/%d, want %d/%d", len(normalized.Version), len(normalized.Source), MaxVersionLen, MaxSourceLen)
	}
	if len(normalized.Fields["origin"]) != MaxFieldLen {
		t.Fatalf("field length = %d, want %d", len(normalized.Fields["origin"]), MaxFieldLen)
	}
}

func TestNormalizeRejectsUnusableItems(t *testing.T) {
	cases := []struct {
		name string
		item Item
	}{
		{"empty name", Item{Kind: KindSoftware, Name: "   "}},
		{"over-long name", Item{Kind: KindSoftware, Name: strings.Repeat("n", MaxNameLen+1)}},
		{"unknown kind", Item{Kind: "packages", Name: "bash"}},
		{"no kind", Item{Name: "bash"}},
	}
	for _, tc := range cases {
		if _, err := tc.item.Normalize(); err == nil {
			t.Errorf("%s: accepted an unusable item", tc.name)
		}
	}
}

func TestNormalizeCapsTheNumberOfFields(t *testing.T) {
	fields := map[string]string{}
	for i := 0; i < MaxFieldCount+20; i++ {
		fields["k"+strings.Repeat("x", i)] = "v"
	}
	normalized, err := Item{Kind: KindEndpoint, Name: "tcp://0.0.0.0:22", Fields: fields}.Normalize()
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if len(normalized.Fields) != MaxFieldCount {
		t.Fatalf("fields = %d, want the %d field cap", len(normalized.Fields), MaxFieldCount)
	}
}

// TestPlanDisclosesTruncation is the guarantee the pages depend on: a snapshot
// that lost entries must say so, so a bounded list is never presented as the
// whole picture.
func TestPlanDisclosesTruncation(t *testing.T) {
	items := make([]Item, 0, 5)
	for i := 0; i < 5; i++ {
		items = append(items, Item{Kind: KindSoftware, Name: "pkg-" + strings.Repeat("a", i), Source: "test"})
	}
	// An unstorable entry is dropped, and the drop is reported as truncation.
	items = append(items, Item{Kind: KindSoftware, Name: strings.Repeat("n", MaxNameLen+1), Source: "test"})
	plan := Plan(Collection{Kind: KindSoftware, Items: items, Total: len(items), Source: "test", Note: "a note"}, 12345)
	if plan.Stored[KindSoftware] != 5 {
		t.Fatalf("stored = %d, want 5 usable items", plan.Stored[KindSoftware])
	}
	if !plan.Truncated() {
		t.Fatal("a dropped entry must be disclosed as truncation")
	}
	if plan.Sources[KindSoftware] != "test" || plan.Notes[KindSoftware] != "a note" {
		t.Fatalf("provenance was lost: %+v", plan)
	}
	if plan.ObservedAt != 12345 {
		t.Fatalf("observed time = %d", plan.ObservedAt)
	}

	// A complete snapshot is not marked as truncated.
	complete := Plan(Collection{Kind: KindSoftware, Items: items[:5], Total: 5, Source: "test"}, 0)
	if complete.Truncated() {
		t.Fatal("a complete snapshot was marked truncated")
	}
	// A reported total above what was supplied is still truncation: the
	// collector observed more than it could hand over.
	partial := Plan(Collection{Kind: KindSoftware, Items: items[:2], Total: 100, Source: "test"}, 0)
	if !partial.Truncated() || partial.Total[KindSoftware] != 100 {
		t.Fatalf("plan = %+v, want total 100 stored 2", partial)
	}
}

// TestPlanCarriesClippedReadingTruncation is the H-1 guard at the model layer.
// The case it pins is the one the defect hid behind: the collector's listing was
// clipped, the number of items observed happens to equal the number stored, and
// the per-kind cap never applied. Nothing about the counts says "partial", so the
// snapshot must carry truncation explicitly, and the stored note must disclose
// it so the UI has something truthful to render.
func TestPlanCarriesClippedReadingTruncation(t *testing.T) {
	items := []Item{{Kind: KindSoftware, Name: "libc6", Source: "dpkg"}}
	clipped := Plan(Collection{
		Kind: KindSoftware, Items: items, Total: len(items), Source: "dpkg",
		Truncated: true,
	}, 7)
	if clipped.Total[KindSoftware] != 1 || clipped.Stored[KindSoftware] != 1 {
		t.Fatalf("counts = observed %d stored %d, want 1 and 1 so the case is the hidden one",
			clipped.Total[KindSoftware], clipped.Stored[KindSoftware])
	}
	if !clipped.Truncated() {
		t.Fatal("a clipped reading whose partial count equals its stored count was reported as complete")
	}
	if !clipped.TruncatedKinds[KindSoftware] {
		t.Fatal("the truncation flag did not reach the snapshot's per-kind map")
	}
	if clipped.Notes[KindSoftware] == "" || !strings.Contains(clipped.Notes[KindSoftware], truncationDisclosure) {
		t.Fatalf("note = %q, want the truncation disclosure to travel with the snapshot", clipped.Notes[KindSoftware])
	}

	// An unclipped reading of the very same shape must not be marked truncated,
	// so the flag is attributable to the clip and not to the counts.
	whole := Plan(Collection{Kind: KindSoftware, Items: items, Total: len(items), Source: "dpkg"}, 7)
	if whole.Truncated() {
		t.Fatal("an unclipped reading was marked truncated")
	}
	if whole.Notes[KindSoftware] != "" {
		t.Fatalf("note = %q, want no disclosure for a complete reading", whole.Notes[KindSoftware])
	}
}

// TestPlanAppliesThePerKindCap pins the cap that bounds a single kind even when
// the store's budget would allow more.
func TestPlanAppliesThePerKindCap(t *testing.T) {
	items := make([]Item, 0, MaxKindItems+10)
	for i := 0; i < MaxKindItems+10; i++ {
		items = append(items, Item{Kind: KindSoftware, Name: "pkg-" + strings.Repeat("a", 1+(i%4)) + "-" + string(rune('a'+i%26)) + string(rune('a'+(i/26)%26)) + string(rune('a'+(i/676)%26)), Source: "test"})
	}
	plan := Plan(Collection{Kind: KindSoftware, Items: items, Total: len(items), Source: "test"}, 0)
	if plan.Stored[KindSoftware] > MaxKindItems {
		t.Fatalf("stored = %d, over the %d per-kind cap", plan.Stored[KindSoftware], MaxKindItems)
	}
	if !plan.Truncated() {
		t.Fatal("hitting the cap must be disclosed as truncation")
	}
}

func TestMergeReplacesOneKindAndKeepsTheRest(t *testing.T) {
	snapshot := Snapshot{ObservedAt: 100}
	snapshot.Merge(Plan(Collection{Kind: KindSoftware, Items: []Item{{Kind: KindSoftware, Name: "bash", Source: "dpkg"}}, Total: 1, Source: "dpkg"}, 100))
	snapshot.Merge(Plan(Collection{Kind: KindEndpoint, Items: []Item{{Kind: KindEndpoint, Name: "tcp://127.0.0.1:22", Source: "ss"}}, Total: 1, Source: "ss"}, 100))
	// Replacing software must not disturb the endpoint coverage already merged.
	snapshot.Merge(Plan(Collection{Kind: KindSoftware, Items: []Item{{Kind: KindSoftware, Name: "curl", Source: "dpkg"}}, Total: 1, Source: "dpkg"}, 100))

	if snapshot.Total[KindSoftware] != 1 || snapshot.Sources[KindEndpoint] != "ss" {
		t.Fatalf("merge lost kind coverage: %+v", snapshot)
	}
	if snapshot.ObservedAt != 100 {
		t.Fatalf("observed time = %d, want the first value to stand", snapshot.ObservedAt)
	}
	software := 0
	for _, item := range snapshot.Items {
		if item.Kind == KindSoftware {
			software++
		}
	}
	// Merge appends; the store groups by kind and replaces the whole kind, so
	// both observations reaching the plan is expected and the store's per-kind
	// delete is what makes the snapshot current.
	if software != 2 {
		t.Fatalf("merged software items = %d, want both plans folded in", software)
	}
}
