//go:build linux

package collectors

import "testing"

// TestParseCandidatePocketBindsOnlyToCandidateVersion guards against a subtle
// but serious miscount: a package whose candidate comes from a regular pocket
// (jammy-updates) must NOT be reported as a pending security update, even when
// some older version of it is also published in the security pocket. Reporting
// that would inflate the security-update count on the strength of a version the
// system is not actually being offered.
func TestParseCandidatePocketBindsOnlyToCandidateVersion(t *testing.T) {
	// Real-world shape: candidate from jammy-updates, older build in security.
	updatesCandidate := `netplan.io:
  Installed: 0.107.1-3ubuntu0.22.04.4
  Candidate: 0.107.1-3ubuntu0.22.04.5
  Version table:
     0.107.1-3ubuntu0.22.04.5 500
        500 http://in.archive.ubuntu.com/ubuntu jammy-updates/main amd64 Packages
 *** 0.107.1-3ubuntu0.22.04.4 100
        100 /var/lib/dpkg/status
     0.106.1-7ubuntu0.22.04.4 500
        500 http://security.ubuntu.com/ubuntu jammy-security/main amd64 Packages
     0.104-0ubuntu2 500
        500 http://in.archive.ubuntu.com/ubuntu jammy/main amd64 Packages
`
	if got := parseCandidatePocket(updatesCandidate); got != "" {
		t.Errorf("candidate from jammy-updates reported pocket %q; must be empty (not a security update)", got)
	}

	// Candidate genuinely from the security pocket must be reported.
	securityCandidate := `libk5crypto3:
  Installed: 1.19.2-2ubuntu0.9
  Candidate: 1.19.2-2ubuntu0.10
  Version table:
     1.19.2-2ubuntu0.10 500
        500 http://security.ubuntu.com/ubuntu jammy-security/main amd64 Packages
 *** 1.19.2-2ubuntu0.9 100
        100 /var/lib/dpkg/status
     1.19.2-2ubuntu0.8 500
        500 http://in.archive.ubuntu.com/ubuntu jammy-updates/main amd64 Packages
`
	if got := parseCandidatePocket(securityCandidate); got != "main" {
		t.Errorf("security-pocket candidate reported pocket %q; want %q", got, "main")
	}

	// No candidate at all is not an error and must not be misread.
	if got := parseCandidatePocket("borked:\n  Installed: (none)\n  Candidate: (none)\n"); got != "" {
		t.Errorf("candidate (none) reported pocket %q; want empty", got)
	}
}

// TestParsePolicyByPackageSplitsBatchedOutput pins the parser used when many
// packages are resolved in a single apt-cache invocation. A mistake here would
// attribute one package's pocket to another and corrupt the pending-security
// count, so both sectioning and the per-section candidate binding are checked.
func TestParsePolicyByPackageSplitsBatchedOutput(t *testing.T) {
	out := `netplan.io:
  Installed: 0.107.1-3ubuntu0.22.04.4
  Candidate: 0.107.1-3ubuntu0.22.04.5
  Version table:
     0.107.1-3ubuntu0.22.04.5 500
        500 http://in.archive.ubuntu.com/ubuntu jammy-updates/main amd64 Packages
libk5crypto3:
  Installed: 1.19.2-2ubuntu0.9
  Candidate: 1.19.2-2ubuntu0.10
  Version table:
     1.19.2-2ubuntu0.10 500
        500 http://security.ubuntu.com/ubuntu jammy-security/main amd64 Packages
curl:
  Installed: 7.81.0-1ubuntu1.15
  Candidate: (none)
  Version table:
`
	got := parsePolicyByPackage(out)
	if len(got) != 3 {
		t.Fatalf("parsed %d packages, want 3: %v", len(got), got)
	}
	if got["netplan.io"] != "" {
		t.Errorf("netplan.io pocket = %q, want empty (candidate is not from security)", got["netplan.io"])
	}
	if got["libk5crypto3"] != "main" {
		t.Errorf("libk5crypto3 pocket = %q, want main", got["libk5crypto3"])
	}
	if got["curl"] != "" {
		t.Errorf("curl pocket = %q, want empty (no candidate)", got["curl"])
	}
}

// TestSafePackageName ensures a token parsed out of apt output can never reach a
// command argument unless it is an ordinary package name.
func TestSafePackageName(t *testing.T) {
	for _, ok := range []string{"libk5crypto3", "g++", "gcc-12", "python3.10", "pkg:name"} {
		if !safePackageName(ok) {
			t.Errorf("safePackageName(%q) = false; want true", ok)
		}
	}
	for _, bad := range []string{"", "has space", "semi;colon", "$(boom)", "../../etc/passwd", "a|b"} {
		if safePackageName(bad) {
			t.Errorf("safePackageName(%q) = true; want false", bad)
		}
	}
}
