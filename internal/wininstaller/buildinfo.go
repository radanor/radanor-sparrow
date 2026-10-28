package wininstaller

import (
	"crypto/sha256"
	"debug/pe"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/radanor/radanor-shield/internal/branding"
	"github.com/radanor/radanor-shield/internal/winres"
)

// BuildInfoName is the record scripts/build-windows.ps1 writes beside the
// portable executable. It is the artifact's own statement of which build it is:
// the version the executable was built with, the variant that build path chose,
// the numeric form Windows stores, the commit the sources came from and the
// executable's checksum.
//
// The installer reads this record instead of deriving the version again from the
// working tree. That is deliberate. A version derived twice can disagree with
// itself: the Windows checkout materialises tracked text files with CRLF, and the
// dashboard build rewrites the committed assets in internal/gateway/assets with
// LF, which made a clean tree report `.dirty` only *after* the portable
// executable had already been built with the clean version. Packaging refused the
// mismatch, correctly. The fix is one identity, captured once, carried by the
// artifact rather than recomputed around it.
//
// The record cannot launder a stale executable either: it names the executable's
// checksum and its commit, and it is validated against the executable's own
// VERSIONINFO resource and against the checkout it is packaged in.
const BuildInfoName = "BUILD-INFO"

// binaryKey is the record's key for the payload executable's checksum.
const binaryKey = "binary." + PortableName

