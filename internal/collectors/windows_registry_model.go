package collectors

import (
	"strconv"
	"strings"

	"github.com/radanor/radanor-shield/internal/inventory"
)

// Windows software inventory: deciding whether the registry enumeration was a
// complete reading.
//
// This file carries no build tag, following the reasoning used for the Windows
// inventory model and the Defender model: the rule that decides whether a
// listing is complete must be reviewable and testable on any host, so it lives
// here and the windows-tagged collector does nothing but read the keys and hand
// over the outcomes.
//
// The rule it pins is CE-6 finding L-3: a registry enumeration failure must never
// be reported as a successful, empty inventory. Before this, a key that opened
// but could not be enumerated was skipped, so a host on which every enumeration
// failed reported "collected: 0 applications" — indistinguishable from a machine
// with nothing installed.

// registryKeyOutcome records what happened while reading one uninstall root.
type registryKeyOutcome struct {
	// Key names the root, in the form the review list uses.
	Key string
	// Opened reports that the key could be opened at all.
	Opened bool
	// Enumerated reports that its subkeys were listed.
	Enumerated bool
	// Reason is the platform's own explanation when enumeration failed.
	Reason string
}

// The status wording for the registry inventory. Kept as constants so the UI
// contract and the tests can both assert the exact text.
const (
	registryDeniedReason  = "the uninstall registry keys could not be read"
	registryEmptyReason   = "the uninstall registry keys were found but none could be enumerated, so this inventory cannot be read as empty"
	registryPartialPrefix = "some uninstall registry keys could not be enumerated, so the application list is partial"
)

// registryInventoryStatus turns the per-key outcomes into the honest status for
// the software inventory.
//
// It returns the status, the error to show when the collection is not usable, and
// the coverage note for a partial or complete reading.
func registryInventoryStatus(outcomes []registryKeyOutcome) (inventory.Status, string, string) {
	opened := 0
	enumerated := 0
	var unreadable []string
	for _, outcome := range outcomes {
		if outcome.Opened {
			opened++
		}
		if outcome.Enumerated {
			enumerated++
			continue
		}
		if reason := outcome.Reason; reason != "" {
			unreadable = append(unreadable, outcome.Key+": "+reason)
		}
	}

	switch {
	case opened == 0:
		// Nothing could be opened: the platform refused, which is a permission
		// problem, not an empty catalogue.
		return inventory.PermissionDenied, registryDeniedReason, ""
	case enumerated == 0:
		// Keys were found, and none could be listed. An empty result here says
		// nothing about what is installed.
		return inventory.Failed, registryEmptyReason, ""
	case enumerated < len(outcomes):
		// Some roots were read and some were not: the list is a partial reading,
		// and the note names which roots were missing so a reader can judge how
		// much of the registry was actually covered.
		return inventory.Incomplete, "", registryPartialPrefix + ": " + registryUnreadableNote(unreadable)
	}
	// Every root was enumerated. The result may still be a partial catalogue of
	// what is installed (portable and Microsoft Store applications are not
	// registered for uninstall), and the collector's note says so.
	return inventory.Collected, "", ""
}

// registryUnreadableNote renders the unreadable roots for the coverage note,
// bounded so an unexpected number of failures cannot make the note unbounded.
func registryUnreadableNote(parts []string) string {
	const maxParts = 3
	const maxPartLen = 96
	out := strings.Builder{}
	for i, part := range parts {
		if i >= maxParts {
			out.WriteString("; and " + strconv.Itoa(len(parts)-maxParts) + " more")
			break
		}
		if len(part) > maxPartLen {
			part = part[:maxPartLen]
		}
		if i > 0 {
			out.WriteString("; ")
		}
		out.WriteString(part)
	}
	return out.String()
}
