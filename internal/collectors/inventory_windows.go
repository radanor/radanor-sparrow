//go:build windows

package collectors

import (
	"context"
	"sort"
	"strconv"
	"strings"

	"github.com/radanor/radanor-shield/internal/inventory"
	"golang.org/x/sys/windows/registry"
)

// CE-6 Windows inventory collectors. They are cross-compiled and fixture-checked
// only: no native Windows 11 host was available during this work, so nothing
// here claims verified native behaviour. See the private engineering record.
//
// Everything that can be verified without Windows — the script text, the JSON
// decoding and the item construction — lives in inventory_windows_model.go,
// which carries no build tag and is driven by fixtures on every change. This
// file holds only the command execution and the registry reads.

// PlatformInventoryCollectors returns the Windows inventory collector set. The
// set is fixed at compile time: no caller can name a source or a path.
func PlatformInventoryCollectors() []inventory.Collector {
	return []inventory.Collector{
		SoftwareInventoryCollector{},
		EndpointInventoryCollector{},
		ServiceInventoryCollector{},
	}
}

// SoftwareInventoryCollector catalogues installed applications from the
// documented Uninstall registry keys, in both registry views plus the per-user
// hive. This is an application inventory, not a security assessment, and no
// version is matched against an advisory database.
//
// The listing is an application inventory, not a catalogue of everything
// installed: portable and Microsoft Store applications are not registered for
// uninstall and may not appear, which the collection note states.
type SoftwareInventoryCollector struct{}

func (SoftwareInventoryCollector) Kind() inventory.Kind { return inventory.KindSoftware }

// windowsSoftwareInventoryNote is the coverage promise this collector attaches
// to its snapshot. It is a constant so the wording the UI repeats can be
// asserted in a test.
const windowsSoftwareInventoryNote = "applications registered for uninstall; portable and Microsoft Store applications may not appear"

// registryEntryNameFailure explains a reading in which the registry listed
// uninstall entries but none carried a readable application name.
const registryEntryNameFailure = "the registry listed uninstall entries but none carried a readable application name, so this inventory cannot be read as empty"

