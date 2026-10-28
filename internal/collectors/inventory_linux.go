//go:build linux

package collectors

import (
	"context"
	"sort"
	"strconv"
	"strings"

	"github.com/radanor/radanor-shield/internal/inventory"
)

// CE-6 Ubuntu inventory collectors: installed packages, locally listening
// endpoints, and systemd services.
//
// These are separate from the posture collectors because inventory is a
// current-state catalogue rather than a bounded evidence observation. They run
// the same unprivileged, machine-readable tools, and they never perform
// vulnerability matching or any modification.

// The coverage wording each Linux collector attaches to its snapshot. Keeping it
// in named constants means the exact promise the UI repeats can be asserted in a
// test rather than drifting between the collector and the page.
const (
	softwareInventoryNote = "installed Debian packages as reported by dpkg; this is not a catalogue of every application on the system"
	serviceInventoryNote  = "systemd service units; a unit is reported with the state systemd records for it, never inferred"
)

// SoftwareInventoryCollector catalogues installed Debian packages.
//
// Source: `dpkg-query` with a fixed machine-readable format string. Only fully
// installed packages (status "ii") are listed. This is the platform's package
// database, not a catalogue of every application on the system, and that
// distinction is stated in the UI.
type SoftwareInventoryCollector struct{}

func (SoftwareInventoryCollector) Kind() inventory.Kind { return inventory.KindSoftware }

func (SoftwareInventoryCollector) CollectInventory(ctx context.Context) inventory.Collection {
	collection := inventory.Collection{Kind: inventory.KindSoftware, Source: "dpkg-query"}
	// The full package database is a large, legitimate listing, so it gets the
	// inventory capture bound rather than the smaller posture one.
	res, err := runCommandWithCap(ctx, defaultCommandTimeout, maxInventoryCommandOutput, "dpkg-query", "-W", "-f=${binary:Package}\t${Version}\t${db:Status-Abbrev}\n")
	if err != nil {
		collection.Status, collection.Error = inventoryFailure(res.Output, err, "dpkg is not available on this system", "reading the package database requires elevated privileges")
		return collection
	}
	items := make([]inventory.Item, 0, 1024)
	for _, line := range splitLines(res.Output, res.Truncated) {
		if line == "" {
			continue
		}
		fields := strings.SplitN(line, "\t", 3)
		if len(fields) != 3 {
			continue
		}
		name, version, status := fields[0], fields[1], fields[2]
		if !strings.HasPrefix(status, "ii") {
			continue
		}
		items = append(items, inventory.Item{
			Kind: inventory.KindSoftware, Name: name, Version: version, Source: "dpkg",
			Fields: map[string]string{"origin": "debian-package"},
		})
	}
	collection.Total = len(items)
	if len(items) == 0 {
		collection.Status = inventory.Incomplete
		collection.Error = "the package database reported no installed packages, which is a collection problem rather than an empty system"
		return collection
	}
	sortItems(items)
	collection.Items = items
	collection.Status = inventory.Collected
	collection.Note = softwareInventoryNote
	markTruncated(&collection, res, "the package database listing")
	return collection
}

// EndpointInventoryCollector catalogues locally listening TCP and UDP sockets.
//
// Source: `ss -H -lntup`. The process column is requested, but the kernel only
// exposes the owning process for sockets this user may inspect, so an endpoint
// whose owner cannot be read is reported as unattributed rather than guessed.
// A listening socket is a local observation: nothing here claims the port is
// reachable from another machine.
type EndpointInventoryCollector struct{}

func (EndpointInventoryCollector) Kind() inventory.Kind { return inventory.KindEndpoint }

func (EndpointInventoryCollector) CollectInventory(ctx context.Context) inventory.Collection {
	collection := inventory.Collection{Kind: inventory.KindEndpoint, Source: "ss"}
	res, err := runCommandWithCap(ctx, defaultCommandTimeout, maxInventoryCommandOutput, "ss", "-H", "-lntup")
	if err != nil {
		collection.Status, collection.Error = inventoryFailure(res.Output, err, "ss is not available on this system", "reading socket statistics requires elevated privileges")
		return collection
	}
	items := make([]inventory.Item, 0, 64)
	unattributed := 0
	for _, line := range splitLines(res.Output, res.Truncated) {
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		netid := strings.ToLower(fields[0])
		transport := "tcp"
		switch {
		case strings.HasPrefix(netid, "tcp"):
			transport = "tcp"
		case strings.HasPrefix(netid, "udp"):
			transport = "udp"
		default:
			continue
		}
		address, port := splitEndpoint(fields[4])
		state := "listening"
		if len(fields) > 1 && fields[1] != "" {
			state = strings.ToLower(fields[1])
		}
		fields2 := map[string]string{
			"transport": transport,
			"address":   address,
			"port":      port,
			"state":     state,
		}
		// Process attribution appears as users:(("name",pid=N,fd=M)).
		if process := parseSSProcess(line); process != "" {
			fields2["process"] = process
			fields2["attribution"] = "observed"
		} else {
			fields2["attribution"] = "unknown"
			fields2["attribution_note"] = "the owning process is not visible to this user"
			unattributed++
		}
		name := transport + "://" + address + ":" + port
		items = append(items, inventory.Item{Kind: inventory.KindEndpoint, Name: name, Source: "ss", Fields: fields2})
	}
	collection.Total = len(items)
	sortItems(items)
	collection.Items = items
	collection.Status = inventory.Collected
	if unattributed > 0 {
		collection.Note = "the owning process could not be read for " + strconv.Itoa(unattributed) + " endpoint(s); they are listed as unattributed"
	}
	markTruncated(&collection, res, "the socket listing")
	return collection
}

