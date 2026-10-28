package assessment

import "testing"

// A finding may only be resolved by fresh, reliable evidence. Every
// inconclusive outcome must leave the lifecycle untouched.
func TestLifecycleOnlyResolvesOnFreshPass(t *testing.T) {
	cases := []struct {
		name    string
		current Lifecycle
		result  Result
		fresh   bool
		want    Lifecycle
		changed bool
	}{
		{"fresh fail creates a new finding", "", Fail, true, LifecycleNew, true},
		{"a second confirmed failure is continuing", LifecycleNew, Fail, true, LifecycleContinuing, true},
		{"fresh fail continues an existing finding", LifecycleContinuing, Fail, true, LifecycleContinuing, false},
		{"fresh fail reopens a resolved finding", LifecycleResolved, Fail, true, LifecycleReopened, true},
		{"repeat fail keeps a reopened finding reopened", LifecycleReopened, Fail, true, LifecycleReopened, false},
		{"fresh pass resolves", LifecycleContinuing, Pass, true, LifecycleResolved, true},
		{"fresh pass resolves a reopened finding", LifecycleReopened, Pass, true, LifecycleResolved, true},
		{"pass on stale evidence resolves nothing", LifecycleContinuing, Pass, false, LifecycleContinuing, false},
		{"unknown never resolves", LifecycleContinuing, Unknown, true, LifecycleContinuing, false},
		{"not supported never resolves", LifecycleContinuing, NotSupported, true, LifecycleContinuing, false},
		{"stale never resolves", LifecycleReopened, Stale, false, LifecycleReopened, false},
		{"unknown leaves a new finding new", LifecycleNew, Unknown, true, LifecycleNew, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, changed := Transition(tc.current, CheckResult{Result: tc.result, Fresh: tc.fresh})
			if got != tc.want || changed != tc.changed {
				t.Fatalf("Transition(%q, %s fresh=%v) = (%q, %v); want (%q, %v)", tc.current, tc.result, tc.fresh, got, changed, tc.want, tc.changed)
			}
		})
	}
}

func TestLifecyclePassesDoNotResolveResolvedFindingsAgain(t *testing.T) {
	got, changed := Transition(LifecycleResolved, CheckResult{Result: Pass, Fresh: true})
	if got != LifecycleResolved || changed {
		t.Fatalf("already-resolved finding changed to (%q, %v)", got, changed)
	}
}

func TestOnlyFailuresGenerateFindings(t *testing.T) {
	for _, r := range []Result{Pass, Unknown, NotSupported, Stale} {
		if GeneratesFinding(r) {
			t.Fatalf("result %q must not generate a finding", r)
		}
	}
	if !GeneratesFinding(Fail) {
		t.Fatal("a confirmed failure must generate a finding")
	}
}

func TestFingerprintIsStableAcrossChangingEvidence(t *testing.T) {
	// The same weakness observed with different evidence must keep one identity,
	// otherwise every scan would mint a new finding and leave the old one open.
	first := Fingerprint("check", "1.0.0", "ubuntu", Observation{Collector: "c", Values: []Value{{Key: "count", Kind: ValueInt, Int: int64Ptr(0)}}})
	second := Fingerprint("check", "1.0.0", "ubuntu", Observation{Collector: "c", Values: []Value{{Key: "count", Kind: ValueInt, Int: int64Ptr(3)}, {Key: "extra", Kind: ValueString, Text: "detail"}}})
	if first != second {
		t.Fatalf("fingerprint changed when only the evidence changed: %s != %s", first, second)
	}
	if len(first) != 64 {
		t.Fatalf("fingerprint length = %d, want 64", len(first))
	}
	base := Observation{Collector: "c"}
	if Fingerprint("check2", "1.0.0", "ubuntu", base) == first {
		t.Fatal("a different check must produce a different fingerprint")
	}
	if Fingerprint("check", "1.0.1", "ubuntu", base) == first {
		t.Fatal("a different rule version must produce a different fingerprint")
	}
	if Fingerprint("check", "1.0.0", "windows", base) == first {
		t.Fatal("a different platform must produce a different fingerprint")
	}
	if Fingerprint("check", "1.0.0", "ubuntu", Observation{Collector: "other"}) == first {
		t.Fatal("a different collector must produce a different fingerprint")
	}
}

func int64Ptr(v int64) *int64 { return &v }
