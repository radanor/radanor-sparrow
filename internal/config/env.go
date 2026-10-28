package config

import "os"

// Environment variable prefixes. RADANOR_SPARROW_* is current; the legacy
// RADANOR_SHIELD_* names are still read so that an existing installation's
// launcher, service unit or shell profile keeps pointing at the same private
// state directory and the same tuning instead of silently starting Sparrow
// against an empty database.
//
// The new name always wins, so an operator can migrate one variable at a time.
const (
	EnvPrefix       = "RADANOR_SPARROW_"
	LegacyEnvPrefix = "RADANOR_SHIELD_"
)

// Env returns the value of the named setting, preferring RADANOR_SPARROW_<name>
// and falling back to RADANOR_SHIELD_<name> from a pre-rebrand installation.
func Env(name string) string {
	if value := os.Getenv(EnvPrefix + name); value != "" {
		return value
	}
	return os.Getenv(LegacyEnvPrefix + name)
}
