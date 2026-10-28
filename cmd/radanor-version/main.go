// Command radanor-version derives the product version from the checked-out
// commit, so a version is never edited into the source and a development build
// can always be told apart from an approved release build.
//
// It is the only implementation of the version rule. Both documented build
// scripts call it instead of parsing `git describe` themselves, for the same
// reason the licensing gate is Go rather than a shell script: the Linux and
// Windows build paths must not be able to disagree.
//
// The rule, in full:
//
//   - No version tag in the repository. The build is on the development line:
//     `0.1.0-dev.0+g<sha>`, because the untagged tree has no released number
//     to report and must never look like one.
//   - A `vX.Y.Z` tag is reachable but HEAD is not exactly on it, or the tree
//     has modifications: `X.Y.Z-dev.<commits-since-tag>+g<sha>`, with `.dirty`
//     appended to the build metadata when the tree is modified. Every path git
//     reports counts, tracked modifications and untracked files alike: a new
//     untracked source file is an uncommitted change, and an artifact built from
//     it must not present itself as a clean one. Ignored paths do not count —
//     `dist/`, `web/node_modules/`, `web/dist/` and the generated Windows
//     resource file are ignored, so the build's own output never marks the tree
//     dirty.
//   - HEAD is exactly on a clean `vX.Y.Z` tag: `X.Y.Z`. This is the only shape
//     that can be a release, and even then `-release` is required for the
//     variant to say so — a label the release build path sets only after the
//     licensing release gate has passed.
//
// It also owns the timestamp rule that makes an artifact reproducible
// (`-field epoch`, `-field epoch-source`), so the Linux build and the AppImage
// packaging step cannot pick different source dates any more than they can pick
// different versions.
//
// Read-only: it runs only `git rev-parse`, `git status`, `git tag`,
// `git describe`, `git rev-list` and `git show`, reaches no network, and changes
// nothing.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/radanor/radanor-shield/internal/branding"
)

// versionTag matches the released forms only. A tag such as `v0.2.0-rc1` is
// deliberately not accepted here rather than being parsed loosely; an
// unrecognised tag leaves the build on the development line, which is the safe
// direction to be wrong in.
var versionTag = regexp.MustCompile(`^v([0-9]+)\.([0-9]+)\.([0-9]+)$`)

func main() {
	dir := flag.String("dir", ".", "repository directory to read the git state from")
	release := flag.Bool("release", false, "require an exact, clean version tag and report the release variant")
	field := flag.String("field", "version", "which value to print: version, variant, epoch or epoch-source")
	record := flag.Bool("record", false, "print artifact identity for Windows packaging")
	flag.Parse()

	switch *field {
	case "version", "variant":
	case "epoch", "epoch-source":
		epoch, source, err := sourceDateEpoch(*dir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "radanor-version: %v\n", err)
			os.Exit(2)
		}
		if *field == "epoch" {
			fmt.Println(epoch)
		} else {
			fmt.Println(source)
		}
		return
	default:
		fmt.Fprintf(os.Stderr, "radanor-version: unknown field %q (expected version, variant, epoch or epoch-source)\n", *field)
		os.Exit(1)
	}

	state := readGit(*dir)
	if *release && !state.releaseable {
		fmt.Fprintf(os.Stderr, "radanor-version: refusing a release version: %s\n", state.reason)
		fmt.Fprintln(os.Stderr, "A public release must be built from a clean checkout of an exact `vX.Y.Z` tag. The licensing release gate must also pass; this check does not replace it.")
		os.Exit(2)
	}
	if *record {
		commit, err := exec.Command("git", "-C", *dir, "rev-parse", "HEAD").Output()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		sequence, err := exec.Command("git", "-C", *dir, "rev-list", "--count", "HEAD").Output()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		variant := branding.VariantDevelopment
		if state.releaseable && *release {
			variant = branding.VariantRelease
		}
		n := branding.NumericVersionOf(state.version)
		fmt.Printf("version=%s\nvariant=%s\nnumeric=%d.%d.%d.%d\ncommit=%s\nsequence=%s\n", state.version, variant, n[0], n[1], n[2], n[3], strings.TrimSpace(string(commit)), strings.TrimSpace(string(sequence)))
		return
	}

	if *field == "variant" {
		if state.releaseable && *release {
			fmt.Println(branding.VariantRelease)
			return
		}
		fmt.Println(branding.VariantDevelopment)
		return
	}
	fmt.Println(state.version)
}

type gitState struct {
	version     string
	releaseable bool
	reason      string
}

func readGit(dir string) gitState {
	if _, err := exec.Command("git", "-C", dir, "rev-parse", "--git-dir").Output(); err != nil {
		return gitState{
			version: branding.DevelopmentVersion,
			reason:  "the working tree is not a git checkout, so no commit is identified",
		}
	}

	sha := "unknown"
	if out, err := exec.Command("git", "-C", dir, "rev-parse", "--short=12", "HEAD").Output(); err == nil {
		if trimmed := strings.TrimSpace(string(out)); trimmed != "" {
			sha = trimmed
		}
	}

	dirty := false
	// --untracked-files=normal, not =no: an untracked source file is an
	// uncommitted change. Ignored paths are never reported, so the build's own
	// dist/ output and the generated Windows resource file stay invisible here.
	if out, err := exec.Command("git", "-C", dir, "status", "--porcelain", "--untracked-files=normal").Output(); err == nil {
		dirty = strings.TrimSpace(string(out)) != ""
	}

	tag, distance, tagged := nearestVersionTag(dir)
	switch {
	case !tagged:
		// Nothing has been released from this repository, so there is no
		// released number to report: stay explicitly on the development line.
		return gitState{
			version: developmentVersion(sha, dirty),
			reason:  "no `vX.Y.Z` tag exists, so the build cannot report a released version",
		}
	case distance == 0 && !dirty:
		return gitState{
			version:     tag,
			releaseable: true,
		}
	default:
		return gitState{
			version: developmentVersionFor(tag, distance, sha, dirty),
			reason:  "v" + tag + " is the closest version tag, but " + deviation(distance, dirty),
		}
	}
}