func (SoftwareInventoryCollector) CollectInventory(ctx context.Context) inventory.Collection {
	collection := inventory.Collection{Kind: inventory.KindSoftware, Source: "registry-uninstall"}
	items := make([]inventory.Item, 0, 256)
	seen := map[string]bool{}
	outcomes := make([]registryKeyOutcome, 0, len(uninstallKeys))
	listed := 0
	for _, key := range uninstallKeys {
		label := key.root + `\` + key.path
		outcome := registryKeyOutcome{Key: label}
		hive, ok := registryHive(key.root)
		if !ok {
			// The hive cannot be resolved at all in this process, so the root was
			// never opened and must not read as an empty one.
			outcome.Reason = "the registry hive is not available to this process"
			outcomes = append(outcomes, outcome)
			continue
		}
		k, err := registry.OpenKey(hive, key.path, registry.READ)
		if err != nil {
			outcome.Reason = err.Error()
			outcomes = append(outcomes, outcome)
			continue
		}
		outcome.Opened = true
		subkeys, err := k.ReadSubKeyNames(-1)
		k.Close()
		if err != nil {
			outcome.Reason = err.Error()
			outcomes = append(outcomes, outcome)
			continue
		}
		outcome.Enumerated = true
		outcomes = append(outcomes, outcome)
		listed += len(subkeys)
		for _, sub := range subkeys {
			display, ok := regString(hive, key.path+`\`+sub, "DisplayName")
			if !ok || strings.TrimSpace(display) == "" {
				continue
			}
			version, _ := regString(hive, key.path+`\`+sub, "DisplayVersion")
			publisher, _ := regString(hive, key.path+`\`+sub, "Publisher")
			identity := strings.ToLower(display) + "\x00" + strings.ToLower(version)
			if seen[identity] {
				continue
			}
			seen[identity] = true
			items = append(items, inventory.Item{
				Kind: inventory.KindSoftware, Name: display, Version: version, Source: "registry-uninstall",
				Fields: map[string]string{"publisher": publisher, "origin": "windows-application"},
			})
		}
	}
	status, reason, coverageNote := registryInventoryStatus(outcomes)
	if reason != "" {
		// Nothing usable was read. The status and the reason are the whole
		// answer: an empty item list must never be presented as an empty system.
		collection.Status = status
		collection.Error = reason
		return collection
	}
	if listed > 0 && len(items) == 0 {
		// The keys were listed, but no entry exposed a name that could be stored.
		// That is a collection problem rather than a machine with nothing
		// installed, and it is reported as one.
		collection.Status = inventory.Incomplete
		collection.Error = registryEntryNameFailure
		return collection
	}
	collection.Total = len(items)
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	collection.Items = items
	collection.Status = status
	collection.Note = windowsSoftwareInventoryNote
	if coverageNote != "" {
		collection.Note += "; " + coverageNote
	}
	return collection
}

// EndpointInventoryCollector catalogues listening TCP and UDP endpoints.
//
// Source: Get-NetTCPConnection -State Listen and Get-NetUDPEndpoint, both
// read-only. Owning process attribution is requested where the cmdlet provides
// it; when it is absent the endpoint is reported as unattributed. A listening
// socket is a local observation: nothing here claims the port is reachable from
// another machine.
type EndpointInventoryCollector struct{}

func (EndpointInventoryCollector) Kind() inventory.Kind { return inventory.KindEndpoint }

func (EndpointInventoryCollector) CollectInventory(ctx context.Context) inventory.Collection {
	collection := inventory.Collection{Kind: inventory.KindEndpoint, Source: "Get-NetTCPConnection/Get-NetUDPEndpoint"}
	res, err := windowsPowerShellOutput(ctx, maxInventoryCommandOutput, windowsSocketScript)
	if err != nil {
		collection.Status = inventory.Unsupported
		collection.Error = "the socket inventory is not available on this system"
		return collection
	}
	records, err := parseWindowsSockets(res.Output)
	if err != nil {
		collection.Status = inventory.Incomplete
		collection.Error = err.Error()
		// Name the real cause when the document did not decode because it was
		// clipped: "could not be decoded" alone would hide it.
		markTruncated(&collection, res, "the socket listing")
		return collection
	}
	items := windowsEndpointItems(records)
	collection.Total = len(items)
	collection.Items = items
	collection.Status = inventory.Collected
	unattributed := 0
	udp := 0
	for _, item := range items {
		if item.Fields["attribution"] == "unknown" {
			unattributed++
		}
		if item.Fields["transport"] == "udp" {
			udp++
		}
	}
	notes := make([]string, 0, 2)
	if unattributed > 0 {
		notes = append(notes, strconv.Itoa(unattributed)+" endpoint(s) could not be attributed to a process; they are listed as unattributed rather than guessed")
	}
	if udp > 0 {
		notes = append(notes, strconv.Itoa(udp)+" UDP endpoint(s) are bound rather than listening; they have no accept queue, and nothing here states they are reachable from another machine")
	}
	collection.Note = strings.Join(notes, "; ")
	// A JSON document clipped at the capture limit does not decode, so truncation
	// normally surfaces above as a decode failure; this covers the case where the
	// clip happened to fall on a decodable boundary, which would otherwise report
	// a partial socket set as a complete one.
	markTruncated(&collection, res, "the socket listing")
	return collection
}

// ServiceInventoryCollector catalogues Windows services and their state.
//
// Source: Get-Service through PowerShell JSON. Only the English property names
// Name, DisplayName, Status and StartType are read. A service the platform does
// not report is absent from the list rather than assumed stopped.
type ServiceInventoryCollector struct{}

func (ServiceInventoryCollector) Kind() inventory.Kind { return inventory.KindService }

func (ServiceInventoryCollector) CollectInventory(ctx context.Context) inventory.Collection {
	collection := inventory.Collection{Kind: inventory.KindService, Source: "Get-Service"}
	res, err := windowsPowerShellOutput(ctx, maxInventoryCommandOutput, windowsServiceScript)
	if err != nil {
		collection.Status = inventory.Unsupported
		collection.Error = "the service inventory is not available on this system"
		return collection
	}
	records, err := parseWindowsServices(res.Output)
	if err != nil {
		collection.Status = inventory.Incomplete
		collection.Error = err.Error()
		markTruncated(&collection, res, "the service listing")
		return collection
	}
	items := windowsServiceItems(records)
	collection.Total = len(items)
	collection.Items = items
	collection.Status = inventory.Collected
	collection.Note = "Windows services as reported by the service control manager"
	markTruncated(&collection, res, "the service listing")
	return collection
}
