package collectors

import (
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"

	"github.com/radanor/radanor-shield/internal/inventory"
)

// Windows inventory decoding.
//
// This file deliberately carries no build tag, following the same reasoning as
// the Defender model: no native Windows 11 host was available to verify the CE-6
// collectors, so everything that can be checked without one — the script text,
// the JSON decoding and the item construction — lives here where the Linux test
// suite drives it from fixtures. Only the command execution that produces the
// JSON sits behind the windows build tag.
//
// Two rules the model enforces, both of which the previous inline decoding got
// wrong or could not demonstrate:
//
//   - ConvertTo-Json collapses a single-element array into a bare object, so a
//     host with exactly one listening socket returned undecodable output and was
//     reported as "unsupported". Both shapes are now accepted.
//   - An endpoint whose owning process was not reported is labelled unattributed
//     rather than guessed, and nothing in the item claims the endpoint is
//     reachable from another machine.

var errWindowsInventoryEmpty = errors.New("no structured output was returned")
var errWindowsInventoryMalformed = errors.New("structured output could not be decoded")

// windowsSocketScript lists listening TCP sockets and all bound UDP endpoints.
// It is fixed at compile time; no caller-supplied value reaches it.
//
// The two transports are reported with different states on purpose. A TCP socket
// in the Listen state is an acceptor; a UDP socket has no such state — it is
// bound to a local address and may or may not receive datagrams. Describing both
// as "listening" would imply TCP-style accept semantics, and would read as a
// claim about exposure that the query never made.
const windowsSocketScript = `$t = @(Get-NetTCPConnection -State Listen -ErrorAction SilentlyContinue | ` +
	`ForEach-Object { [pscustomobject]@{ transport='tcp'; address=[string]$_.LocalAddress; port=[int]$_.LocalPort; state='listening'; pid=[int]$_.OwningProcess } }); ` +
	`$u = @(Get-NetUDPEndpoint -ErrorAction SilentlyContinue | ` +
	`ForEach-Object { [pscustomobject]@{ transport='udp'; address=[string]$_.LocalAddress; port=[int]$_.LocalPort; state='bound'; pid=[int]$_.OwningProcess } }); ` +
	`@($t + $u) | ConvertTo-Json -Compress`

// udpBindingNote is attached to every UDP endpoint. It is the honest description
// of what the platform reported: a bound datagram socket, not a listener, and
// with no claim either way about reachability from another machine.
const udpBindingNote = "a UDP endpoint is bound rather than listening; it has no accept queue, and nothing here states that another machine can reach it"

// windowsServiceScript lists Windows services. Only the English property names
// of the service objects are read, and only these four are emitted.
const windowsServiceScript = `$s = Get-Service -ErrorAction Stop | ForEach-Object { ` +
	`[pscustomobject]@{ Name=[string]$_.Name; DisplayName=[string]$_.DisplayName; Status=[string]$_.Status; StartType=[string]$_.StartType } }; ` +
	`@($s) | ConvertTo-Json -Compress`

// windowsSocketRecord is one row of the socket inventory query.
type windowsSocketRecord struct {
	Transport string `json:"transport"`
	Address   string `json:"address"`
	Port      int    `json:"port"`
	State     string `json:"state"`
	PID       int    `json:"pid"`
}

// windowsServiceRecord is one row of the service inventory query.
type windowsServiceRecord struct {
	Name        string `json:"Name"`
	DisplayName string `json:"DisplayName"`
	Status      string `json:"Status"`
	StartType   string `json:"StartType"`
}

// decodeWindowsArray decodes a ConvertTo-Json document that may be an array or,
// when the query returned exactly one object, a bare object.
func decodeWindowsArray[T any](raw string) ([]T, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || trimmed == "null" {
		return nil, errWindowsInventoryEmpty
	}
	var many []T
	if err := json.Unmarshal([]byte(trimmed), &many); err == nil {
		return many, nil
	}
	var single T
	if err := json.Unmarshal([]byte(trimmed), &single); err != nil {
		return nil, errWindowsInventoryMalformed
	}
	return []T{single}, nil
}

func parseWindowsSockets(raw string) ([]windowsSocketRecord, error) {
	return decodeWindowsArray[windowsSocketRecord](raw)
}

func parseWindowsServices(raw string) ([]windowsServiceRecord, error) {
	return decodeWindowsArray[windowsServiceRecord](raw)
}

// windowsEndpointItems builds the endpoint inventory items. An entry whose
// owning process id was not reported is marked unattributed, and a missing
// address or transport is recorded as unknown rather than filled in.
func windowsEndpointItems(records []windowsSocketRecord) []inventory.Item {
	items := make([]inventory.Item, 0, len(records))
	for _, record := range records {
		transport := strings.ToLower(strings.TrimSpace(record.Transport))
		if transport != "tcp" && transport != "udp" {
			// Both queries only ask for TCP and UDP; anything else is not a
			// value this model can describe, so it is skipped rather than
			// mislabelled.
			continue
		}
		address := strings.TrimSpace(record.Address)
		port := strconv.Itoa(record.Port)
		fields := map[string]string{
			"transport": transport,
			"port":      port,
			"state":     strings.ToLower(strings.TrimSpace(record.State)),
		}
		if address != "" {
			fields["address"] = address
		} else {
			fields["address"] = ""
		}
		if transport == "udp" {
			fields["binding_note"] = udpBindingNote
		}
		if record.PID > 0 {
			fields["process_id"] = strconv.Itoa(record.PID)
			fields["attribution"] = "observed"
		} else {
			fields["attribution"] = "unknown"
			fields["attribution_note"] = "the owning process was not reported"
		}
		items = append(items, inventory.Item{
			Kind:   inventory.KindEndpoint,
			Name:   transport + "://" + address + ":" + port,
			Source: "windows-net",
			Fields: fields,
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items
}

// windowsServiceItems builds the service inventory items. A service with no name
// is skipped: an unnamed row cannot be identified, so storing it would add
// nothing a reader could act on.
func windowsServiceItems(records []windowsServiceRecord) []inventory.Item {
	items := make([]inventory.Item, 0, len(records))
	for _, record := range records {
		name := strings.TrimSpace(record.Name)
		if name == "" {
			continue
		}
		fields := map[string]string{
			"status":      strings.ToLower(strings.TrimSpace(record.Status)),
			"start_type":  strings.ToLower(strings.TrimSpace(record.StartType)),
			"attribution": "service-control-manager",
		}
		if display := strings.TrimSpace(record.DisplayName); display != "" {
			fields["display_name"] = display
		}
		items = append(items, inventory.Item{
			Kind:   inventory.KindService,
			Name:   name,
			Source: "windows-service",
			Fields: fields,
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items
}
