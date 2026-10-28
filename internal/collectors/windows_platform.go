//go:build windows

package collectors

import (
	"context"
	"runtime"
	"strconv"
	"strings"

	"github.com/radanor/radanor-shield/internal/assessment"
	"golang.org/x/sys/windows/registry"
)

// windowsPlatformID identifies the platform string stamped on Windows
// observations.
const windowsPlatformID = "windows"

// regString reads a string value from the registry. It never fails the
// collector: an absent value is reported as not present.
func regString(root registry.Key, path, name string) (string, bool) {
	k, err := registry.OpenKey(root, path, registry.QUERY_VALUE)
	if err != nil {
		return "", false
	}
	defer k.Close()
	value, _, err := k.GetStringValue(name)
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(value), true
}

// regDWORD reads a DWORD value from the registry.
func regDWORD(root registry.Key, path, name string) (int64, bool) {
	k, err := registry.OpenKey(root, path, registry.QUERY_VALUE)
	if err != nil {
		return 0, false
	}
	defer k.Close()
	value, _, err := k.GetIntegerValue(name)
	if err != nil {
		return 0, false
	}
	return int64(value), true
}

// WindowsPlatformCollector reports the Windows edition, version, build and
// architecture.
//
// Source: read-only Win32_OperatingSystem CIM identity, CurrentVersion registry
// evidence and the Go runtime architecture. ProductName may retain old branding.
type WindowsPlatformCollector struct{}

func (WindowsPlatformCollector) ID() string { return "platform" }

func (WindowsPlatformCollector) Collect(ctx context.Context) ([]assessment.Observation, error) {
	obs := assessment.Observation{
		ID: "platform", Collector: "platform", Platform: windowsPlatformID,
		Status: assessment.Collected, Source: `Win32_OperatingSystem; HKLM\SOFTWARE\Microsoft\Windows NT\CurrentVersion`,
	}
	const path = `SOFTWARE\Microsoft\Windows NT\CurrentVersion`
	product, okProduct := regString(registry.LOCAL_MACHINE, path, "ProductName")
	build, okBuild := regString(registry.LOCAL_MACHINE, path, "CurrentBuildNumber")
	edition, _ := regString(registry.LOCAL_MACHINE, path, "EditionID")
	installation, _ := regString(registry.LOCAL_MACHINE, path, "InstallationType")
	display, okDisplay := regString(registry.LOCAL_MACHINE, path, "DisplayVersion")
	var cim windowsPlatformCIM
	cimErr := windowsPowerShellJSON(ctx, `$ErrorActionPreference='Stop'; $o=Get-CimInstance -ClassName Win32_OperatingSystem; [pscustomobject]@{Caption=[string]$o.Caption; BuildNumber=[string]$o.BuildNumber; ProductType=[int]$o.ProductType; OperatingSystemSKU=[int]$o.OperatingSystemSKU; OSArchitecture=[string]$o.OSArchitecture} | ConvertTo-Json -Compress`, &cim)
	if cimErr != nil {
		cim = windowsPlatformCIM{}
	}
	if !okProduct && !okBuild && cim.Caption == "" && cim.BuildNumber == "" {
		obs.Status = assessment.Unsupported
		obs.Error = "Windows registry and CIM identity are unavailable"
		return []assessment.Observation{obs}, nil
	}
	if !okDisplay {
		// Older builds use ReleaseId instead of DisplayVersion.
		if legacy, ok := regString(registry.LOCAL_MACHINE, path, "ReleaseId"); ok {
			display, okDisplay = legacy, true
		}
	}
	name, complete, reason := windowsPlatformName(product, build, installation, cim)
	if !complete {
		obs.Status = assessment.Incomplete
		obs.Error = reason
	}
	obs.Values = append(obs.Values,
		presentValue("product_name", name),
		presentValue("registry_product_name", product),
		presentValue("registry_build", build),
		presentValue("installation_type", installation),
		presentValue("cim_caption", cim.Caption),
		presentValue("cim_build", cim.BuildNumber),
		presentValue("os_architecture", cim.OSArchitecture),
		presentValue("edition_id", edition),
		presentValue("build", build),
		presentValue("display_version", display),
		presentValue("arch", runtime.GOARCH),
	)
	if cimErr == nil {
		obs.Values = append(obs.Values, presentInt("product_type", int64(cim.ProductType)), presentInt("operating_system_sku", int64(cim.OperatingSystemSKU)))
	} else {
		obs.Values = append(obs.Values, presentValue("cim_identity_status", "unavailable"))
	}
	if build == "" && cim.BuildNumber != "" {
		for i := range obs.Values {
			if obs.Values[i].Key == "build" {
				obs.Values[i] = presentValue("build", cim.BuildNumber)
			}
		}
	}
	return []assessment.Observation{obs}, nil
}

