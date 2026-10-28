//go:build windows

package collectors

import (
	"context"
	"sort"
	"strings"

	"github.com/radanor/radanor-shield/internal/assessment"
	"golang.org/x/sys/windows/registry"
)

// maxInventorySamples bounds how many names are echoed into evidence. Counts
// stay exact.
const maxInventorySamples = 25

// WindowsApplicationsCollector inventories installed applications.
//
// Source: the documented Uninstall registry keys, read in both the 64-bit and
// 32-bit registry views so WOW64 applications are not missed. This is an
// inventory, not a vulnerability list: no version is matched against any
// advisory database, because doing so offline would misrepresent vendors that
// backport fixes without changing the version string.
type WindowsApplicationsCollector struct{}

func (WindowsApplicationsCollector) ID() string { return "windows_applications" }

// registryHive maps a reviewed root name onto the registry hive handle. An
// unrecognised root is refused rather than defaulted, so a typo in the reviewed
// list cannot silently read the wrong hive. The list itself lives in
// windows_uninstall_keys.go, which carries no build tag so it can be reviewed
// and tested on any host.
func registryHive(root string) (registry.Key, bool) {
	switch root {
	case registryHiveLocalMachine:
		return registry.LOCAL_MACHINE, true
	case registryHiveCurrentUser:
		return registry.CURRENT_USER, true
	default:
		return 0, false
	}
}