// deviation explains, in the words the build log will show, why HEAD does not
// qualify as a release.
func deviation(distance int, dirty bool) string {
	switch {
	case distance == 0 && dirty:
		return "that tag is checked out with uncommitted changes on top of it"
	case distance == 0:
		return "HEAD is not on it"
	case dirty:
		return "HEAD is " + strconv.Itoa(distance) + " commits past it, with uncommitted changes"
	default:
		return "HEAD is " + strconv.Itoa(distance) + " commits past it"
	}
}

// nearestVersionTag returns the closest reachable `vX.Y.Z` tag, how many commits
// separate it from HEAD, and whether any such tag was found.
func nearestVersionTag(dir string) (string, int, bool) {
	// An exact tag at HEAD wins outright: it is the only release shape.
	if out, err := exec.Command("git", "-C", dir, "tag", "--points-at", "HEAD").Output(); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if match := versionTag.FindStringSubmatch(strings.TrimSpace(line)); match != nil {
				return match[1] + "." + match[2] + "." + match[3], 0, true
			}
		}
	}

	out, err := exec.Command("git", "-C", dir, "describe", "--tags", "--match", "v[0-9]*", "--abbrev=0").Output()
	if err != nil {
		return "", 0, false
	}
	match := versionTag.FindStringSubmatch(strings.TrimSpace(string(out)))
	if match == nil {
		return "", 0, false
	}
	tag := match[1] + "." + match[2] + "." + match[3]

	distance := 0
	if counted, err := exec.Command("git", "-C", dir, "rev-list", "--count", strings.TrimSpace(string(out))+"..HEAD").Output(); err == nil {
		if value, err := strconv.Atoi(strings.TrimSpace(string(counted))); err == nil && value >= 0 {
			distance = value
		}
	}
	return tag, distance, true
}

// lastEpoch is 9999-12-31T23:59:59Z, the last second representable with a
// four-digit year. Anything beyond it is far more likely to be a millisecond
// timestamp passed by mistake than a real source date.
const lastEpoch = 253402300799

// sourceDateEpoch returns the timestamp every generated file in an artifact is
// given, and a word naming where that value came from so the artifact can record
// it. The rule, in order:
//
//   - SOURCE_DATE_EPOCH, when set and non-empty. This is the cross-project
//     reproducible-builds variable, so a distribution or release engineer can
//     pin the timestamp without editing the build.
//   - Otherwise the committer timestamp of HEAD. It belongs to the commit, so
//     every checkout of that commit produces the same value, which is the whole
//     point: the clock of the machine that happens to run the build must never
//     reach the artifact.
//   - Otherwise 0, Unix epoch, for a directory that is not a git checkout. It is
//     a fixed value rather than "now" precisely because "now" cannot be
//     reproduced; the artifact records `fixed-epoch` so the weaker provenance is
//     visible rather than implied.
//
// The value is deliberately not the build time, the file mtime or anything else
// that moves between two builds of the same source.
func sourceDateEpoch(dir string) (int64, string, error) {
	if raw := strings.TrimSpace(os.Getenv("SOURCE_DATE_EPOCH")); raw != "" {
		value, err := parseEpoch(raw)
		if err != nil {
			return 0, "", fmt.Errorf("SOURCE_DATE_EPOCH=%q: %w", raw, err)
		}
		return value, "environment", nil
	}
	if out, err := exec.Command("git", "-C", dir, "show", "-s", "--format=%ct", "HEAD").Output(); err == nil {
		if value, err := parseEpoch(strings.TrimSpace(string(out))); err == nil {
			return value, "git-commit", nil
		}
	}
	return 0, "fixed-epoch", nil
}

func parseEpoch(raw string) (int64, error) {
	if raw == "" {
		return 0, errors.New("is empty")
	}
	for _, digit := range raw {
		if digit < '0' || digit > '9' {
			return 0, errors.New("is not a whole number of seconds since 1 January 1970")
		}
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, errors.New("is not a whole number of seconds since 1 January 1970")
	}
	if value > lastEpoch {
		return 0, errors.New("is later than 9999-12-31; a millisecond timestamp was probably meant")
	}
	return value, nil
}

func developmentVersion(sha string, dirty bool) string {
	return metadata(branding.DevelopmentBase+"-dev.0", sha, dirty)
}

func developmentVersionFor(tag string, distance int, sha string, dirty bool) string {
	return metadata(tag+"-dev."+strconv.Itoa(distance), sha, dirty)
}

// metadata appends SemVer build metadata. The identifier stays alphanumeric
// with hyphens and dots so the result is a valid SemVer version and a safe
// Windows version-resource string.
func metadata(core, sha string, dirty bool) string {
	build := "g" + sha
	if dirty {
		build += ".dirty"
	}
	return core + "+" + build
}