type windowsPlatformCIM struct {
	Caption, BuildNumber, OSArchitecture string
	ProductType, OperatingSystemSKU      int
}

// Aligns with Get-WindowsPlatformClassification: 10240..19045 is Windows 10,
// 22000+ is Windows 11 only for clients; the intervening band is unclassified.
// Unlike the harness, contradictory authoritative identity is marked incomplete.
func windowsPlatformName(product, registryBuild, installation string, cim windowsPlatformCIM) (string, bool, string) {
	caption := windowsIdentityCaption(cim.Caption)
	build, err := strconv.Atoi(strings.TrimSpace(registryBuild))
	if err != nil || build <= 0 {
		build, _ = strconv.Atoi(strings.TrimSpace(cim.BuildNumber))
	}
	cimBuild, _ := strconv.Atoi(strings.TrimSpace(cim.BuildNumber))
	if build > 0 && cimBuild > 0 && build != cimBuild {
		return "Windows", false, "registry and CIM build numbers disagree"
	}
	serverBrand := windowsServerCaption(windowsIdentityCaption(product)) || windowsServerCaption(caption)
	serverInstall := strings.HasPrefix(strings.ToLower(installation), "server")
	if cim.ProductType < 0 || cim.ProductType > 3 {
		return "Windows", false, "invalid CIM product type"
	}
	client := cim.ProductType == 1 || cim.ProductType == 0 && strings.EqualFold(installation, "Client")
	server := cim.ProductType == 2 || cim.ProductType == 3 || serverInstall || serverBrand
	if client && server || (cim.ProductType == 2 || cim.ProductType == 3) && strings.EqualFold(installation, "Client") {
		return "Windows", false, "client and server identity evidence conflicts"
	}
	if server {
		if windowsServerCaption(caption) {
			return caption, true, ""
		}
		if windowsClientFamily(caption) != "" {
			return "Windows Server", false, "client caption conflicts with server product type"
		}
		if strings.TrimSpace(cim.Caption) != "" {
			return "Windows Server", false, "CIM caption does not establish a recognized Windows Server identity"
		}
		if windowsServerCaption(windowsIdentityCaption(product)) {
			return product, true, ""
		}
		return "Windows Server", true, ""
	}
	if !client {
		return "Windows", false, "client product classification is unavailable"
	}
	family := ""
	switch {
	case build >= 22000:
		family = "Windows 11"
	case build >= 10240 && build <= 19045:
		family = "Windows 10"
	}
	if family == "" {
		return "Windows", false, "build does not establish a released Windows client family"
	}
	productDisplay := windowsIdentityCaption(product)
	productFamily := windowsClientFamily(productDisplay)
	if productFamily != "" && productFamily != family && !(family == "Windows 11" && productFamily == "Windows 10") {
		return family, false, "registry branding conflicts with authoritative client build"
	}
	if windowsClientFamily(caption) == family {
		return caption, true, ""
	}
	if windowsClientFamily(caption) != "" {
		return family, false, "caption conflicts with authoritative client build"
	}
	if strings.TrimSpace(cim.Caption) != "" {
		return family, false, "CIM caption does not establish a recognized Windows client identity"
	}
	if productFamily == family {
		return productDisplay, true, ""
	}
	if family == "Windows 11" && productFamily == "Windows 10" {
		return strings.TrimSpace(family + " " + strings.Join(strings.Fields(productDisplay)[2:], " ")), true, ""
	}
	return family, true, ""
}

