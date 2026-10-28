//go:build linux

package collectors

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/radanor/radanor-shield/internal/assessment"
)

const (
	// maxSSHDirectives bounds how many matched directives are echoed into evidence.
	maxSSHDirectives = 40
	// sshdConfigPath is the single ssh server configuration this collector reads.
	sshdConfigPath = "/etc/ssh/sshd_config"
	// Bounds on Include expansion so a recursive or very large configuration
	// cannot turn a bounded config read into unbounded work.
	maxIncludeDepth    = 4
	maxIncludeFiles    = 32
	maxIncludeEvidence = 8
)

// sshTextReader abstracts reading one configuration file. Production passes
// readFileText; tests substitute a fixture or a reader that reports a
// permission refusal, so the collector path under test is the real one.
type sshTextReader func(path string) (string, error)

// SSHServerCollector reports the effective OpenSSH server configuration.
//
// Source: /etc/ssh/sshd_config and any Include targets it names, parsed
// directly. sshd applies first-match-wins per keyword, ignoring later
// duplicates, so this parser honors that rule rather than naively scanning for
// keywords. Match blocks are respected: a directive inside a conditional block
// is recorded separately and never treated as unconditional.
//
// The collector only reports what is configured. Absence of a directive means
// sshd's compiled-in default applies, which is recorded as "unset" rather than
// being guessed.
type SSHServerCollector struct{}

func (SSHServerCollector) ID() string { return "ssh_server_config" }

func (SSHServerCollector) Collect(ctx context.Context) ([]assessment.Observation, error) {
	installed, ierr := sshServerInstalled(ctx)
	return collectSSHDConfig(sshdConfigPath, installed, ierr, readFileText)
}

// collectSSHDConfig is the whole collector body, separated from Collect so the
// integration tests can drive the exact production path from a configuration
// fixture instead of the host's real sshd_config.
func collectSSHDConfig(path string, installed bool, installErr error, read sshTextReader) ([]assessment.Observation, error) {
	obs := assessment.Observation{
		ID: "ssh_server_config", Collector: "ssh_server_config", Platform: ubuntuPlatformID,
		Status: assessment.Collected, Source: path,
	}
	if installErr != nil {
		// A failed unit query does not block the config read; note it and go on.
		obs.Error = boundText("could not confirm ssh server installation: " + installErr.Error())
	}
	if !installed {
		// No ssh server on this host: the check genuinely does not apply.
		obs.Status = assessment.Unsupported
		obs.Error = "the OpenSSH server is not installed on this system"
		return []assessment.Observation{obs}, nil
	}

	base, rerr := read(path)
	if rerr != nil {
		if os.IsNotExist(rerr) {
			obs.Status = assessment.Unsupported
			obs.Error = "no ssh server configuration file is present"
			return []assessment.Observation{obs}, nil
		}
		if os.IsPermission(rerr) {
			obs.Status = assessment.PermissionDenied
			obs.Error = "the ssh server configuration is not readable by this user"
			return []assessment.Observation{obs}, nil
		}
		obs.Status = assessment.Failed
		obs.Error = boundText(rerr.Error())
		return []assessment.Observation{obs}, nil
	}

	// Expand Include targets in place so first-match-wins is decided against
	// the configuration sshd would actually load. If any target cannot be
	// resolved the effective precedence is unknown, so the observation is
	// incomplete and every SSH check reports UNKNOWN rather than a verdict
	// derived from a partial file.
	text, includes, unresolved := expandSSHIncludes(path, base, read)
	if unresolved != "" {
		obs.Status = assessment.Incomplete
		obs.Error = boundText(unresolved)
		return []assessment.Observation{obs}, nil
	}

	effective, conditional, unset := parseSSHDConfig(text, trackedSSHKeys)
	// directives_set and directives_unset are both counted over the tracked
	// allowlist, so they are comparable and set+unset always equals the size of
	// the allowlist.
	obs.Values = append(obs.Values,
		presentInt("directives_set", int64(len(effective))),
		presentInt("directives_unset", int64(len(unset))),
		presentValue("unset_directives", strings.Join(unset, ",")),
	)
	if len(includes) > 0 {
		shown := includes
		if len(shown) > maxIncludeEvidence {
			shown = append(append([]string(nil), shown[:maxIncludeEvidence]...), "…")
		}
		obs.Values = append(obs.Values, presentValue("include_files", strings.Join(shown, ",")))
	}
	count := 0
	for _, key := range trackedSSHKeys {
		if v, ok := effective[key]; ok && count < maxSSHDirectives {
			obs.Values = append(obs.Values, presentValue("ssh."+key, v))
			count++
		}
		if v, ok := conditional[key]; ok && count < maxSSHDirectives {
			obs.Values = append(obs.Values, presentValue("ssh_conditional."+key, v))
			count++
		}
	}
	return []assessment.Observation{obs}, nil
}

