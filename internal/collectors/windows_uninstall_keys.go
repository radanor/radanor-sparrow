package collectors

// The reviewed list of registry locations that hold Windows application
// registrations.
//
// This file carries no build tag on purpose. Like the Defender model, it holds
// the part of the Windows inventory that can be reviewed and tested on any host:
// the exact set of keys, and nothing else. Mapping a root name onto the registry
// hive handle, and opening the key at all, stays behind the windows build tag.
//
// The list covers both registry views so a 32-bit application installed on a
// 64-bit host is not missed, and the per-user hive so a user-scoped installation
// is seen by the user who owns it. Nothing here recurses deeper than one level or
// follows a symlink outside the named path.

const (
	registryHiveLocalMachine = "hklm"
	registryHiveCurrentUser  = "hkcu"
)

// reviewedRegistryKey is one uninstall root to read.
type reviewedRegistryKey struct {
	// root names the hive; see registryHiveLocalMachine and
	// registryHiveCurrentUser.
	root string
	// path is the subkey path relative to that hive.
	path string
}

var uninstallKeys = []reviewedRegistryKey{
	{registryHiveLocalMachine, `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall`},
	{registryHiveLocalMachine, `SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall`},
	{registryHiveCurrentUser, `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall`},
}
