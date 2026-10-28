// Package releasecheck implements the licensing validation for Radanor Sparrow
// Community Edition.
//
// It has two modes, because the repository is intentionally in an intermediate
// state: the Business Source License 1.1 here is a proposal whose Licensor
// identity and per-version Change Date are deliberately still placeholders (see
// the private release decision log).
//
//   - Development mode accepts those documented placeholders but still rejects
//     structural problems: altered license text, a missing or invalid license
//     structure, a missing Additional Use Grant, a missing GPLv3 Change License,
//     an invalid concrete Change Date, and inconsistent licensing metadata.
//   - Release mode is strict and rejects every unresolved placeholder as well.
//
// Release is the zero value, so an Input that forgets to set a mode fails closed.
// Development builds, `go test ./...` and ordinary CI use development mode; only
// the explicit release build path and the release workflow use release mode.
package releasecheck

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Mode selects how strict validation is.
type Mode int

const (
	// Release rejects every finding, including documented placeholders. This is
	// the zero value so the default is strict.
	Release Mode = iota
	// Development allows explicitly documented placeholders but rejects
	// structural problems.
	Development
)

// Finding codes. Stable strings so reports and tests can match them.
const (
	CodeLicenseMissing       = "bsl.license.missing"
	CodeStructureInvalid     = "bsl.structure.invalid"
	CodeParametersMissing    = "bsl.parameters.missing"
	CodeTextNotIntact        = "bsl.text.intact"
	CodeLicensorUnverified   = "bsl.licensor.verified"
	CodeCopyrightHolder      = "bsl.copyright-holder.verified"
	CodeChangeDateMissing    = "bsl.change-date.concrete"
	CodeChangeDateInvalid    = "bsl.change-date.valid"
	CodeChangeDateMaximum    = "bsl.change-date.maximum"
	CodeChangeDateOrder      = "bsl.change-date.order"
	CodeAdditionalUseGrant   = "bsl.additional-use-grant.present"
	CodeChangeLicenseGPL3    = "bsl.change-license.gpl3"
	CodeNoticesMissing       = "package.notices.present"
	CodePackageLicense       = "package.license.present"
	CodePackageLicenseMatch  = "package.license.matches"
	CodePlaceholder          = "metadata.placeholder.free"
	CodeMetadataInconsistent = "metadata.consistent"
)

// Finding is one licensing observation.
type Finding struct {
	Code string
	// Placeholder marks a finding caused by a documented placeholder (such as the
	// Licensor identity or the first-release Change Date). It is allowed in
	// Development mode and blocking in Release mode.
	Placeholder bool
	Message     string
}

// Result is the outcome of a validation run.
type Result struct {
	Mode     Mode
	Findings []Finding
}

// Blocking returns the findings that must stop the current mode. In Development
// mode, documented-placeholder findings are excluded.
func (r Result) Blocking() []Finding {
	var out []Finding
	for _, f := range r.Findings {
		if r.Mode == Development && f.Placeholder {
			continue
		}
		out = append(out, f)
	}
	return out
}

// Allowed returns the findings that are tolerated in the current mode: the
// documented placeholders in Development mode, and nothing in Release mode.
func (r Result) Allowed() []Finding {
	if r.Mode != Development {
		return nil
	}
	var out []Finding
	for _, f := range r.Findings {
		if f.Placeholder {
			out = append(out, f)
		}
	}
	return out
}

// Blocked reports whether any finding blocks the current mode.
func (r Result) Blocked() bool { return len(r.Blocking()) > 0 }

func (r *Result) block(code, format string, args ...any) {
	r.Findings = append(r.Findings, Finding{Code: code, Message: fmt.Sprintf(format, args...)})
}

func (r *Result) placeholder(code, format string, args ...any) {
	r.Findings = append(r.Findings, Finding{Code: code, Placeholder: true, Message: fmt.Sprintf(format, args...)})
}

// Input selects what to validate and how strictly.
type Input struct {
	// Root is the repository root holding LICENSE and THIRD_PARTY_NOTICES.md.
	Root string
	// ReleaseDir, when set, is a built release artifact directory that must
	// carry the license and third-party notices.
	ReleaseDir string
	// Mode defaults to Release (strict).
	Mode Mode
}

var (
	paramLabelRe  = regexp.MustCompile(`^([A-Za-z][A-Za-z ]*):\s*(.*)$`)
	dateRe        = regexp.MustCompile(`\b\d{4}-\d{2}-\d{2}\b`)
	firstDistRe   = regexp.MustCompile(`First public distribution of this version:\s*(\d{4}-\d{2}-\d{2})`)
	licenseNameRe = regexp.MustCompile(`LicenseName\s*=\s*"([^"]*)"`)
)

// requiredParameters must all be present for the license to be well formed.
var requiredParameters = []string{"Licensor", "Licensed Work", "Additional Use Grant", "Change Date", "Change License"}