// ServiceInventoryCollector catalogues systemd services and their state.
//
// Source: `systemctl list-units --type=service --all`. This is the documented,
// machine-readable unit listing and works without elevated privileges. A unit
// whose state cannot be read is reported as unknown rather than assumed active.
type ServiceInventoryCollector struct{}

func (ServiceInventoryCollector) Kind() inventory.Kind { return inventory.KindService }

func (ServiceInventoryCollector) CollectInventory(ctx context.Context) inventory.Collection {
	collection := inventory.Collection{Kind: inventory.KindService, Source: "systemctl"}
	res, err := runCommandWithCap(ctx, defaultCommandTimeout, maxInventoryCommandOutput, "systemctl", "list-units", "--type=service", "--all", "--no-legend", "--plain", "--no-pager")
	if err != nil {
		collection.Status, collection.Error = inventoryFailure(res.Output, err, "systemctl is not available on this system", "listing services requires elevated privileges")
		return collection
	}
	items := make([]inventory.Item, 0, 128)
	for _, line := range splitLines(res.Output, res.Truncated) {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		unit, load, active, sub := fields[0], fields[1], fields[2], fields[3]
		if !strings.HasSuffix(unit, ".service") {
			continue
		}
		description := ""
		if len(fields) > 4 {
			description = strings.Join(fields[4:], " ")
		}
		items = append(items, inventory.Item{
			Kind: inventory.KindService, Name: unit, Version: "", Source: "systemd",
			Fields: map[string]string{
				"load": load, "active": active, "sub": sub,
				"description": description, "attribution": "systemd",
			},
		})
	}
	collection.Total = len(items)
	sortItems(items)
	collection.Items = items
	collection.Status = inventory.Collected
	collection.Note = serviceInventoryNote
	markTruncated(&collection, res, "the service listing")
	return collection
}

// splitEndpoint splits an `ss` local address into address and port, handling the
// IPv6 bracket form, the scoped zone form and the wildcard form.
func splitEndpoint(value string) (string, string) {
	if value == "" {
		return "", ""
	}
	if strings.HasPrefix(value, "[") {
		if idx := strings.LastIndex(value, "]:"); idx >= 0 {
			return value[1:idx], value[idx+2:]
		}
		return value, ""
	}
	if idx := strings.LastIndex(value, ":"); idx >= 0 {
		return value[:idx], value[idx+1:]
	}
	return value, ""
}

// parseSSProcess extracts "name(pid=N)" from an ss process column.
func parseSSProcess(line string) string {
	idx := strings.Index(line, "users:((")
	if idx < 0 {
		return ""
	}
	segment := line[idx+len("users:(("):]
	if end := strings.Index(segment, ")"); end >= 0 {
		segment = segment[:end]
	}
	parts := strings.Split(segment, ",")
	name := strings.Trim(strings.TrimSpace(parts[0]), `"`)
	pid := ""
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "pid=") {
			pid = strings.TrimPrefix(part, "pid=")
		}
	}
	if name == "" {
		return ""
	}
	if pid != "" {
		return name + " (pid " + pid + ")"
	}
	return name
}

// inventoryFailure maps a failed inventory command onto the honest status.
func inventoryFailure(output string, err error, missing, denied string) (inventory.Status, string) {
	if isMissingTool(err) {
		return inventory.Unsupported, missing
	}
	if isPermissionError(output, err) {
		return inventory.PermissionDenied, denied
	}
	return inventory.Failed, boundText(err.Error())
}

// PlatformInventoryCollectors returns the Ubuntu inventory collector set. The
// set is fixed at compile time: no caller can name a source or a path.
func PlatformInventoryCollectors() []inventory.Collector {
	return []inventory.Collector{
		SoftwareInventoryCollector{},
		EndpointInventoryCollector{},
		ServiceInventoryCollector{},
	}
}

// sortItems orders items by name so pagination cursors are stable.
func sortItems(items []inventory.Item) {
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
}