func (WindowsApplicationsCollector) Collect(ctx context.Context) ([]assessment.Observation, error) {
	obs := assessment.Observation{
		ID: "windows_applications", Collector: "windows_applications", Platform: windowsPlatformID,
		Status: assessment.Collected, Source: "Uninstall registry keys",
	}
	seen := map[string]bool{}
	var names []string
	for _, key := range uninstallKeys {
		hive, ok := registryHive(key.root)
		if !ok {
			continue
		}
		k, err := registry.OpenKey(hive, key.path, registry.READ)
		if err != nil {
			continue
		}
		subkeys, err := k.ReadSubKeyNames(-1)
		k.Close()
		if err != nil {
			continue
		}
		for _, sub := range subkeys {
			display, ok := regString(hive, key.path+`\`+sub, "DisplayName")
			if !ok || display == "" || seen[display] {
				continue
			}
			seen[display] = true
			names = append(names, display)
		}
	}
	if len(names) == 0 {
		obs.Status = assessment.Incomplete
		obs.Error = "the uninstall registry keys returned no applications"
		return []assessment.Observation{obs}, nil
	}
	sort.Strings(names)
	shown := names
	if len(shown) > maxInventorySamples {
		shown = shown[:maxInventorySamples]
	}
	obs.Values = append(obs.Values,
		presentInt("installed_applications", int64(len(names))),
		presentValue("sample_applications", strings.Join(shown, ", ")),
	)
	return []assessment.Observation{obs}, nil
}

// WindowsServicesCollector reports the state of an allowlisted set of
// security-relevant services plus an aggregate count.
//
// Source: Get-Service through PowerShell JSON. Only English property names
// (Name, Status, StartType) are read.
type WindowsServicesCollector struct{}

func (WindowsServicesCollector) ID() string { return "windows_services" }

// The reviewed allowlist and the dedup helpers live in
// windows_service_names.go, which has no build tag, so a case-insensitive
// duplicate of a service name is caught by the test suite on every platform.

func (WindowsServicesCollector) Collect(ctx context.Context) ([]assessment.Observation, error) {
	obs := assessment.Observation{
		ID: "windows_services", Collector: "windows_services", Platform: windowsPlatformID,
		Status: assessment.Collected, Source: "Get-Service (CIM)",
	}
	// Each name is queried once. Get-Service resolves names case-insensitively,
	// so a duplicated list entry would return the same service twice.
	script := `$names = ` + serviceNamesForQuery(windowsSecurityServices) + `; ` +
		`$out = foreach ($n in $names) { $s = Get-Service -Name $n -ErrorAction SilentlyContinue; ` +
		`if ($null -ne $s) { [pscustomobject]@{ Name=[string]$s.Name; Status=[string]$s.Status; StartType=[string]$s.StartType } } }; ` +
		`@($out) | ConvertTo-Json -Compress`
	var services []struct {
		Name      string `json:"Name"`
		Status    string `json:"Status"`
		StartType string `json:"StartType"`
	}
	if err := windowsPowerShellJSON(ctx, script, &services); err != nil {
		obs.Status = assessment.Unsupported
		obs.Error = "the service inventory is not available on this system"
		return []assessment.Observation{obs}, nil
	}
	// A service is counted and recorded once even if the query returns it more
	// than once (a case-variant, or two aliases of the same service).
	seen := map[string]bool{}
	present := 0
	for _, s := range services {
		key := strings.ToLower(strings.TrimSpace(s.Name))
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		present++
		obs.Values = append(obs.Values,
			presentValue("service."+s.Name+".status", strings.ToLower(s.Status)),
			presentValue("service."+s.Name+".start", strings.ToLower(s.StartType)),
		)
	}
	for _, name := range uniqueServiceNames(windowsSecurityServices) {
		if _, ok := stringValue(obs, "service."+name+".status"); !ok {
			obs.Values = append(obs.Values, presentValue("service."+name+".status", "absent"))
		}
	}
	obs.Values = append(obs.Values, presentInt("services_present", int64(present)))
	return []assessment.Observation{obs}, nil
}

// WindowsNetworkCollector reports listening TCP and UDP endpoints.
//
// Source: Get-NetTCPConnection -State Listen and Get-NetUDPEndpoint JSON.
// Only aggregate counts and a bounded sample of local addresses are stored.
type WindowsNetworkCollector struct{}

func (WindowsNetworkCollector) ID() string { return "windows_network_endpoints" }

func (WindowsNetworkCollector) Collect(ctx context.Context) ([]assessment.Observation, error) {
	obs := assessment.Observation{
		ID: "windows_network_endpoints", Collector: "windows_network_endpoints", Platform: windowsPlatformID,
		Status: assessment.Collected, Source: "Get-NetTCPConnection / Get-NetUDPEndpoint",
	}
	const script = `$t = @(Get-NetTCPConnection -State Listen -ErrorAction SilentlyContinue); ` +
		`$u = @(Get-NetUDPEndpoint -ErrorAction SilentlyContinue); ` +
		`[pscustomobject]@{ tcp=@($t | ForEach-Object { [string]$_.LocalAddress + ':' + [string]$_.LocalPort }); ` +
		`udp=@($u | ForEach-Object { [string]$_.LocalAddress + ':' + [string]$_.LocalPort }) } | ConvertTo-Json -Compress -Depth 4`
	var result struct {
		TCP []string `json:"tcp"`
		UDP []string `json:"udp"`
	}
	if err := windowsPowerShellJSON(ctx, script, &result); err != nil {
		obs.Status = assessment.Unsupported
		obs.Error = "the socket inventory is not available on this system"
		return []assessment.Observation{obs}, nil
	}
	loopback, wildcard := 0, 0
	var samples []string
	for _, addr := range append(append([]string{}, result.TCP...), result.UDP...) {
		host := addr
		if idx := strings.LastIndex(addr, ":"); idx > 0 {
			host = addr[:idx]
		}
		switch {
		case host == "127.0.0.1" || host == "::1":
			loopback++
		case host == "0.0.0.0" || host == "::":
			wildcard++
		}
		if len(samples) < maxEndpointSamples {
			samples = append(samples, addr)
		}
	}
	obs.Values = append(obs.Values,
		presentInt("tcp_listeners", int64(len(result.TCP))),
		presentInt("udp_listeners", int64(len(result.UDP))),
		presentInt("loopback_bound", int64(loopback)),
		presentInt("wildcard_bound", int64(wildcard)),
		presentValue("sample_addresses", strings.Join(samples, ", ")),
	)
	return []assessment.Observation{obs}, nil
}