// releaseFacingFiles are the metadata artifacts a reader sees when they receive
// a release. docs/ and CONTRIBUTING.md are deliberately excluded: they are review
// documents that are expected to *describe* the open items.
var releaseFacingFiles = []string{
	"LICENSE",
	"README.md",
	"THIRD_PARTY_NOTICES.md",
	filepath.Join("internal", "branding", "branding.go"),
	filepath.Join("web", "src", "product.ts"),
}

// placeholderMarkers are phrases that mean a value is still unresolved. Matching
// is case-insensitive.
var placeholderMarkers = []string{
	"not yet verified",
	"release blocker",
	"release-specific change date",
	"to be set on first public",
	"legal review required",
	"pending legal",
	"placeholder",
	"tbd",
}

// Run validates the licensing metadata under in.Root (and in.ReleaseDir when set)
// using in.Mode, and returns every finding.
func Run(in Input) Result {
	res := &Result{Mode: in.Mode}

	licensePath := filepath.Join(in.Root, "LICENSE")
	raw, err := os.ReadFile(licensePath)
	if err != nil {
		res.block(CodeLicenseMissing, "LICENSE is missing or unreadable at %s: %v", licensePath, err)
		return *res
	}
	// Normalize line endings once, so a CRLF checkout parses and compares exactly
	// like an LF one and no stray carriage return reaches a diagnostic.
	license := normalizeEOL(string(raw))
	params := parseParameters(license)

	if !wellStructured(license) {
		res.block(CodeStructureInvalid, "LICENSE does not have the expected structure: a Parameters block followed by a Terms section")
	}
	for _, label := range requiredParameters {
		if strings.TrimSpace(params[label]) == "" {
			res.block(CodeParametersMissing, "the parameter %q is missing or empty", label)
		}
	}
	if normalizeText(licenseBody(license)) != normalizeText(normalizeEOL(canonicalBody)) {
		res.block(CodeTextNotIntact, "the standardized Business Source License 1.1 text has been modified; the Terms, Covenants and Notice must match the official text verbatim")
	}

	checkLicensor(params["Licensor"], res)
	checkCopyrightHolder(params["Licensor"], params["Licensed Work"], res)
	checkAdditionalUseGrant(params["Additional Use Grant"], res)
	checkChangeLicense(params["Change License"], res)
	checkChangeDate(params["Change Date"], res)
	checkMetadataConsistency(in.Root, license, res)
	scanPlaceholders(in.Root, res)

	notices := filepath.Join(in.Root, "THIRD_PARTY_NOTICES.md")
	if info, err := os.Stat(notices); err != nil || info.Size() == 0 {
		res.block(CodeNoticesMissing, "THIRD_PARTY_NOTICES.md is missing or empty at %s", notices)
	}

	if in.ReleaseDir != "" {
		checkReleaseDir(in.ReleaseDir, license, res)
	}
	return *res
}

// wellStructured requires a Parameters heading followed by a Terms section.
func wellStructured(license string) bool {
	parameters, terms := -1, -1
	for i, line := range strings.Split(license, "\n") {
		switch strings.TrimSpace(line) {
		case "Parameters":
			if parameters < 0 {
				parameters = i
			}
		case "Terms":
			if terms < 0 {
				terms = i
			}
		}
	}
	return parameters >= 0 && terms > parameters
}

// parseParameters extracts the Parameters block of a LICENSE file. A parameter
// starts at a column-zero "Label:" line; indented and blank lines continue it.
// It expects end-of-line-normalized input.
func parseParameters(license string) map[string]string {
	params := map[string]string{}
	lines := strings.Split(license, "\n")
	inParams, current := false, ""
	var buf []string
	flush := func() {
		if current != "" {
			params[current] = strings.TrimSpace(strings.Join(buf, "\n"))
		}
	}
	for _, line := range lines {
		if !inParams {
			if strings.TrimSpace(line) == "Parameters" {
				inParams = true
			}
			continue
		}
		if strings.TrimSpace(line) == "Terms" {
			flush()
			current = ""
			break
		}
		if m := paramLabelRe.FindStringSubmatch(line); m != nil {
			flush()
			current, buf = m[1], []string{m[2]}
			continue
		}
		if current != "" {
			buf = append(buf, line)
		}
	}
	flush()
	return params
}

func checkLicensor(value string, res *Result) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		res.block(CodeLicensorUnverified, "the Licensor parameter is empty; BSL 1.1 requires a specified Licensor")
		return
	}
	if containsPlaceholderMarker(trimmed) {
		res.placeholder(CodeLicensorUnverified, "the Licensor is still a placeholder (%q); it must name the legally verified copyright holder before release (see the private release decision log)", firstLine(trimmed))
	}
}