// trackedSSHKeys are the server directives whose posture is meaningful. The
// list is a reviewed allowlist, not an open-ended scan.
var trackedSSHKeys = []string{
	"PermitRootLogin",
	"PasswordAuthentication",
	"PubkeyAuthentication",
	"PermitEmptyPasswords",
	"X11Forwarding",
	"AllowTcpForwarding",
	"Protocol",
	"MaxAuthTries",
	"ClientAliveInterval",
	"AllowGroups",
	"AllowUsers",
}

// splitSSHDLine applies sshd's own line syntax: leading whitespace is ignored,
// a line whose first non-blank character is '#' is a comment, and the keyword
// is separated from its arguments either by whitespace or by optional
// whitespace and exactly one '='. Tabs are ordinary separators, which the
// space-only split this replaced could not read.
//
// The returned keyword is lowercased for case-insensitive matching; the
// argument text is returned verbatim apart from surrounding whitespace,
// because sshd treats trailing junk (including a stray '#') as a configuration
// error rather than a comment. Keeping it means such a line yields an
// unrecognised value and therefore UNKNOWN, never a fabricated PASS.
func splitSSHDLine(raw string) (keyword, args string, ok bool) {
	line := strings.TrimLeft(raw, " \t\r")
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", false
	}
	line = strings.TrimRight(line, "\r")
	idx := strings.IndexAny(line, " \t=")
	if idx < 0 {
		keyword = strings.ToLower(line)
		if keyword == "" {
			return "", "", false
		}
		return keyword, "", true
	}
	keyword = strings.ToLower(line[:idx])
	if keyword == "" {
		return "", "", false
	}
	args = strings.TrimLeft(line[idx:], " \t=")
	return keyword, args, true
}

// parseSSHDConfig implements sshd's first-match-wins semantics. Directives
// inside a Match block are returned separately in conditional so they are never
// mistaken for global settings.
//
// Both returned maps are keyed by the canonical spelling from `tracked`, not by
// the lowercased line keyword. That shared contract is what lets the collector's
// evidence, the unset list and the check evaluators agree; keying one map
// lowercased and indexing it with the tracked spelling is exactly the defect
// that silently discarded every directive this collector read.
//
// Only allowlisted directives are retained. Untracked keywords are still
// recognised (so a Match or Include line is not mistaken for a directive) but
// their values are never copied into evidence.
func parseSSHDConfig(text string, tracked []string) (effective, conditional map[string]string, unset []string) {
	effective = map[string]string{}
	conditional = map[string]string{}
	trackedName := map[string]string{}
	for _, key := range tracked {
		trackedName[strings.ToLower(key)] = key
	}
	// First-match-wins applies within each scope independently: a global
	// directive must not suppress recording a conditional one, and vice versa.
	seenGlobal := map[string]bool{}
	seenConditional := map[string]bool{}
	inMatch := false
	for _, raw := range strings.Split(text, "\n") {
		keyword, args, ok := splitSSHDLine(raw)
		if !ok {
			continue
		}
		if keyword == "match" {
			// A Match block runs to the end of the file; nothing returns to
			// the global scope after one starts.
			inMatch = true
			continue
		}
		name, trackedDirective := trackedName[keyword]
		if !trackedDirective {
			continue
		}
		value := strings.TrimSpace(args)
		// sshd honors the first occurrence of a keyword within its scope; later
		// duplicates in the same scope are ignored.
		if inMatch {
			if seenConditional[keyword] {
				continue
			}
			seenConditional[keyword] = true
			conditional[name] = value
			continue
		}
		if seenGlobal[keyword] {
			continue
		}
		seenGlobal[keyword] = true
		effective[name] = value
	}
	for _, key := range tracked {
		if _, ok := effective[key]; !ok {
			unset = append(unset, key)
		}
	}
	return effective, conditional, unset
}

// expandSSHIncludes splices Include targets into the configuration at the point
// the directive appears, which is where sshd resolves them, so first-match-wins
// still reflects the effective configuration. It returns the expanded text, the
// files that were loaded (for evidence), and a non-empty reason when the
// effective configuration could not be established.
func expandSSHIncludes(path, text string, read sshTextReader) (string, []string, string) {
	seen := map[string]bool{filepath.Clean(path): true}
	loaded := []string{}
	expanded, unresolved := expandSSHConfig(path, sshConfigRoot(path), text, read, 0, seen, &loaded)
	return expanded, loaded, unresolved
}

// sshConfigRoot is the directory the main configuration lives in; every Include
// target must resolve inside it. sshd_config is unprivileged world-readable
// configuration, but an Include line names its own targets, so containment is
// what stops the directive widening what an unprivileged collector opens. The
// root is derived from the file actually read rather than hardcoded, so the
// production path (/etc/ssh/sshd_config) yields /etc/ssh and a test fixture
// yields its own directory.
func sshConfigRoot(path string) string {
	return filepath.Dir(filepath.Clean(path))
}

