//go:build !linux && !windows

package collectors

import "github.com/radanor/radanor-shield/internal/inventory"

// PlatformInventoryCollectors returns no collectors on platforms CE-6 does not
// target. The Software and Network & Services pages then report an empty
// inventory rather than inventing readings.
func PlatformInventoryCollectors() []inventory.Collector { return nil }
