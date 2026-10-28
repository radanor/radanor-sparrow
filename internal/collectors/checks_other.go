//go:build !linux && !windows

package collectors

import "github.com/radanor/radanor-shield/internal/assessment"

// PlatformCollectors returns no collectors on platforms that CE-5 does not
// target. A scan on such a host reports an empty assessment rather than
// inventing readings.
func PlatformCollectors() []assessment.Collector { return nil }

// PlatformChecks returns no checks on unsupported platforms.
func PlatformChecks() []assessment.CheckDefinition { return nil }