func expandSSHConfig(path, root, text string, read sshTextReader, depth int, seen map[string]bool, loaded *[]string) (string, string) {
	var out strings.Builder
	var unresolved string
	fail := func(reason string) {
		if unresolved == "" {
			unresolved = reason
		}
	}
	inMatch := false
	for _, raw := range strings.Split(text, "\n") {
		keyword, args, ok := splitSSHDLine(raw)
		if ok && keyword == "match" {
			inMatch = true
		}
		if !ok || keyword != "include" {
			out.WriteString(raw)
			out.WriteByte('\n')
			continue
		}
		if inMatch {
			// sshd rejects Include inside a Match block, so the configuration
			// would not load; refusing to guess keeps that from looking like a
			// clean reading.
			fail("an Include directive inside a Match block could not be resolved; effective sshd precedence is unknown")
			continue
		}
		if depth+1 > maxIncludeDepth {
			fail("Include nesting exceeds the read depth limit; effective sshd precedence is unknown")
			continue
		}
		if len(*loaded) >= maxIncludeFiles {
			fail("the configuration includes too many files to read; effective sshd precedence is unknown")
			continue
		}
		targets, reason := resolveIncludeTargets(args, path, root)
		if reason != "" {
			fail(reason)
			continue
		}
		for _, target := range targets {
			if seen[target] {
				fail("an Include cycle was detected; effective sshd precedence is unknown")
				continue
			}
			if len(*loaded) >= maxIncludeFiles {
				fail("the configuration includes too many files to read; effective sshd precedence is unknown")
				continue
			}
			seen[target] = true
			included, err := read(target)
			if err != nil {
				if os.IsNotExist(err) {
					fail("an Include target is missing; effective sshd precedence is unknown")
				} else if os.IsPermission(err) {
					fail("an Include target is not readable by this user; effective sshd precedence is unknown")
				} else {
					fail("an Include target could not be read; effective sshd precedence is unknown")
				}
				continue
			}
			*loaded = append(*loaded, target)
			nested, reason := expandSSHConfig(target, root, included, read, depth+1, seen, loaded)
			if reason != "" {
				fail(reason)
			}
			out.WriteString(nested)
		}
	}
	return out.String(), unresolved
}

// resolveIncludeTargets turns one Include argument into concrete files that are
// provably inside sshdConfigRoot, both as written and after symlink resolution.
func resolveIncludeTargets(pattern, fromPath, root string) ([]string, string) {
	p := strings.TrimSpace(pattern)
	if p == "" {
		return nil, "an Include directive has no target; effective sshd precedence is unknown"
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(filepath.Dir(fromPath), p)
	}
	p = filepath.Clean(p)
	// The root itself may sit under a symlinked directory (for example a
	// symlinked TMPDIR), so containment is checked against both the path as
	// written and the path after resolution.
	resolvedRoot := root
	if r, err := filepath.EvalSymlinks(root); err == nil {
		resolvedRoot = filepath.Clean(r)
	}
	if !withinSSHConfigRoot(p, root) && !withinSSHConfigRoot(p, resolvedRoot) {
		return nil, "an Include target lies outside " + root + "; effective sshd precedence is unknown"
	}
	matches, err := filepath.Glob(p)
	if err != nil {
		return nil, "an Include pattern could not be interpreted; effective sshd precedence is unknown"
	}
	if len(matches) == 0 {
		if hasGlobMeta(p) {
			// sshd treats a glob that matches nothing as empty, not as an error.
			return nil, ""
		}
		return nil, "an Include target does not exist; effective sshd precedence is unknown"
	}
	sort.Strings(matches)
	targets := make([]string, 0, len(matches))
	for _, match := range matches {
		clean := filepath.Clean(match)
		if !withinSSHConfigRoot(clean, root) && !withinSSHConfigRoot(clean, resolvedRoot) {
			return nil, "an Include target lies outside " + root + "; effective sshd precedence is unknown"
		}
		// A symlink must not become a route to an arbitrary path, so containment
		// is re-checked after resolution.
		if resolved, err := filepath.EvalSymlinks(clean); err == nil && !withinSSHConfigRoot(filepath.Clean(resolved), resolvedRoot) {
			return nil, "an Include target resolves outside " + root + "; effective sshd precedence is unknown"
		}
		targets = append(targets, clean)
	}
	return targets, ""
}

func withinSSHConfigRoot(path, root string) bool {
	return path == root || strings.HasPrefix(path, root+"/")
}

func hasGlobMeta(pattern string) bool {
	return strings.ContainsAny(pattern, "*?[")
}

// sshServerInstalled reports whether an ssh server unit is present. It reads
// unit-file state, which is readable without elevated privileges.
func sshServerInstalled(ctx context.Context) (bool, error) {
	res, err := runCommand(ctx, "systemctl", "list-unit-files", "ssh.service", "sshd.service")
	if err != nil {
		return false, err
	}
	// A clipped unit list could hide the ssh unit and make an installed server
	// look absent, so a clipped reading is refused rather than answered.
	if res.Truncated {
		return false, errors.New(truncatedNote("the unit-file listing", res.Cap))
	}
	// The unit list names a real unit when a state other than the header line
	// mentions ssh; an absent server lists nothing.
	for _, line := range strings.Split(res.Output, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && (fields[0] == "ssh.service" || fields[0] == "sshd.service") {
			return true, nil
		}
	}
	return false, nil
}
