package releasecheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// readReleaseDoc reads a repository document by its slash-separated relative path.
func readReleaseDoc(t *testing.T, rel string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("%s is missing: %v", rel, err)
	}
	return string(raw)
}

// The notices name the LGPL and the GCC Runtime Library Exception, so the terms they
// name have to travel with the artifact. Naming a licence without reproducing it
// leaves the recipient unable to read the conditions that apply to what they were
// given, which is exactly what LGPL-2.1 §6 asks a distribution to carry.
//
// This test pins the texts, not a conclusion. The question of whether §6 is engaged
// by linking the host's own libraries stays open in §6 of the notices — the owner accepted
// that uncertainty on 2026-10-28 rather than settling it, and
// TestOwnerRiskDecisionClosesTheGateWithoutClaimingClearance keeps it described that way.
// Shipping
// the text is what lets the answer be either way without another build.
func TestThirdPartyNoticesCarryTheLicenceTextsTheyName(t *testing.T) {
	notices := readReleaseDoc(t, "THIRD_PARTY_NOTICES.md")

	for _, subject := range []struct{ what, anchor string }{
		{"the LGPL-2.1 title", "GNU LESSER GENERAL PUBLIC LICENSE"},
		{"the LGPL-2.1 version line", "Version 2.1, February 1999"},
		{"LGPL-2.1 §6 itself, the relinking clause", "suitable shared library"},
		{"the GCC Runtime Library Exception", `GCC Runtime Library Exception ("Exception") is an additional`},
		{"the Exception's central grant", "Eligible Compilation Processes"},
		{"the Exception's no-weakening clause", "2. No Weakening of GCC Copyleft."},
	} {
		if !strings.Contains(notices, subject.anchor) {
			t.Errorf("%s is named in the notices but its text is not reproduced (%q is missing); a recipient cannot check terms they were not given", subject.what, subject.anchor)
		}
	}

	for _, heading := range []string{
		"## Appendix F — GNU Lesser General Public License v2.1",
		"## Appendix G — GCC Runtime Library Exception 3.1",
	} {
		if !strings.Contains(notices, heading) {
			t.Errorf("the notices do not carry %q, so the licence text has no labelled home", heading)
		}
	}

	// A truncated paste is worse than an absent appendix, because it looks complete.
	// The real LGPL-2.1 is just over 500 lines; refuse a fragment.
	start := strings.Index(notices, "## Appendix F — GNU Lesser General Public License v2.1")
	end := strings.Index(notices, "## Appendix G — GCC Runtime Library Exception 3.1")
	if start < 0 || end < start {
		t.Fatalf("the LGPL appendix is missing or misplaced, so its length cannot be checked")
	}
	if lines := strings.Count(notices[start:end], "\n"); lines < 480 {
		t.Errorf("the LGPL appendix is only %d lines; the full LGPL-2.1 text is about 500, so this looks truncated", lines)
	}

	// Reproducing the text must not read as an answer to the open question: the
	// marker has to stay, and the appendix has to say what it does not decide.
	if !strings.Contains(notices, "whichever way the open question in") {
		t.Error("the LGPL appendix must state that it is reproduced regardless of the open conclusion, not as a settled answer to it")
	}
	if !strings.Contains(notices, "does not settle that question") {
		t.Error("the LGPL appendix must say plainly that it does not decide the §6 question")
	}
}

// Radanor is a brand, not an incorporated entity: there is no company to accept the
// licence terms or to sign a publisher approval. Two live documents previously said
// otherwise, which sent a reader looking for a signatory that does not exist and
// contradicted LICENSE, where the Licensor is an individual.
func TestReleaseDocumentsDoNotInventACorporatePublisher(t *testing.T) {
	for _, rel := range []string{
		"docs/licensing/RELEASE_APPROVAL_CHECKLIST.md",
		"docs/licensing/LICENSING.md",
		"docs/release/FINAL_RELEASE_CHECKLIST.md",
	} {
		document := strings.ToLower(readReleaseDoc(t, rel))
		for _, claim := range []string{"corporate identity", "the company accepts", "company's name"} {
			if strings.Contains(document, claim) {
				t.Errorf("%s still describes Radanor as a company (%q); the Licensor is an individual and Radanor is a product brand", rel, claim)
			}
		}
	}

	checklist := readReleaseDoc(t, "docs/licensing/RELEASE_APPROVAL_CHECKLIST.md")
	if !strings.Contains(checklist, "brand, not an incorporated entity") {
		t.Error("the approval checklist must state that Radanor is a brand rather than an incorporated entity, so item 6 asks for the right signature")
	}
}