// copyrightHolderRe finds the copyright line in the Licensed Work parameter. The
// BSL parameter block names the holder with a © sign; "(c)" and "Copyright" are
// accepted because a plain-text rendering of the same statement is common. The
// holder runs to the end of the parameter, because it may wrap across lines.
var copyrightHolderRe = regexp.MustCompile(`(?i)(?:©|\(c\)|copyright)\s*(.+)$`)

// checkCopyrightHolder enforces the second half of the BSL identity: the Licensed
// Work must name the same party as the Licensor. A placeholder Licensor tolerates a
// placeholder (or absent) holder in development mode, because that is the documented
// intermediate state; in release mode every one of those cases blocks.
//
// The gate used to read only the Licensor parameter, which meant a change that
// replaced it could leave the © line as a placeholder and still pass — the audit in
// the private engineering record §2.2.
func checkCopyrightHolder(licensor, licensedWork string, res *Result) {
	licensorValue := strings.TrimSpace(licensor)
	licensorUnresolved := licensorValue == "" || containsPlaceholderMarker(licensorValue)

	holder, found := copyrightHolder(licensedWork)
	switch {
	case !found:
		if licensorUnresolved {
			res.placeholder(CodeCopyrightHolder, "the Licensed Work does not name a copyright holder yet; it must read \"The Licensed Work is © <holder>\" and name the same party as the Licensor")
			return
		}
		res.block(CodeCopyrightHolder, "the Licensed Work does not name a copyright holder; the BSL copyright line must name the same party as the Licensor (%q)", firstLine(licensorValue))
	case containsPlaceholderMarker(holder):
		res.placeholder(CodeCopyrightHolder, "the copyright holder in the Licensed Work is still a placeholder (%q); it must name the same legally verified holder as the Licensor", firstLine(holder))
	case licensorUnresolved:
		res.placeholder(CodeCopyrightHolder, "the copyright holder (%q) cannot be checked against an unresolved Licensor; both must name the same legally verified party", firstLine(holder))
	default:
		if holder != normalizeParty(firstLine(licensorValue)) {
			res.block(CodeCopyrightHolder, "the copyright holder in the Licensed Work (%q) does not match the Licensor (%q); they must name the same party", holder, firstLine(licensorValue))
		}
	}
}

// A copyright year is conventionally printed beside the holder, as in
// "The Licensed Work is © 2026 Jane Doe" or "© Jane Doe, 2026". The year is not
// a party, so the comparison against the Licensor ignores one four-digit year
// adjacent to the name and nothing else: a different name still fails, and a ©
// line that holds only a year names nobody.
var (
	holderLeadingYearRe  = regexp.MustCompile(`^\d{4}\s+`)
	holderTrailingYearRe = regexp.MustCompile(`(?:,?\s+\d{4})$`)
)

// copyrightHolder extracts the party named after the copyright sign or word in
// the Licensed Work parameter. Whitespace from line wrapping is collapsed, a
// copyright year beside the name is dropped, and trailing punctuation is dropped
// so "Ltd." compares to the Licensor's own spelling.
func copyrightHolder(licensedWork string) (string, bool) {
	flat := strings.Join(strings.Fields(licensedWork), " ")
	match := copyrightHolderRe.FindStringSubmatch(flat)
	if match == nil {
		return "", false
	}
	holder := strings.TrimPrefix(strings.TrimSpace(match[1]), "©")
	holder = holderLeadingYearRe.ReplaceAllString(holder, "")
	holder = holderTrailingYearRe.ReplaceAllString(holder, "")
	holder = normalizeParty(holder)
	if holder == "" {
		return "", false
	}
	return holder, true
}

// normalizeParty makes the two spellings of one legal name comparable: whitespace
// from line wrapping is collapsed and trailing punctuation is dropped, so "Ltd."
// and "Ltd" are the same party and nothing else is.
func normalizeParty(value string) string {
	return strings.TrimRight(strings.Join(strings.Fields(value), " "), ".,;")
}

func checkAdditionalUseGrant(value string, res *Result) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		res.block(CodeAdditionalUseGrant, "the Additional Use Grant is empty; it must grant the intended free use or say \"None\"")
		return
	}
	if strings.EqualFold(trimmed, "none") {
		res.block(CodeAdditionalUseGrant, "the Additional Use Grant is \"None\", so free personal and internal production use is not granted")
		return
	}
	if containsPlaceholderMarker(trimmed) {
		res.block(CodeAdditionalUseGrant, "the Additional Use Grant still contains an unresolved placeholder")
	}
}