func windowsClientFamily(name string) string {
	words := strings.Fields(name)
	if len(words) >= 2 && strings.EqualFold(words[0], "Windows") && (words[1] == "10" || words[1] == "11") {
		return "Windows " + words[1]
	}
	return ""
}

// Brand/family tokens remain invariant; localized edition suffixes are retained.
// Recognizing whole tokens avoids treating Windows 110 or Serverish as identities.
func windowsServerCaption(name string) bool {
	words := strings.Fields(name)
	return len(words) >= 2 && strings.EqualFold(words[0], "Windows") && strings.EqualFold(words[1], "Server")
}

func windowsIdentityCaption(name string) string {
	words := strings.Fields(name)
	if len(words) > 0 && strings.EqualFold(words[0], "Microsoft") {
		words = words[1:]
	}
	return strings.Join(words, " ")
}

// WindowsSecurityConfigCollector reports a reviewed set of local security
// configuration facts read from the registry.
//
// Source: documented policy and LSA registry locations. Each value is recorded
// factually; the corresponding check decides which combinations are a weakness.
type WindowsSecurityConfigCollector struct{}

func (WindowsSecurityConfigCollector) ID() string { return "windows_security_config" }

func (WindowsSecurityConfigCollector) Collect(ctx context.Context) ([]assessment.Observation, error) {
	obs := assessment.Observation{
		ID: "windows_security_config", Collector: "windows_security_config", Platform: windowsPlatformID,
		Status: assessment.Collected, Source: "local security policy registry",
	}

	// UAC: EnableLUA=1 keeps elevation prompts enabled.
	if enableLUA, ok := regDWORD(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows\CurrentVersion\Policies\System`, "EnableLUA"); ok {
		obs.Values = append(obs.Values, presentBool("uac_enabled", enableLUA == 1))
	}
	// LSA protection: RunAsPPL=1 enables LSASS as a protected process.
	if ppl, ok := regDWORD(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\Lsa`, "RunAsPPL"); ok {
		obs.Values = append(obs.Values, presentBool("lsa_protection_enabled", ppl == 1))
	}
	// Credential Guard: LsaCfgFlags>=1 enables it (1 = user mode, 2 = secure mode).
	if vbs, ok := regDWORD(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\DeviceGuard`, "EnableVirtualizationBasedSecurity"); ok {
		obs.Values = append(obs.Values, presentBool("vbs_enabled", vbs == 1))
	}
	if cfg, ok := regDWORD(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\Lsa`, "LsaCfgFlags"); ok {
		obs.Values = append(obs.Values, presentBool("credential_guard_enabled", cfg >= 1))
	}
	// SMBv1: the SMB1 server driver start value; 4 (disabled) or absent is good.
	if smb1, ok := regDWORD(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Services\LanmanServer\Parameters`, "SMB1"); ok {
		obs.Values = append(obs.Values, presentBool("smb1_server_disabled", smb1 == 0))
	}
	// PowerShell script block logging (policy), 1 enables it.
	if logging, ok := regDWORD(registry.LOCAL_MACHINE, `SOFTWARE\Policies\Microsoft\Windows\PowerShell\ScriptBlockLogging`, "EnableScriptBlockLogging"); ok {
		obs.Values = append(obs.Values, presentBool("script_block_logging_enabled", logging == 1))
	} else {
		obs.Values = append(obs.Values, presentBool("script_block_logging_enabled", false))
	}
	if len(obs.Values) == 0 {
		obs.Status = assessment.Unsupported
		obs.Error = "no security configuration registry values are readable"
	}
	return []assessment.Observation{obs}, nil
}

// regDWORDString formats an integer evidence value from a registry read for
// evidence text. Kept separate so callers do not accidentally print a raw DWORD
// as a locale-formatted number.
func regDWORDString(v int64) string { return strconv.FormatInt(v, 10) }
