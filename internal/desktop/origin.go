package desktop

// ValidOrigin reports whether an origin is the exact private loopback origin the
// supervisor publishes: plain http, the loopback host, an explicit port, and
// nothing else.
//
// It is exported for the isolated Linux presentation component
// (cmd/radanor-sparrow-window), which reads the same discovery record before it
// loads the dashboard. Keeping one implementation means the window and the
// supervisor cannot disagree about what a legitimate origin is — and a future
// relaxation for the window's sake is impossible, because there is only one
// rule.
func ValidOrigin(origin string) bool { return validOrigin(origin) }