func checkChangeLicense(value string, res *Result) {
	lower := strings.ToLower(strings.TrimSpace(value))
	if lower == "" {
		res.block(CodeChangeLicenseGPL3, "the Change License parameter is empty")
		return
	}
	gpl := strings.Contains(lower, "general public license") || strings.Contains(lower, "gpl")
	version3 := strings.Contains(lower, "version 3") || strings.Contains(lower, "3.0") || strings.Contains(lower, "v3")
	if !gpl || !version3 {
		res.block(CodeChangeLicenseGPL3, "the Change License is %q, but this project is configured for the GNU General Public License version 3.0", firstLine(strings.TrimSpace(value)))
	}
}

func checkChangeDate(value string, res *Result) {
	block := strings.TrimSpace(value)
	if block == "" {
		res.block(CodeChangeDateMissing, "the Change Date parameter is empty")
		return
	}
	if containsPlaceholderMarker(block) {
		res.placeholder(CodeChangeDateMissing, "the Change Date is still a placeholder; a concrete calendar date for the released version must be recorded")
		return
	}
	// The Change Date must sit on the label line itself, so a missing or malformed
	// date cannot silently borrow the first-distribution date found later in the
	// block.
	head := block
	if i := strings.IndexByte(head, '\n'); i >= 0 {
		head = head[:i]
	}
	dates := dateRe.FindAllString(head, -1)
	if len(dates) == 0 {
		res.block(CodeChangeDateInvalid, "the Change Date does not contain a concrete calendar date on the \"Change Date:\" line (expected YYYY-MM-DD)")
		return
	}
	changeDate, err := time.Parse("2006-01-02", dates[0])
	if err != nil {
		res.block(CodeChangeDateInvalid, "the Change Date %q is not a valid calendar date", dates[0])
		return
	}
	match := firstDistRe.FindStringSubmatch(block)
	if match == nil {
		res.block(CodeChangeDateMaximum, "the Change Date does not record \"First public distribution of this version: YYYY-MM-DD\", so the four-year maximum cannot be verified")
		return
	}
	firstDist, err := time.Parse("2006-01-02", match[1])
	if err != nil {
		res.block(CodeChangeDateInvalid, "the recorded first public distribution date %q is not a valid calendar date", match[1])
		return
	}
	if firstDist.After(changeDate) {
		res.block(CodeChangeDateOrder, "the Change Date (%s) precedes the first public distribution (%s)", changeDate.Format("2006-01-02"), firstDist.Format("2006-01-02"))
	}
	if maximum := firstDist.AddDate(4, 0, 0); changeDate.After(maximum) {
		res.block(CodeChangeDateMaximum, "the Change Date (%s) is later than the four-year maximum (%s)", changeDate.Format("2006-01-02"), maximum.Format("2006-01-02"))
	}
}

// checkMetadataConsistency rejects licensing metadata that contradicts the
// LICENSE. It applies in both modes: an inconsistency is not a placeholder.
func checkMetadataConsistency(root, license string, res *Result) {
	title := licenseTitle(license)
	if title == "" {
		return
	}
	raw, err := os.ReadFile(filepath.Join(root, "internal", "branding", "branding.go"))
	if err != nil {
		return
	}
	match := licenseNameRe.FindStringSubmatch(string(raw))
	if match == nil {
		return
	}
	if match[1] != title {
		res.block(CodeMetadataInconsistent, "the application declares license %q but LICENSE states %q", match[1], title)
	}
}

func licenseTitle(license string) string {
	for _, line := range strings.Split(license, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "Business Source License ") {
			return trimmed
		}
	}
	return ""
}

func checkReleaseDir(dir, rootLicense string, res *Result) {
	shipped := filepath.Join(dir, "LICENSE")
	raw, err := os.ReadFile(shipped)
	if err != nil {
		res.block(CodePackageLicense, "the release package has no LICENSE beside the executable at %s", dir)
	} else if normalizeEOL(string(raw)) != normalizeEOL(rootLicense) {
		res.block(CodePackageLicenseMatch, "the LICENSE in the release package at %s does not match the repository LICENSE", dir)
	}
	notices := filepath.Join(dir, "THIRD_PARTY_NOTICES.md")
	if info, err := os.Stat(notices); err != nil || info.Size() == 0 {
		res.block(CodeNoticesMissing, "the release package has no THIRD_PARTY_NOTICES.md beside the executable at %s", dir)
	}
}

func scanPlaceholders(root string, res *Result) {
	for _, rel := range releaseFacingFiles {
		raw, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			continue
		}
		lower := strings.ToLower(string(raw))
		for _, marker := range placeholderMarkers {
			if strings.Contains(lower, marker) {
				res.placeholder(CodePlaceholder, "release-facing metadata %s still contains the unresolved marker %q", rel, marker)
				break
			}
		}
	}
}

func containsPlaceholderMarker(text string) bool {
	lower := strings.ToLower(text)
	if strings.ContainsAny(text, "<>") {
		return true
	}
	for _, marker := range placeholderMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func firstLine(text string) string {
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		return text[:i]
	}
	return text
}
