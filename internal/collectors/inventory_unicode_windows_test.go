//go:build windows

package collectors

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/radanor/radanor-shield/internal/inventory"
	"golang.org/x/sys/windows/registry"
)

// Read-only probe: never creates fixtures in real uninstall keys. A SKIP leaves
// W-INV-UNI outstanding; synthetic Unicode does not substitute for host evidence.
func TestWindowsSoftwareInventoryNativeUnicode(t *testing.T) {
	want := map[string]bool{}
	seen := map[string]bool{}
	for _, root := range uninstallKeys {
		hive, ok := registryHive(root.root)
		if !ok {
			continue
		}
		key, err := registry.OpenKey(hive, root.path, registry.READ)
		if err != nil {
			continue
		}
		entries, err := key.ReadSubKeyNames(-1)
		key.Close()
		if err != nil {
			continue
		}
		for _, entry := range entries {
			key, err := registry.OpenKey(hive, root.path+`\`+entry, registry.QUERY_VALUE)
			if err != nil {
				continue
			}
			name, _, err := key.GetStringValue("DisplayName")
			version, _, _ := key.GetStringValue("DisplayVersion")
			key.Close()
			if err != nil {
				continue
			}
			name = strings.TrimSpace(name)
			version = strings.TrimSpace(version)
			identity := strings.ToLower(name) + "\x00" + strings.ToLower(version)
			if name == "" || seen[identity] {
				continue
			}
			seen[identity] = true
			if !utf8.ValidString(name) {
				t.Fatal("native registry DisplayName contains invalid UTF-8")
			}
			for _, r := range name {
				if r > 127 {
					want[name+"\x00"+version] = true
					break
				}
			}
		}
	}
	if len(want) == 0 {
		t.Skip("W-INV-UNI NOT RUN: no readable non-ASCII uninstall application name exists on this host")
	}
	collection := (SoftwareInventoryCollector{}).CollectInventory(context.Background())
	if collection.Status != inventory.Collected && collection.Status != inventory.Incomplete {
		t.Fatalf("native collection unavailable: %s", collection.Status)
	}
	for _, item := range collection.Items {
		delete(want, item.Name+"\x00"+item.Version)
	}
	if len(want) != 0 {
		t.Fatalf("native Unicode name/version identities lost: %d", len(want))
	}
	t.Log("NATIVE registry-to-collector Unicode comparison passed; this probe does not exercise API retrieval")
}
