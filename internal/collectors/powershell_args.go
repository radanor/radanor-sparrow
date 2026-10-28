package collectors

// The PowerShell argument vector is built here, outside any build tag, so the
// execution-policy behaviour the CE-5 review flagged is exercised by the test
// suite that runs on every platform rather than only by the Windows
// cross-build.

// powershellArgs builds the fixed argument vector for a read-only PowerShell
// query.
//
// The host's execution policy is deliberately left in force: every collector
// runs an inline '-Command' expression of cmdlets, not a script file, so it does
// not need an execution policy to be bypassed. The review flagged the previous
// '-ExecutionPolicy Bypass' as a needless weakening of a system policy for the
// collector's own process, and it would also mask a policy an operator intends
// to enforce. Passing no execution-policy argument is the honest,
// least-privilege choice.
//
// Every flag is fixed at compile time and the script is a constant in its
// collector; no caller- or user-supplied value ever reaches this vector.
func powershellArgs(script string) []string {
	return []string{"-NoProfile", "-NonInteractive", "-Command", script}
}
