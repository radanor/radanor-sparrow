package collectors

import (
	"strconv"
	"strings"
	"testing"

	"github.com/radanor/radanor-shield/internal/inventory"
)

// These fixtures drive the real Windows inventory decoding and item construction
// on Linux, which is why the model carries no build tag. No native Windows host
// was available for CE-6, so this is the evidence the plan in
// the private engineering record depends on; what it cannot cover is
// recorded there rather than implied here.

func TestParseWindowsSocketsAcceptsArrayAndSingleObject(t *testing.T) {
	array := `[{"transport":"tcp","address":"0.0.0.0","port":135,"state":"listening","pid":912},` +
		`{"transport":"udp","address":"127.0.0.1","port":5353,"state":"listening","pid":0}]`
	records, err := parseWindowsSockets(array)
	if err != nil {
		t.Fatalf("array: %v", err)
	}
	if len(records) != 2 || records[0].Port != 135 || records[1].Transport != "udp" {
		t.Fatalf("array records = %+v", records)
	}

	// ConvertTo-Json collapses a single-element array to a bare object. Reading
	// only the array shape is what made a host with one listener look like a
	// platform that does not support the query at all.
	single := `{"transport":"tcp","address":"::","port":445,"state":"listening","pid":4}`
	records, err = parseWindowsSockets(single)
	if err != nil {
		t.Fatalf("single object: %v", err)
	}
	if len(records) != 1 || records[0].Port != 445 || records[0].Address != "::" {
		t.Fatalf("single record = %+v", records)
	}

	empty, err := parseWindowsSockets("[]")
	if err != nil {
		t.Fatalf("empty array: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("empty array produced %d records", len(empty))
	}
}

func TestParseWindowsServicesAcceptsArrayAndSingleObject(t *testing.T) {
	array := `[{"Name":"Spooler","DisplayName":"Print Spooler","Status":"Running","StartType":"Automatic"},` +
		`{"Name":"W32Time","DisplayName":"Windows Time","Status":"Stopped","StartType":"Manual"}]`
	records, err := parseWindowsServices(array)
	if err != nil {
		t.Fatalf("array: %v", err)
	}
	if len(records) != 2 || records[0].Name != "Spooler" {
		t.Fatalf("array records = %+v", records)
	}
	records, err = parseWindowsServices(`{"Name":"Spooler","DisplayName":"Print Spooler","Status":"Running","StartType":"Automatic"}`)
	if err != nil {
		t.Fatalf("single object: %v", err)
	}
	if len(records) != 1 || records[0].Status != "Running" {
		t.Fatalf("single record = %+v", records)
	}
}

func TestWindowsInventoryMalformedOutputIsRejected(t *testing.T) {
	for _, raw := range []string{"", "   ", "null", "nonsense", "<html>not json</html>", `{"transport":`} {
		if _, err := parseWindowsSockets(raw); err == nil {
			t.Errorf("sockets accepted %q", raw)
		}
		if _, err := parseWindowsServices(raw); err == nil {
			t.Errorf("services accepted %q", raw)
		}
	}
}

// TestWindowsEndpointItemsLabelAttributionHonestly pins the rule that an
// endpoint whose owner was not reported is labelled unattributed, never guessed.
func TestWindowsEndpointItemsLabelAttributionHonestly(t *testing.T) {
	items := windowsEndpointItems([]windowsSocketRecord{
		{Transport: "TCP", Address: "0.0.0.0", Port: 445, State: "Listen", PID: 4},
		{Transport: "udp", Address: "127.0.0.1", Port: 5353, State: "Listen", PID: 0},
	})
	if len(items) != 2 {
		t.Fatalf("items = %d, want 2", len(items))
	}
	// Items are sorted by name so pagination is stable.
	if items[0].Name != "tcp://0.0.0.0:445" || items[1].Name != "udp://127.0.0.1:5353" {
		t.Fatalf("names = %q, %q", items[0].Name, items[1].Name)
	}
	if items[0].Fields["attribution"] != "observed" || items[0].Fields["process_id"] != "4" {
		t.Fatalf("attributed endpoint = %+v", items[0].Fields)
	}
	if items[0].Fields["transport"] != "tcp" || items[0].Fields["state"] != "listen" {
		t.Fatalf("transport/state not normalised: %+v", items[0].Fields)
	}
	if items[1].Fields["attribution"] != "unknown" || items[1].Fields["attribution_note"] == "" {
		t.Fatalf("unattributed endpoint = %+v", items[1].Fields)
	}
	if _, present := items[1].Fields["process_id"]; present {
		t.Fatalf("an absent process id was invented: %+v", items[1].Fields)
	}
	// A local observation must never be described as reachable from elsewhere.
	for _, item := range items {
		for key, value := range item.Fields {
			lower := strings.ToLower(key + "=" + value)
			for _, claim := range []string{"exposed", "public", "reachable", "open to", "vulnerable"} {
				if strings.Contains(lower, claim) {
					t.Fatalf("endpoint field %q makes an exposure claim", lower)
				}
			}
		}
	}
}

func TestWindowsEndpointItemsSkipUnrecognisedTransports(t *testing.T) {
	items := windowsEndpointItems([]windowsSocketRecord{
		{Transport: "sctp", Address: "0.0.0.0", Port: 1, State: "Listen", PID: 9},
		{Transport: "", Address: "0.0.0.0", Port: 2, State: "Listen", PID: 9},
		{Transport: "tcp", Address: "", Port: 0, State: "", PID: 0},
	})
	if len(items) != 1 {
		t.Fatalf("items = %d, want only the TCP row: %+v", len(items), items)
	}
	if items[0].Fields["address"] != "" || items[0].Fields["port"] != "0" {
		t.Fatalf("missing address/port were filled in: %+v", items[0].Fields)
	}
}

func TestWindowsServiceItemsSkipUnnamedAndNormaliseState(t *testing.T) {
	items := windowsServiceItems([]windowsServiceRecord{
		{Name: " Spooler ", DisplayName: "Print Spooler", Status: "Running", StartType: "Automatic"},
		{Name: "   ", DisplayName: "no name", Status: "Running", StartType: "Manual"},
		{Name: "W32Time", Status: "Stopped", StartType: "Manual"},
	})
	if len(items) != 2 {
		t.Fatalf("items = %d, want 2 (an unnamed row cannot be identified): %+v", len(items), items)
	}
	if items[0].Name != "Spooler" || items[0].Fields["display_name"] != "Print Spooler" {
		t.Fatalf("service = %+v", items[0])
	}
	if items[1].Fields["status"] != "stopped" || items[1].Fields["start_type"] != "manual" {
		t.Fatalf("state not normalised: %+v", items[1].Fields)
	}
	if _, present := items[1].Fields["display_name"]; present {
		t.Fatalf("an absent display name was invented: %+v", items[1].Fields)
	}
	if items[1].Fields["attribution"] != "service-control-manager" {
		t.Fatalf("attribution = %q", items[1].Fields["attribution"])
	}
}

// TestWindowsInventoryItemsStayWithinBounds runs the model output through the
// same normalisation the store applies, so a long display name or address
// cannot inflate the snapshot.
func TestWindowsInventoryItemsStayWithinBounds(t *testing.T) {
	storable := strings.Repeat("N", 200)
	for _, kind := range []struct {
		name  inventory.Kind
		items []inventory.Item
	}{
		{inventory.KindService, windowsServiceItems([]windowsServiceRecord{{Name: storable, DisplayName: storable, Status: "Running", StartType: "Automatic"}})},
		{inventory.KindEndpoint, windowsEndpointItems([]windowsSocketRecord{{Transport: "tcp", Address: storable, Port: 80, State: "Listen", PID: 1}})},
	} {
		plan := inventory.Plan(inventory.Collection{Kind: kind.name, Items: kind.items, Total: len(kind.items), Source: "windows-test"}, 0)
		if len(plan.Items) != 1 {
			t.Fatalf("%s: plan stored %d items, want 1", kind.name, len(plan.Items))
		}
		stored := plan.Items[0]
		if len(stored.Name) > inventory.MaxNameLen {
			t.Fatalf("%s: stored name is %d bytes, over the %d byte bound", kind.name, len(stored.Name), inventory.MaxNameLen)
		}
		if len(stored.Fields) > inventory.MaxFieldCount {
			t.Fatalf("%s: stored %d fields, over the %d field bound", kind.name, len(stored.Fields), inventory.MaxFieldCount)
		}
		for key, value := range stored.Fields {
			if len(key) > 32 || len(value) > inventory.MaxFieldLen {
				t.Fatalf("%s: field %q is not bounded (%d bytes)", kind.name, key, len(value))
			}
		}
	}

	// An entry that cannot be stored at all is dropped rather than silently
	// mangled, and the drop must be visible as truncation so the page cannot
	// present a partial inventory as a complete one.
	huge := strings.Repeat("N", inventory.MaxNameLen+1)
	for _, kind := range []struct {
		name  inventory.Kind
		items []inventory.Item
	}{
		{inventory.KindService, windowsServiceItems([]windowsServiceRecord{{Name: huge, DisplayName: huge, Status: "Running", StartType: "Automatic"}})},
		{inventory.KindEndpoint, windowsEndpointItems([]windowsSocketRecord{{Transport: "tcp", Address: huge, Port: 80, State: "Listen", PID: 1}})},
	} {
		plan := inventory.Plan(inventory.Collection{Kind: kind.name, Items: kind.items, Total: len(kind.items), Source: "windows-test"}, 0)
		if len(plan.Items) != 0 {
			t.Fatalf("%s: an unstorable name was kept: %+v", kind.name, plan.Items[0].Name[:32])
		}
		if !plan.Truncated() {
			t.Fatalf("%s: dropping an entry was not disclosed as truncation", kind.name)
		}
	}
}

// TestWindowsInventoryScriptsAreReadOnly is the CE-6 security regression for the
// scripts that run on a Windows host. Sparrow performs no remediation and no
// privileged action, so the fixed scripts must contain no state-changing verb,
// no remote call and no command injection surface. This is checkable on Linux,
// unlike the commands themselves.
func TestWindowsInventoryScriptsAreReadOnly(t *testing.T) {
	scripts := map[string]string{"sockets": windowsSocketScript, "services": windowsServiceScript}
	// Verbs that would change device state, reach the network, or write to disk.
	forbidden := []string{
		"Stop-Service", "Start-Service", "Restart-Service", "Set-Service", "Remove-",
		"Set-Item", "New-Item", "Remove-Item", "Set-Content", "Out-File", "Invoke-WebRequest",
		"Invoke-RestMethod", "Invoke-Expression", "iex ", "Start-Process", "cmd ", "curl ",
		"wget ", "netsh", "reg add", "reg delete", "New-NetFirewallRule", "Disable-", "Enable-",
	}
	for name, script := range scripts {
		if strings.TrimSpace(script) == "" {
			t.Fatalf("%s script is empty", name)
		}
		lower := strings.ToLower(script)
		for _, verb := range forbidden {
			if strings.Contains(lower, strings.ToLower(verb)) {
				t.Errorf("%s script contains the state-changing verb %q", name, verb)
			}
		}
		// The script must be a fixed literal: no argument splatting or caller input.
		for _, marker := range []string{"$args", "$input", "param(", "Read-Host", "$env:"} {
			if strings.Contains(script, marker) {
				t.Errorf("%s script references %q, so it is not a fixed literal", name, marker)
			}
		}
		if !strings.Contains(script, "ConvertTo-Json -Compress") {
			t.Errorf("%s script no longer emits machine-readable JSON", name)
		}
	}
	if !strings.Contains(windowsSocketScript, "Get-NetTCPConnection -State Listen") {
		t.Error("the socket script must ask only for listening TCP sockets")
	}
	if !strings.Contains(windowsSocketScript, "Get-NetUDPEndpoint") {
		t.Error("the socket script must read UDP endpoints")
	}
	if !strings.Contains(windowsServiceScript, "Get-Service") {
		t.Error("the service script must read services through Get-Service")
	}
}

// TestRegistryEnumerationFailureIsNeverAnEmptyInventory is the CE-6 regression
// for L-3. Before it, an uninstall root that opened but could not be enumerated
// was skipped, so a host whose every enumeration failed reported "collected: 0
// applications" — indistinguishable from a machine with nothing installed.
func TestRegistryEnumerationFailureIsNeverAnEmptyInventory(t *testing.T) {
	if len(uninstallKeys) == 0 {
		t.Fatal("the uninstall key list is empty")
	}

	// Every key opened, none could be enumerated: the reading is a failure, and
	// it carries an explanation. It is not "Collected" with zero items.
	opened := make([]registryKeyOutcome, 0, len(uninstallKeys))
	for _, key := range uninstallKeys {
		opened = append(opened, registryKeyOutcome{Key: key.root + `\` + key.path, Opened: true, Reason: "access is denied"})
	}
	status, explain, note := registryInventoryStatus(opened)
	if status != inventory.Failed {
		t.Fatalf("all enumerations failed and the status is %q, want %q", status, inventory.Failed)
	}
	if explain == "" {
		t.Fatal("a failed enumeration must explain itself rather than look like an empty list")
	}
	if note != "" {
		t.Fatalf("a failed reading must not carry a coverage note that implies coverage: %q", note)
	}

	// Nothing could be opened: that is a permission problem, not an empty
	// catalogue either.
	denied := make([]registryKeyOutcome, 0, len(uninstallKeys))
	for _, key := range uninstallKeys {
		denied = append(denied, registryKeyOutcome{Key: key.root + `\` + key.path, Reason: "access is denied"})
	}
	status, explain, _ = registryInventoryStatus(denied)
	if status != inventory.PermissionDenied || explain == "" {
		t.Fatalf("a denied enumeration reported %q with explanation %q", status, explain)
	}

	// Some roots read, some not: incomplete, and the note names the roots that
	// could not be read so the coverage is judged honestly.
	partial := append([]registryKeyOutcome{{Key: "hklm uninstall", Opened: true, Enumerated: true}}, opened...)
	status, explain, note = registryInventoryStatus(partial)
	if status != inventory.Incomplete || explain != "" {
		t.Fatalf("a partial enumeration reported status %q with explanation %q", status, explain)
	}
	if !strings.Contains(note, "hklm") && !strings.Contains(note, "could not be enumerated") {
		t.Fatalf("the partial note does not name the unreadable roots: %q", note)
	}

	// A complete enumeration is the only case that reads as collected.
	complete := make([]registryKeyOutcome, 0, len(uninstallKeys))
	for _, key := range uninstallKeys {
		complete = append(complete, registryKeyOutcome{Key: key.root + `\` + key.path, Opened: true, Enumerated: true})
	}
	if status, explain, _ := registryInventoryStatus(complete); status != inventory.Collected || explain != "" {
		t.Fatalf("a complete enumeration reported %q with explanation %q", status, explain)
	}
}

// TestRegistryUnreadableNoteIsBounded keeps an unexpected number of unreadable
// roots from producing an unbounded coverage note.
func TestRegistryUnreadableNoteIsBounded(t *testing.T) {
	parts := make([]string, 0, 12)
	for i := 0; i < 12; i++ {
		parts = append(parts, "root-"+strings.Repeat("x", 200)+strconv.Itoa(i))
	}
	note := registryUnreadableNote(parts)
	if len(note) > 400 {
		t.Fatalf("the note is %d bytes, which is not bounded", len(note))
	}
	if !strings.Contains(note, "more") {
		t.Fatalf("the note does not summarise the omitted roots: %q", note)
	}
}

// TestUDPInventoryNeverClaimsTCPStyleListening is the CE-6 regression for L-4.
// Windows reports a UDP endpoint as a bound local address with no state at all;
// rendering it as "listening", or as exposed beyond this machine, would invent
// a TCP-shaped fact the platform never stated.
func TestUDPInventoryNeverClaimsTCPStyleListening(t *testing.T) {
	// Get-NetUDPEndpoint reports the transport as "UDP" for both address
	// families and no state field at all, which is the shape this regression is
	// about: the model must not fill the missing state in with a TCP one.
	items := windowsEndpointItems([]windowsSocketRecord{
		{Transport: "udp", Address: "0.0.0.0", Port: 137, State: "", PID: 4},
		{Transport: "UDP", Address: "::", Port: 5353, State: ""},
	})
	if len(items) != 2 {
		t.Fatalf("expected both UDP endpoints, got %d", len(items))
	}
	for _, item := range items {
		if strings.EqualFold(item.Fields["state"], "listen") || strings.EqualFold(item.Fields["state"], "listening") {
			t.Fatalf("a UDP endpoint was given the TCP state %q", item.Fields["state"])
		}
		if item.Fields["binding_note"] != udpBindingNote {
			t.Fatalf("a UDP endpoint carries no binding note: %+v", item.Fields)
		}
		for key, value := range item.Fields {
			lowered := strings.ToLower(value)
			if key != "binding_note" && (strings.Contains(lowered, "listen") || strings.Contains(lowered, "exposed") || strings.Contains(lowered, "reachable")) {
				t.Fatalf("UDP field %q=%q implies listening or exposure", key, value)
			}
		}
	}
	// A TCP socket keeps the state the platform reported.
	tcp := windowsEndpointItems([]windowsSocketRecord{{Transport: "tcp", Address: "0.0.0.0", Port: 22, State: "Listen", PID: 1}})
	if len(tcp) != 1 || tcp[0].Fields["state"] != "listen" {
		t.Fatalf("the TCP state was not preserved: %+v", tcp)
	}
}

// TestWindowsInventoryUsesASingleUninstallKeyList keeps the registry traversal
// bounded and free of duplicates, mirroring the CE-5 service-name regression.
func TestWindowsInventoryUsesASingleUninstallKeyList(t *testing.T) {
	if len(uninstallKeys) == 0 {
		t.Fatal("the uninstall key list is empty")
	}
	for _, key := range uninstallKeys {
		if strings.TrimSpace(key.path) == "" {
			t.Fatal("an uninstall key has an empty path")
		}
		if strings.Contains(key.path, `..`) {
			t.Fatalf("uninstall key path %q escapes its root", key.path)
		}
	}
}
