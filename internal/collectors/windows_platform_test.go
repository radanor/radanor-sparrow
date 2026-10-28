//go:build windows

package collectors

import (
	"context"
	"runtime"
	"testing"

	"golang.org/x/sys/windows/registry"
)

func TestWindowsPlatformName(t *testing.T) {
	tests := []struct {
		name, product, build, installation string
		cim                                windowsPlatformCIM
		want                               string
		complete                           bool
	}{
		{"Windows 10", "Windows 10 Pro", "19045", "Client", windowsPlatformCIM{Caption: "Microsoft Windows 10 Pro", BuildNumber: "19045", ProductType: 1}, "Windows 10 Pro", true},
		{"Windows 11 RTM", "Windows 10 Pro", "22000", "Client", windowsPlatformCIM{Caption: "Microsoft Windows 11 Pro", BuildNumber: "22000", ProductType: 1}, "Windows 11 Pro", true},
		{"25H2 legacy branding", "Windows 10 Home Single Language", "26200", "Client", windowsPlatformCIM{Caption: "Microsoft Windows 11 Home Single Language", BuildNumber: "26200", ProductType: 1, OperatingSystemSKU: 100, OSArchitecture: "64-bit"}, "Windows 11 Home Single Language", true},
		{"legacy caption absent", "Windows 10 Home Single Language", "26200", "Client", windowsPlatformCIM{ProductType: 1}, "Windows 11 Home Single Language", true},
		{"CIM unavailable", "Windows 10 Home Single Language", "26200", "Client", windowsPlatformCIM{}, "Windows 11 Home Single Language", true},
		{"Server 2022", "Windows Server 2022 Standard", "20348", "Server", windowsPlatformCIM{Caption: "Microsoft Windows Server 2022 Standard", BuildNumber: "20348", ProductType: 3}, "Windows Server 2022 Standard", true},
		{"Server 2025", "Windows Server 2025 Datacenter", "26100", "Server Core", windowsPlatformCIM{Caption: "Microsoft Windows Server 2025 Datacenter", BuildNumber: "26100", ProductType: 3}, "Windows Server 2025 Datacenter", true},
		{"domain controller", "", "26100", "", windowsPlatformCIM{ProductType: 2}, "Windows Server", true},
		{"server without caption", "Windows Server 2025 Standard", "26100", "Server", windowsPlatformCIM{}, "Windows Server 2025 Standard", true},
		{"registry unavailable", "", "", "", windowsPlatformCIM{Caption: "Microsoft Windows 11 Pro", BuildNumber: "26200", ProductType: 1}, "Windows 11 Pro", true},
		{"invalid registry build", "Windows 10 Pro", "bad", "Client", windowsPlatformCIM{BuildNumber: "22000", ProductType: 1}, "Windows 11 Pro", true},
		{"missing build", "Windows 10 Pro", "", "Client", windowsPlatformCIM{Caption: "Microsoft Windows 11 Pro", ProductType: 1}, "Windows", false},
		{"zero build", "Windows 11 Pro", "0", "Client", windowsPlatformCIM{}, "Windows", false},
		{"negative build", "Windows 11 Pro", "-1", "Client", windowsPlatformCIM{}, "Windows", false},
		{"pre-release gap", "Windows 10 Pro", "20000", "Client", windowsPlatformCIM{ProductType: 1}, "Windows", false},
		{"older build", "Windows 8.1 Pro", "9600", "Client", windowsPlatformCIM{ProductType: 1}, "Windows", false},
		{"build mismatch", "Windows 10 Pro", "19045", "Client", windowsPlatformCIM{BuildNumber: "26200", ProductType: 1}, "Windows", false},
		{"caption mismatch", "Windows 10 Pro", "19045", "Client", windowsPlatformCIM{Caption: "Microsoft Windows 11 Pro", ProductType: 1}, "Windows 10", false},
		{"stale CIM caption", "Windows 10 Pro", "26200", "Client", windowsPlatformCIM{Caption: "Microsoft Windows 10 Pro", ProductType: 1}, "Windows 11", false},
		{"client server conflict", "Windows Server 2025", "26100", "Client", windowsPlatformCIM{ProductType: 1}, "Windows", false},
		{"server client install conflict", "", "26100", "Client", windowsPlatformCIM{ProductType: 3}, "Windows", false},
		{"server client caption", "", "26100", "Server", windowsPlatformCIM{Caption: "Microsoft Windows 11 Pro", ProductType: 3}, "Windows Server", false},
		{"no product classification", "Windows 10 Pro", "26200", "", windowsPlatformCIM{}, "Windows", false},
		{"invalid product type", "Windows 10 Pro", "26200", "Client", windowsPlatformCIM{ProductType: 4}, "Windows", false},
		{"reverse branding conflict", "Windows 11 Pro", "19045", "Client", windowsPlatformCIM{ProductType: 1}, "Windows 10", false},
		{"invalid server product type", "Windows Server 2025", "26100", "Server", windowsPlatformCIM{ProductType: 4}, "Windows", false},
		{"all missing", "", "", "", windowsPlatformCIM{}, "Windows", false},
		{"localized caption", "Windows 10 Pro", "26200", "Client", windowsPlatformCIM{Caption: "Microsoft Windows 11 Professionnel", ProductType: 1}, "Windows 11 Professionnel", true},
		{"Chinese edition", "Windows 10 Home", "26200", "Client", windowsPlatformCIM{Caption: "Microsoft Windows 11 家庭中文版", ProductType: 1}, "Windows 11 家庭中文版", true},
		{"Unicode whitespace", "Windows 10 Pro", "26200", "Client", windowsPlatformCIM{Caption: "Microsoft\u00a0Windows\u00a011\u00a0Professionnel", ProductType: 1}, "Windows 11 Professionnel", true},
		{"case insensitive caption", "Windows 10 Pro", "26200", "Client", windowsPlatformCIM{Caption: "microsoft windows 11 Professionnel", ProductType: 1}, "windows 11 Professionnel", true},
		{"localized Server", "Windows Server 2025 Standard", "26100", "Server", windowsPlatformCIM{Caption: "Microsoft Windows Server 2025 Standard (édition française)", ProductType: 3}, "Windows Server 2025 Standard (édition française)", true},
		{"unknown client caption", "Windows 10 Pro", "26200", "Client", windowsPlatformCIM{Caption: "Ubuntu", ProductType: 1}, "Windows 11", false},
		{"arbitrary client caption", "Windows 10 Pro", "26200", "Client", windowsPlatformCIM{Caption: "Custom operating system", ProductType: 1}, "Windows 11", false},
		{"future caption", "Windows 10 Pro", "26200", "Client", windowsPlatformCIM{Caption: "Microsoft Windows 12 Pro", ProductType: 1}, "Windows 11", false},
		{"older contradictory caption", "Windows 10 Pro", "26200", "Client", windowsPlatformCIM{Caption: "Microsoft Windows 8.1 Pro", ProductType: 1}, "Windows 11", false},
		{"version token boundary", "Windows 10 Pro", "26200", "Client", windowsPlatformCIM{Caption: "Microsoft Windows 110 Pro", ProductType: 1}, "Windows 11", false},
		{"version token glued", "Windows 10 Pro", "26200", "Client", windowsPlatformCIM{Caption: "Microsoft Windows 11Pro", ProductType: 1}, "Windows 11", false},
		{"embedded client branding", "Windows 10 Pro", "26200", "Client", windowsPlatformCIM{Caption: "Not Windows 11 Pro", ProductType: 1}, "Windows 11", false},
		{"lowercase contradictory family", "Windows 10 Pro", "26200", "Client", windowsPlatformCIM{Caption: "microsoft windows 10 Pro", ProductType: 1}, "Windows 11", false},
		{"unknown Server caption", "Windows Server 2025 Standard", "26100", "Server", windowsPlatformCIM{Caption: "Custom operating system", ProductType: 3}, "Windows Server", false},
		{"future client caption on Server", "Windows Server 2025 Standard", "26100", "Server", windowsPlatformCIM{Caption: "Microsoft Windows 12 Pro", ProductType: 3}, "Windows Server", false},
		{"Server token boundary", "Windows Server 2025 Standard", "26100", "Server", windowsPlatformCIM{Caption: "Microsoft Windows Serverish", ProductType: 3}, "Windows Server", false},
		{"embedded Server branding", "Windows Server 2025 Standard", "26100", "Server", windowsPlatformCIM{Caption: "Not Windows Server 2025", ProductType: 3}, "Windows Server", false},
		{"lowercase client caption on Server", "Windows Server 2025 Standard", "26100", "Server", windowsPlatformCIM{Caption: "microsoft windows 11 Pro", ProductType: 3}, "Windows Server", false},
		{"Server caption on client", "Windows 10 Pro", "26200", "Client", windowsPlatformCIM{Caption: "Microsoft Windows Server 2025", ProductType: 1}, "Windows", false},
		{"missing CIM and classification", "Windows 10 Pro", "26200", "", windowsPlatformCIM{}, "Windows", false},
		{"caption alone is insufficient", "", "26200", "", windowsPlatformCIM{Caption: "Microsoft Windows 11 Pro"}, "Windows", false},
		{"whitespace caption absent", "Windows 10 Pro", "26200", "Client", windowsPlatformCIM{Caption: " \t\u00a0 "}, "Windows 11 Pro", true},
		{"missing CIM on Server Core", "", "26100", "Server Core", windowsPlatformCIM{}, "Windows Server", true},
		{"localized unknown caption", "Windows 10 Pro", "26200", "Client", windowsPlatformCIM{Caption: "Système inconnu", ProductType: 1}, "Windows 11", false},
		{"vendor-only caption", "Windows 10 Pro", "26200", "Client", windowsPlatformCIM{Caption: "Microsoft", ProductType: 1}, "Windows 11", false},
		{"vendor-only Server caption", "Windows Server 2025 Standard", "26100", "Server", windowsPlatformCIM{Caption: "Microsoft", ProductType: 3}, "Windows Server", false},
		{"lowercase legacy registry", "windows 10 Professionnel", "26200", "Client", windowsPlatformCIM{}, "Windows 11 Professionnel", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, complete, reason := windowsPlatformName(tt.product, tt.build, tt.installation, tt.cim)
			if got != tt.want || complete != tt.complete {
				t.Fatalf("got %q complete=%v reason=%q; want %q complete=%v", got, complete, reason, tt.want, tt.complete)
			}
			if !complete && reason == "" {
				t.Fatal("incomplete evidence lacks reason")
			}
		})
	}
}

func TestWindowsPlatformCollectorPreservesRawEvidence(t *testing.T) {
	raw, ok := regString(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows NT\CurrentVersion`, "ProductName")
	if !ok {
		t.Skip("registry ProductName unavailable on this host")
	}
	observations, err := (WindowsPlatformCollector{}).Collect(context.Background())
	if err != nil || len(observations) != 1 {
		t.Fatalf("collection: %v", err)
	}
	values := map[string]string{}
	for _, value := range observations[0].Values {
		values[value.Key] = value.Text
	}
	if values["registry_product_name"] != raw {
		t.Fatalf("raw registry evidence changed: %q", values["registry_product_name"])
	}
	if values["product_name"] == "" || values["arch"] != runtime.GOARCH {
		t.Fatal("existing display keys missing")
	}
}