// The shapes a record must have. They are strict because every value reaches
// makensis as a define and an artifact name, and because a typo has to fail
// before an installer is produced rather than inside the installer.
var (
	versionShape  = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`)
	commitShape   = regexp.MustCompile(`^[0-9a-f]{40}$|^[0-9a-f]{64}$`)
	checksumShape = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// BuildInfo is a parsed BUILD-INFO record.
type BuildInfo struct {
	Version      string
	Variant      string
	Numeric      string
	Commit       string
	BinarySHA256 string
	Sequence     string
}

// ReadBuildInfo reads the record beside a portable executable.
func ReadBuildInfo(dir string) (BuildInfo, error) {
	path := filepath.Join(dir, BuildInfoName)
	data, err := os.ReadFile(path)
	if err != nil {
		return BuildInfo{}, fmt.Errorf("the portable build's identity record cannot be read: %w (build the executable with scripts/build-windows.ps1, which writes it)", err)
	}
	return ParseBuildInfo(data)
}

// ParseBuildInfo parses a record. Unknown and duplicate keys are errors: a
// record this package does not fully understand must not be accepted, because
// silently ignoring a key is how a packaging step starts relying on a field that
// was renamed away.
func ParseBuildInfo(data []byte) (BuildInfo, error) {
	text := strings.TrimPrefix(string(data), "\ufeff")
	// A Windows editor or PowerShell writes CRLF. The record is compared
	// field by field, so either line ending has to read the same.
	values := map[string]string{}
	for number, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			return BuildInfo{}, fmt.Errorf("%s line %d is not key=value: %q", BuildInfoName, number+1, line)
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		switch key {
		case "version", "variant", "numeric", "commit", "sequence", binaryKey:
		default:
			return BuildInfo{}, fmt.Errorf("%s line %d has unknown key %q", BuildInfoName, number+1, key)
		}
		if value == "" {
			return BuildInfo{}, fmt.Errorf("%s line %d gives %q no value", BuildInfoName, number+1, key)
		}
		if previous, duplicate := values[key]; duplicate {
			return BuildInfo{}, fmt.Errorf("%s names %q twice (%q and %q)", BuildInfoName, key, previous, value)
		}
		values[key] = value
	}

	info := BuildInfo{
		Version:      values["version"],
		Variant:      values["variant"],
		Numeric:      values["numeric"],
		Commit:       values["commit"],
		BinarySHA256: values[binaryKey],
		Sequence:     values["sequence"],
	}
	if err := info.Validate(); err != nil {
		return BuildInfo{}, err
	}
	return info, nil
}

// Validate reports every problem with a record, rather than the first, so a
// build log names all of them at once.
func (info BuildInfo) Validate() error {
	var problems []string
	if n, err := strconv.ParseUint(info.Sequence, 10, 31); err != nil || n == 0 {
		problems = append(problems, "sequence must be a positive git commit count")
	}
	switch {
	case info.Version == "":
		problems = append(problems, "version is missing")
	case !versionShape.MatchString(info.Version):
		problems = append(problems, fmt.Sprintf("version %q is not a version the build rule produces", info.Version))
	}

	switch info.Variant {
	case branding.VariantDevelopment, branding.VariantRelease:
	case "":
		problems = append(problems, "variant is missing")
	default:
		problems = append(problems, fmt.Sprintf("variant %q is neither %q nor %q", info.Variant, branding.VariantDevelopment, branding.VariantRelease))
	}

	switch {
	case info.Numeric == "":
		problems = append(problems, "numeric is missing")
	default:
		parts, ok := numericParts(info.Numeric)
		if !ok {
			problems = append(problems, fmt.Sprintf("numeric %q is not four numbers between 0 and 65535", info.Numeric))
		} else if info.Version != "" && versionShape.MatchString(info.Version) {
			want := branding.NumericVersionOf(info.Version)
			if parts != want {
				problems = append(problems, fmt.Sprintf("numeric %q does not describe version %q (%d.%d.%d.%d)", info.Numeric, info.Version, want[0], want[1], want[2], want[3]))
			}
		}
	}

	switch {
	case info.Commit == "":
		problems = append(problems, "commit is missing")
	case !commitShape.MatchString(info.Commit):
		problems = append(problems, fmt.Sprintf("commit %q is not a full git object name", info.Commit))
	}

	switch {
	case info.BinarySHA256 == "":
		problems = append(problems, binaryKey+" is missing")
	case !checksumShape.MatchString(info.BinarySHA256):
		problems = append(problems, fmt.Sprintf("%s %q is not a lowercase SHA-256", binaryKey, info.BinarySHA256))
	}

	if len(problems) > 0 {
		return fmt.Errorf("%s is not usable: %s", BuildInfoName, strings.Join(problems, "; "))
	}
	return nil
}

// numericParts parses the four 16-bit fields, and reports false for anything
// Windows could not store in them.
func numericParts(numeric string) ([4]uint16, bool) {
	fields := strings.Split(numeric, ".")
	if len(fields) != 4 {
		return [4]uint16{}, false
	}
	var parts [4]uint16
	for index, field := range fields {
		value, err := strconv.ParseUint(field, 10, 16)
		if err != nil {
			return [4]uint16{}, false
		}
		parts[index] = uint16(value)
	}
	return parts, true
}

// PortableInspection is what the payload executable says about itself, read from
// the executable rather than from the source tree.
type PortableInspection struct {
	Version   winres.VersionInfo
	SHA256    string
	Subsystem uint16
	Machine   uint16
}

// InspectPortable reads the resource tree and checksum of a built executable.
func InspectPortable(path string) (PortableInspection, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return PortableInspection{}, fmt.Errorf("cannot read %s: %w", filepath.Base(path), err)
	}
	sum := sha256.Sum256(data)
	entries, err := winres.ReadResources(path)
	if err != nil {
		return PortableInspection{}, fmt.Errorf("%s carries no readable resource tree: %w", filepath.Base(path), err)
	}
	var blob []byte
	for _, entry := range entries {
		if entry.Type == winres.ResourceVersion {
			blob = entry.Data
			break
		}
	}
	if blob == nil {
		return PortableInspection{}, fmt.Errorf("%s carries no version resource", filepath.Base(path))
	}
	version, err := winres.ParseVersionInfo(blob)
	if err != nil {
		return PortableInspection{}, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	file, err := pe.Open(path)
	if err != nil {
		return PortableInspection{}, fmt.Errorf("%s is not a PE executable: %w", filepath.Base(path), err)
	}
	defer file.Close()
	subsystem, ok := subsystemOf(file)
	if !ok {
		return PortableInspection{}, fmt.Errorf("%s has no PE optional header", filepath.Base(path))
	}
	return PortableInspection{
		Version:   version,
		SHA256:    hex.EncodeToString(sum[:]),
		Subsystem: subsystem,
		Machine:   file.Machine,
	}, nil
}

// VerifyPortable checks the payload executable against the record: the same
// bytes, the same version strings, the same numeric version, and the product
// identity every distribution form must carry.
func (info BuildInfo) VerifyPortable(path string) (PortableInspection, error) {
	inspection, err := InspectPortable(path)
	if err != nil {
		return inspection, err
	}
	return inspection, info.verify(inspection)
}

// verify is the record-versus-artifact rule, kept separate from reading the file
// so the rule itself is testable without a linked executable.
func (info BuildInfo) verify(inspection PortableInspection) error {
	var problems []string
	if inspection.Machine != pe.IMAGE_FILE_MACHINE_AMD64 {
		problems = append(problems, "P8 installer requires an amd64 portable payload")
	}
	if inspection.SHA256 != info.BinarySHA256 {
		problems = append(problems, fmt.Sprintf("the executable is %s, the record describes %s", shortHash(inspection.SHA256), shortHash(info.BinarySHA256)))
	}
	for _, key := range []string{"FileVersion", "ProductVersion"} {
		if got := inspection.Version.String(key); got != info.Version {
			problems = append(problems, fmt.Sprintf("%s is %q, the record says %q", key, got, info.Version))
		}
	}
	for _, key := range []string{"ProductName", "InternalName"} {
		if got := inspection.Version.String(key); got != branding.Product {
			problems = append(problems, fmt.Sprintf("%s is %q, want %q", key, got, branding.Product))
		}
	}
	if got := inspection.Version.String("OriginalFilename"); got != PortableName {
		problems = append(problems, fmt.Sprintf("OriginalFilename is %q, want %q", got, PortableName))
	}
	fixed := fmt.Sprintf("%d.%d.%d.%d", inspection.Version.FileVersion[0], inspection.Version.FileVersion[1], inspection.Version.FileVersion[2], inspection.Version.FileVersion[3])
	if fixed != info.Numeric {
		problems = append(problems, fmt.Sprintf("the fixed file version is %s, the record says %s", fixed, info.Numeric))
	}
	if inspection.Subsystem != pe.IMAGE_SUBSYSTEM_WINDOWS_GUI {
		problems = append(problems, fmt.Sprintf("the executable's subsystem is %d, want the Windows GUI subsystem", inspection.Subsystem))
	}
	if len(problems) > 0 {
		return fmt.Errorf("%s does not match %s for %s: %s", PortableName, BuildInfoName, info.Version, strings.Join(problems, "; "))
	}
	return nil
}

// VerifyCommit ties the artifact to the sources it is packaged from: the record
// names the commit, and the checkout must still be at it. This is what keeps a
// development installer from silently wrapping an executable built from another
// commit, without re-deriving the version from a tree the build itself has
// touched.
func (info BuildInfo) VerifyCommit(root string) error {
	out, err := exec.Command("git", "-C", root, "rev-parse", "HEAD").Output()
	if err != nil {
		return fmt.Errorf("cannot read the checkout's commit, so %s cannot be tied to these sources: %w", BuildInfoName, err)
	}
	head := strings.TrimSpace(string(out))
	if head != info.Commit {
		return fmt.Errorf("%s records commit %s but this checkout is at %s, so the executable was built from other sources", BuildInfoName, info.Commit, head)
	}
	count, err := exec.Command("git", "-C", root, "rev-list", "--count", "HEAD").Output()
	if err != nil || strings.TrimSpace(string(count)) != info.Sequence {
		return fmt.Errorf("%s sequence does not match this checkout's commit count", BuildInfoName)
	}
	return nil
}

// subsystemOf reports the Windows subsystem of either PE optional-header shape.
func subsystemOf(file *pe.File) (uint16, bool) {
	switch header := file.OptionalHeader.(type) {
	case *pe.OptionalHeader32:
		return header.Subsystem, true
	case *pe.OptionalHeader64:
		return header.Subsystem, true
	}
	return 0, false
}

// shortHash keeps a mismatch readable in a build log while still identifying
// which side is which.
func shortHash(hash string) string {
	if len(hash) <= 12 {
		return hash
	}
	return hash[:12] + "…"
}
