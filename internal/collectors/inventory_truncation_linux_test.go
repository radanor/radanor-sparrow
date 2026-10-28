//go:build linux

package collectors

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/radanor/radanor-shield/internal/inventory"
)

// inventoryTruncationDisclosure mirrors the wording the inventory package appends
// to a clipped kind's note; it is asserted here so the disclosure the UI renders
// cannot be lost in transit.
const inventoryTruncationDisclosure = "cut off at the collection output limit"

// CE-6 H-1 regression tests.
//
// The defect these pin: the shared command runner capped captured output and
// discarded the excess without telling anyone. A host with enough packages (a
// little over four thousand, at roughly 60 bytes per line) had its package
// listing clipped, the collector counted only what it received, and because that
// partial count was also the number of records stored, the snapshot reported
// itself as a complete inventory of a smaller system. Nothing in the API, the
// store or the UI could tell the two apart.
//
// These tests drive the *real* collectors through the *real* runner, using a
// synthetic command on PATH, so the whole path — capture cap, collector status,
// coverage plan — is exercised rather than a hand-built struct. The commands in
// the collector are fixed at compile time, which is exactly why a PATH shim is
// the honest way to reproduce an oversized host here.

// installShimTool writes an executable stand-in for a tool into dir and prepends
// dir to PATH for the duration of the test. The rest of PATH is preserved so the
// shim can still use the system's own utilities.
func installShimTool(t *testing.T, tool, payload string) {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\nexec cat " + shellQuote(payload) + "\n"
	if err := os.WriteFile(filepath.Join(dir, tool), []byte(script), 0o755); err != nil {
		t.Fatalf("writing the %s shim: %v", tool, err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

// writeSyntheticOutput materialises `lines` produced by build into a file and
// returns its path. Building the payload in Go rather than in a shell loop keeps
// the test fast enough to run on every change.
func writeSyntheticOutput(t *testing.T, build func(i int) string, lines int) string {
	t.Helper()
	var builder strings.Builder
	for i := 0; i < lines; i++ {
		builder.WriteString(build(i))
		builder.WriteByte('\n')
	}
	path := filepath.Join(t.TempDir(), "synthetic.txt")
	if err := os.WriteFile(path, []byte(builder.String()), 0o644); err != nil {
		t.Fatalf("writing synthetic output: %v", err)
	}
	return path
}

// TestSoftwareInventoryKeepsARealisticHostComplete is the direct H-1
// reproduction with the shape the review used: a synthetic 12000-package host
// whose listing is larger than the 256 KiB cap the runner used to apply to it.
// The honest answer now is a complete inventory of 12000 packages, because the
// inventory path is allowed the larger bound it needs.
func TestSoftwareInventoryKeepsARealisticHostComplete(t *testing.T) {
	const packages = 12000
	// Names and versions are the lengths a real Ubuntu host reports, so the
	// listing crosses the old cap for the same reason a real host's did.
	name := func(i int) string { return fmt.Sprintf("linux-modules-extra-6.8.0-%d-generic", 30+i) }
	payload := writeSyntheticOutput(t, func(i int) string {
		return fmt.Sprintf("%s\t6.8.0-%d.%d~22.04.1\tii ", name(i), 30+i, 100+i%90)
	}, packages)
	if info, err := os.Stat(payload); err != nil {
		t.Fatal(err)
	} else if info.Size() <= maxCommandOutput {
		t.Fatalf("the synthetic listing is %d bytes; it must exceed the %d byte posture cap to reproduce H-1", info.Size(), maxCommandOutput)
	}
	installShimTool(t, "dpkg-query", payload)

	collection := SoftwareInventoryCollector{}.CollectInventory(context.Background())
	if collection.Status != inventory.Collected {
		t.Fatalf("status = %q (%s), want collected: the listing fits the inventory cap", collection.Status, collection.Error)
	}
	if collection.Truncated {
		t.Fatal("a listing inside the inventory cap was reported as truncated")
	}
	if len(collection.Items) != packages {
		t.Fatalf("items = %d, want all %d packages", len(collection.Items), packages)
	}
	if collection.Total != packages {
		t.Fatalf("total = %d, want %d", collection.Total, packages)
	}
	seen := map[string]bool{}
	for _, item := range collection.Items {
		if seen[item.Name] {
			t.Fatalf("package %q was listed twice", item.Name)
		}
		seen[item.Name] = true
	}
	if !seen[name(0)] || !seen[name(packages-1)] {
		t.Fatalf("the listing is missing its first or last package (%q / %q)", name(0), name(packages-1))
	}

	// The same bytes through the posture cap must still be reported as clipped:
	// the larger inventory bound is a deliberate allowance, not the removal of
	// the mechanism.
	posture, err := runCommandWithCap(context.Background(), defaultCommandTimeout, maxCommandOutput, "dpkg-query", "-W")
	if err != nil {
		t.Fatalf("posture-cap run: %v", err)
	}
	if !posture.Truncated {
		t.Fatal("the same listing was not reported as truncated under the posture cap")
	}

	// And the plan built from the complete collection must not claim truncation.
	plan := inventory.Plan(collection, 0)
	if plan.Truncated() {
		t.Fatal("a complete collection planned as truncated")
	}
}

// TestSoftwareInventorySurfacesAClippedReading drives the collector past its own
// inventory cap. The count that results is a lower bound, so the collection must
// say so, and the plan must carry truncation even though the observed count and
// the stored count are equal — the case that used to be indistinguishable from
// complete coverage.
func TestSoftwareInventorySurfacesAClippedReading(t *testing.T) {
	// Names near the 256-byte limit keep the item count (and so the test's
	// memory) modest while still overflowing an 8 MiB capture.
	const packages = 46000
	long := strings.Repeat("n", 200)
	payload := writeSyntheticOutput(t, func(i int) string {
		return fmt.Sprintf("%s-%06d\t1.0\tii ", long, i)
	}, packages)
	info, err := os.Stat(payload)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() <= int64(maxInventoryCommandOutput) {
		t.Fatalf("the synthetic listing is %d bytes; it must exceed the %d byte inventory cap", info.Size(), maxInventoryCommandOutput)
	}
	installShimTool(t, "dpkg-query", payload)

	collection := SoftwareInventoryCollector{}.CollectInventory(context.Background())
	if !collection.Truncated {
		t.Fatal("a listing past the inventory cap was not reported as truncated")
	}
	if collection.Status != inventory.Incomplete {
		t.Fatalf("status = %q, want %q for a clipped reading", collection.Status, inventory.Incomplete)
	}
	if collection.Error == "" {
		t.Fatal("a clipped reading must explain itself")
	}
	if !strings.Contains(collection.Error, strconv.Itoa(maxInventoryCommandOutput/1024)) {
		t.Fatalf("the explanation does not name the limit that bit: %q", collection.Error)
	}
	if len(collection.Items) == 0 {
		t.Fatal("a clipped reading still observed real packages; discarding them loses evidence")
	}
	if collection.Total != len(collection.Items) {
		t.Fatalf("total = %d with %d items; a clipped count is reported as observed", collection.Total, len(collection.Items))
	}
	// Nothing before the clip may be lost and nothing partial may be kept: the
	// items must be exactly the whole records the capture received.
	want := completeLines(t, payload, maxInventoryCommandOutput)
	if len(collection.Items) != want {
		t.Fatalf("items = %d, want the %d whole records that fitted before the clip", len(collection.Items), want)
	}
	if collection.Items[0].Name != long+"-000000" {
		t.Fatalf("first item = %q, want the first record of the listing", collection.Items[0].Name)
	}
	for _, item := range collection.Items {
		if _, err := item.Normalize(); err != nil {
			t.Fatalf("a partial record was parsed as an item: %q", item.Name)
		}
	}

	// The plan must carry the clip all the way to the snapshot. This is the H-1
	// core: the store is told the kind is truncated *because the collector said
	// its reading was clipped*, independently of any count comparison. A snapshot
	// whose observed count happens to equal its stored count — which is what a
	// clipped listing that fits the per-kind cap produces — is still truncated.
	plan := inventory.Plan(collection, 0)
	if !plan.Truncated() || !plan.TruncatedKinds[inventory.KindSoftware] {
		t.Fatal("a clipped reading planned as complete")
	}
	if !plan.TruncatedKinds[inventory.KindSoftware] && plan.Total[inventory.KindSoftware] == plan.Stored[inventory.KindSoftware] {
		t.Fatal("the snapshot carries no truncation signal and its counts agree, so the clip is invisible")
	}
	if !strings.Contains(plan.Notes[inventory.KindSoftware], inventoryTruncationDisclosure) {
		t.Fatalf("the snapshot note does not disclose the clip: %q", plan.Notes[inventory.KindSoftware])
	}
	lower := strings.ToLower(plan.Notes[inventory.KindSoftware] + " " + collection.Error)
	for _, forbidden := range []string{"complete inventory", "all installed", "every package"} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("the clipped reading claims completeness (%q): %q", forbidden, lower)
		}
	}
}

// TestEndpointInventorySurfacesAClippedReading is the same contract for `ss`.
func TestEndpointInventorySurfacesAClippedReading(t *testing.T) {
	const listeners = 46000
	long := strings.Repeat("a", 200)
	payload := writeSyntheticOutput(t, func(i int) string {
		return fmt.Sprintf("tcp LISTEN 0 128 %s:%d 0.0.0.0:* users:((\"svc\",pid=%d,fd=3))", long, 1024+(i%50000), i+1)
	}, listeners)
	info, err := os.Stat(payload)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() <= int64(maxInventoryCommandOutput) {
		t.Fatalf("the synthetic listing is %d bytes; it must exceed the %d byte inventory cap", info.Size(), maxInventoryCommandOutput)
	}
	installShimTool(t, "ss", payload)

	collection := EndpointInventoryCollector{}.CollectInventory(context.Background())
	if !collection.Truncated || collection.Status != inventory.Incomplete {
		t.Fatalf("status = %q truncated = %v, want incomplete and truncated", collection.Status, collection.Truncated)
	}
	if collection.Error == "" {
		t.Fatal("a clipped socket listing must explain itself")
	}
	if len(collection.Items) == 0 {
		t.Fatal("a clipped socket listing still observed real endpoints")
	}
	for _, item := range collection.Items {
		if _, err := item.Normalize(); err != nil {
			t.Fatalf("a partial record was parsed as an endpoint: %q", item.Name)
		}
		if item.Fields["transport"] != "tcp" && item.Fields["transport"] != "udp" {
			t.Fatalf("endpoint %q has transport %q", item.Name, item.Fields["transport"])
		}
	}
	plan := inventory.Plan(collection, 0)
	if !plan.TruncatedKinds[inventory.KindEndpoint] {
		t.Fatal("a clipped socket reading planned as complete")
	}
}

// TestServiceInventorySurfacesAClippedReading is the same contract for
// `systemctl list-units`.
func TestServiceInventorySurfacesAClippedReading(t *testing.T) {
	const units = 46000
	long := strings.Repeat("u", 200)
	payload := writeSyntheticOutput(t, func(i int) string {
		return fmt.Sprintf("%s-%06d.service loaded active running a unit", long, i)
	}, units)
	info, err := os.Stat(payload)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() <= int64(maxInventoryCommandOutput) {
		t.Fatalf("the synthetic listing is %d bytes; it must exceed the %d byte inventory cap", info.Size(), maxInventoryCommandOutput)
	}
	installShimTool(t, "systemctl", payload)

	collection := ServiceInventoryCollector{}.CollectInventory(context.Background())
	if !collection.Truncated || collection.Status != inventory.Incomplete {
		t.Fatalf("status = %q truncated = %v, want incomplete and truncated", collection.Status, collection.Truncated)
	}
	if len(collection.Items) == 0 {
		t.Fatal("a clipped service listing still observed real units")
	}
	for _, item := range collection.Items {
		if !strings.HasSuffix(item.Name, ".service") {
			t.Fatalf("a partial record was parsed as a unit: %q", item.Name)
		}
	}
	if plan := inventory.Plan(collection, 0); !plan.TruncatedKinds[inventory.KindService] {
		t.Fatal("a clipped service reading planned as complete")
	}
}

// TestClipDetectionIsNotFooledByAnExactlyFullCapture covers the boundary: output
// that fills the capture exactly and stops is complete, and output that fills it
// exactly and continues is not. Reporting either one wrongly would make the flag
// useless in both directions.
func TestClipDetectionIsNotFooledByAnExactlyFullCapture(t *testing.T) {
	exact := strings.Repeat("x", maxCommandOutput)
	payload := filepath.Join(t.TempDir(), "exact.txt")
	if err := os.WriteFile(payload, []byte(exact), 0o644); err != nil {
		t.Fatal(err)
	}
	installShimTool(t, "radanor-test-exact", payload)

	res, err := runCommandWithCap(context.Background(), defaultCommandTimeout, maxCommandOutput, "radanor-test-exact")
	if err != nil {
		t.Fatalf("exact-fit run: %v", err)
	}
	if len(res.Output) != maxCommandOutput {
		t.Fatalf("captured %d bytes, want %d", len(res.Output), maxCommandOutput)
	}
	if res.Truncated {
		t.Fatal("output that filled the capture exactly and stopped was reported as clipped")
	}

	payload = filepath.Join(t.TempDir(), "over.txt")
	if err := os.WriteFile(payload, []byte(exact+"more"), 0o644); err != nil {
		t.Fatal(err)
	}
	installShimTool(t, "radanor-test-over", payload)
	res, err = runCommandWithCap(context.Background(), defaultCommandTimeout, maxCommandOutput, "radanor-test-over")
	if err != nil {
		t.Fatalf("overflow run: %v", err)
	}
	if !res.Truncated {
		t.Fatal("output that filled the capture exactly and continued was not reported as clipped")
	}
	if len(res.Output) != maxCommandOutput {
		t.Fatalf("captured %d bytes, want the %d byte prefix", len(res.Output), maxCommandOutput)
	}
}

// completeLines counts the newline-terminated records in the first limit bytes of
// a payload, which is exactly the set a clipped capture receives.
func completeLines(t *testing.T, payload string, limit int) int {
	t.Helper()
	raw, err := os.ReadFile(payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > limit {
		raw = raw[:limit]
	}
	return strings.Count(string(raw), "\n")
}

// TestMarkTruncatedRecordsAPartialReading pins the collector-side flag on its
// own, including the case that made H-1 invisible: a clipped reading whose
// observed count equals the number of records it hands over, with the per-kind
// cap never applied. Nothing about those numbers says "partial", so the flag has
// to be carried explicitly from the moment the runner reports the clip.
func TestMarkTruncatedRecordsAPartialReading(t *testing.T) {
	items := []inventory.Item{{Kind: inventory.KindSoftware, Name: "bash", Source: "dpkg"}}
	clipped := inventory.Collection{Kind: inventory.KindSoftware, Items: items, Total: len(items), Source: "dpkg"}
	markTruncated(&clipped, commandResult{Truncated: true, Cap: maxInventoryCommandOutput}, "the package database listing")

	if !clipped.Truncated {
		t.Fatal("a clipped command result did not mark the collection truncated")
	}
	if clipped.Status != inventory.Incomplete {
		t.Fatalf("status = %q, want %q", clipped.Status, inventory.Incomplete)
	}
	if !strings.Contains(clipped.Error, strconv.Itoa(maxInventoryCommandOutput/1024)) {
		t.Fatalf("the explanation does not name the limit that bit: %q", clipped.Error)
	}
	plan := inventory.Plan(clipped, 0)
	if plan.Total[inventory.KindSoftware] != plan.Stored[inventory.KindSoftware] {
		t.Fatalf("observed %d != stored %d, which is not the hidden case",
			plan.Total[inventory.KindSoftware], plan.Stored[inventory.KindSoftware])
	}
	if !plan.Truncated() {
		t.Fatal("a clipped reading whose partial count equals its stored count planned as complete")
	}

	// An unclipped result of the same shape must stay untouched, so the mark is
	// attributable to the clip and not to the collection's contents.
	whole := inventory.Collection{Kind: inventory.KindSoftware, Items: items, Total: len(items), Source: "dpkg", Status: inventory.Collected}
	markTruncated(&whole, commandResult{Truncated: false, Cap: maxInventoryCommandOutput}, "the package database listing")
	if whole.Truncated || whole.Status != inventory.Collected || whole.Error != "" {
		t.Fatalf("an unclipped result was marked: %+v", whole)
	}
	if inventory.Plan(whole, 0).Truncated() {
		t.Fatal("an unclipped result planned as truncated")
	}
}
