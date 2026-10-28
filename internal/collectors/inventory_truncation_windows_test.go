//go:build windows

package collectors

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/radanor/radanor-shield/internal/inventory"
)

// CE-6 H-1 regression tests for the Windows inventory paths.
//
// STATUS: compiled and vetted under GOOS=windows, NOT executed. No native
// Windows 11 host was available while this correction was written, so nothing
// here is a runtime result. It is written to run unchanged on a Windows host and
// is covered by the private engineering record, which records it as an
// outstanding validation item rather than as evidence already obtained.
//
// The contract under test is the same one the Linux tests exercise: a Windows
// inventory whose own PowerShell output was clipped at the capture cap must never
// be presented as a complete inventory of a smaller machine. On Windows the
// clipped document does not decode, so the honest outcome is an Incomplete
// collection that carries the truncation flag and an explanation naming the
// limit — which the core then refuses to merge, so the kind keeps its previous
// snapshot instead of being replaced by an empty one.

// installPowerShellShim writes a stand-in for powershell.exe that prints the
// payload verbatim, and prepends its directory to PATH for the test. Go resolves
// an extensionless name through PATHEXT on Windows, so a .cmd file is found.
func installPowerShellShim(t *testing.T, payload string) {
	t.Helper()
	if _, err := exec.LookPath("cmd"); err != nil {
		t.Skipf("cmd.exe is not available, so the shim cannot run: %v", err)
	}
	dir := t.TempDir()
	script := "@echo off\r\ntype \"" + payload + "\"\r\n"
	if err := os.WriteFile(filepath.Join(dir, "powershell.cmd"), []byte(script), 0o755); err != nil {
		t.Fatalf("writing the powershell shim: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// writeOversizeJSON writes a JSON array of socket-shaped objects whose total size
// exceeds the inventory capture cap.
func writeOversizeJSON(t *testing.T, records int) string {
	t.Helper()
	var builder strings.Builder
	builder.WriteString("[")
	for i := 0; i < records; i++ {
		if i > 0 {
			builder.WriteString(",")
		}
		fmt.Fprintf(&builder, `{"transport":"tcp","address":"%s%06d","port":%d,"state":"listening","pid":%d}`,
			strings.Repeat("a", 180), i, 1024+i%50000, i+1)
	}
	builder.WriteString("]")
	path := filepath.Join(t.TempDir(), "sockets.json")
	if err := os.WriteFile(path, []byte(builder.String()), 0o644); err != nil {
		t.Fatalf("writing the synthetic socket document: %v", err)
	}
	return path
}

// TestWindowsSocketInventorySurfacesAClippedDocument is the Windows H-1
// reproduction: an 8 MiB-plus PowerShell document cannot be clipped silently.
func TestWindowsSocketInventorySurfacesAClippedDocument(t *testing.T) {
	const records = 40000
	payload := writeOversizeJSON(t, records)
	info, err := os.Stat(payload)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() <= int64(maxInventoryCommandOutput) {
		t.Fatalf("the synthetic document is %d bytes; it must exceed the %d byte inventory cap", info.Size(), maxInventoryCommandOutput)
	}
	installPowerShellShim(t, payload)

	collection := EndpointInventoryCollector{}.CollectInventory(context.Background())
	if collection.Status != inventory.Incomplete {
		t.Fatalf("status = %q (%s), want incomplete for a clipped document", collection.Status, collection.Error)
	}
	if !collection.Truncated {
		t.Fatal("a clipped PowerShell document was not reported as truncated")
	}
	if !strings.Contains(collection.Error, strconv.Itoa(maxInventoryCommandOutput/1024)) {
		t.Fatalf("the explanation does not name the limit that bit: %q", collection.Error)
	}
	if !strings.Contains(collection.Error, "partial reading") {
		t.Fatalf("the explanation does not say the reading is partial: %q", collection.Error)
	}
	if len(collection.Items) != 0 {
		t.Fatalf("items = %d, want none: a document that does not decode yields no records", len(collection.Items))
	}
	// The snapshot must carry truncation, and the empty observed count must not
	// read as a complete, empty inventory.
	plan := inventory.Plan(collection, 0)
	if !plan.TruncatedKinds[inventory.KindEndpoint] {
		t.Fatal("a clipped Windows reading planned as complete")
	}
}

// TestWindowsServiceInventorySurfacesAClippedDocument is the same contract for
// the service inventory path.
func TestWindowsServiceInventorySurfacesAClippedDocument(t *testing.T) {
	var builder strings.Builder
	builder.WriteString("[")
	for i := 0; i < 40000; i++ {
		if i > 0 {
			builder.WriteString(",")
		}
		fmt.Fprintf(&builder, `{"Name":"%s%06d","DisplayName":"svc","Status":"Running","StartType":"Automatic"}`,
			strings.Repeat("s", 180), i)
	}
	builder.WriteString("]")
	payload := filepath.Join(t.TempDir(), "services.json")
	if err := os.WriteFile(payload, []byte(builder.String()), 0o644); err != nil {
		t.Fatalf("writing the synthetic service document: %v", err)
	}
	info, err := os.Stat(payload)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() <= int64(maxInventoryCommandOutput) {
		t.Fatalf("the synthetic document is %d bytes; it must exceed the %d byte inventory cap", info.Size(), maxInventoryCommandOutput)
	}
	installPowerShellShim(t, payload)

	collection := ServiceInventoryCollector{}.CollectInventory(context.Background())
	if collection.Status != inventory.Incomplete || !collection.Truncated {
		t.Fatalf("status = %q truncated = %v, want incomplete and truncated", collection.Status, collection.Truncated)
	}
	if !strings.Contains(collection.Error, "partial reading") {
		t.Fatalf("the explanation does not say the reading is partial: %q", collection.Error)
	}
	if plan := inventory.Plan(collection, 0); !plan.TruncatedKinds[inventory.KindService] {
		t.Fatal("a clipped Windows service reading planned as complete")
	}
}

// TestWindowsInventoryInsideTheCapIsNotMarked is the contrast case: an ordinary
// socket document must be reported as a complete reading, so the flag means
// something when it does appear.
func TestWindowsInventoryInsideTheCapIsNotMarked(t *testing.T) {
	payload := filepath.Join(t.TempDir(), "small.json")
	document := `[{"transport":"tcp","address":"0.0.0.0","port":445,"state":"listening","pid":4},` +
		`{"transport":"udp","address":"0.0.0.0","port":5353,"state":"listening","pid":0}]`
	if err := os.WriteFile(payload, []byte(document), 0o644); err != nil {
		t.Fatal(err)
	}
	installPowerShellShim(t, payload)

	collection := EndpointInventoryCollector{}.CollectInventory(context.Background())
	if collection.Status != inventory.Collected {
		t.Fatalf("status = %q (%s), want collected for a document inside the cap", collection.Status, collection.Error)
	}
	if collection.Truncated {
		t.Fatal("a document inside the cap was reported as truncated")
	}
	if len(collection.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(collection.Items))
	}
	for _, item := range collection.Items {
		if item.Fields["attribution"] == "unknown" && item.Fields["attribution_note"] == "" {
			t.Fatalf("endpoint %q is unattributed without saying so", item.Name)
		}
	}
	if inventory.Plan(collection, 0).Truncated() {
		t.Fatal("a complete Windows reading planned as truncated")
	}
}
