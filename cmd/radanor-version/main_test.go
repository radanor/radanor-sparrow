package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/radanor/radanor-shield/internal/branding"
)

// The version rule is exercised against real repositories rather than a mocked
// description of one, because the failure it guards against — a development
// build presenting itself as a released version, or a release built from a
// modified tree — depends on the actual git state.
//
// Requires the git command. Skips, rather than fails, where it is unavailable.
func TestVersionRuleAgainstRealRepositories(t *testing.T) {
	requireGit(t)

	t.Run("no tags leaves the build on the development line", func(t *testing.T) {
		repo := newRepo(t)
		commit(t, repo, "first")

		state := readGit(repo)
		if state.releaseable {
			t.Error("a repository with no version tag reported itself as releaseable")
		}
		assertVersionShape(t, state.version, branding.DevelopmentBase+"-dev.0+g", false)
	})

	t.Run("a modified tree is marked and is never releaseable", func(t *testing.T) {
		repo := newRepo(t)
		commit(t, repo, "first")
		tag(t, repo, "v1.2.3")
		commit(t, repo, "second")
		if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("modified\n"), 0600); err != nil {
			t.Fatal(err)
		}

		state := readGit(repo)
		if state.releaseable {
			t.Error("a modified tree reported itself as releaseable")
		}
		assertVersionShape(t, state.version, "1.2.3-dev.1+g", true)
	})

	// An artifact must never present a tree with a new, untracked source file as
	// a clean one: the file is part of what was built even though git never
	// committed it.
	t.Run("an untracked source file is a modification", func(t *testing.T) {
		repo := newRepo(t)
		commit(t, repo, "first")
		tag(t, repo, "v1.5.0")
		if err := os.WriteFile(filepath.Join(repo, "added.go"), []byte("package main\n"), 0600); err != nil {
			t.Fatal(err)
		}

		state := readGit(repo)
		if state.releaseable {
			t.Error("a tree with an untracked source file reported itself as releaseable")
		}
		if !strings.HasSuffix(state.version, ".dirty") {
			t.Errorf("version = %q: an untracked source file did not mark the build dirty", state.version)
		}
	})

	// The build's own output is ignored, so it must not make every build dirty.
	t.Run("ignored build output is not a modification", func(t *testing.T) {
		repo := newRepo(t)
		if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("dist/\n"), 0600); err != nil {
			t.Fatal(err)
		}
		gitRun(t, repo, "add", ".gitignore")
		gitRun(t, repo, "commit", "-q", "-m", "ignore build output")
		tag(t, repo, "v1.6.0")
		if err := os.MkdirAll(filepath.Join(repo, "dist", "linux"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo, "dist", "linux", "radanor-sparrow"), []byte("binary\n"), 0700); err != nil {
			t.Fatal(err)
		}

		state := readGit(repo)
		if !state.releaseable {
			t.Fatalf("ignored build output made a clean tag unreleaseable: %s", state.reason)
		}
		if state.version != "1.6.0" {
			t.Errorf("version = %q, want 1.6.0", state.version)
		}
	})

	t.Run("commits past the tag count into the version", func(t *testing.T) {
		repo := newRepo(t)
		commit(t, repo, "first")
		tag(t, repo, "v2.0.1")
		commit(t, repo, "second")
		commit(t, repo, "third")

		state := readGit(repo)
		if state.releaseable {
			t.Error("commits past the tag reported themselves as releaseable")
		}
		assertVersionShape(t, state.version, "2.0.1-dev.2+g", false)
	})

	t.Run("a clean checkout of an exact version tag is releaseable", func(t *testing.T) {
		repo := newRepo(t)
		commit(t, repo, "first")
		tag(t, repo, "v1.4.0")

		state := readGit(repo)
		if !state.releaseable {
			t.Fatalf("a clean exact version tag was not releaseable: %s", state.reason)
		}
		if state.version != "1.4.0" {
			t.Errorf("release version = %q, want %q (the leading v is not part of a SemVer version)", state.version, "1.4.0")
		}
	})

	t.Run("a prerelease-shaped tag does not make a release", func(t *testing.T) {
		repo := newRepo(t)
		commit(t, repo, "first")
		tag(t, repo, "v0.2.0-rc1")

		state := readGit(repo)
		if state.releaseable {
			t.Errorf("an unrecognised tag %q made the build releaseable; it must stay on the development line", "v0.2.0-rc1")
		}
	})

	t.Run("a directory that is not a git checkout still yields a development version", func(t *testing.T) {
		state := readGit(t.TempDir())
		if state.releaseable {
			t.Error("a non-repository reported itself as releaseable")
		}
		if state.version != branding.DevelopmentVersion {
			t.Errorf("version = %q, want %q", state.version, branding.DevelopmentVersion)
		}
	})
}

// The generated identifiers must stay valid SemVer, because they are written
// into the Windows version resource and used in artifact names.
func assertVersionShape(t *testing.T, version, prefix string, dirty bool) {
	t.Helper()
	if !strings.HasPrefix(version, prefix) {
		t.Fatalf("version = %q, want it to start with %q", version, prefix)
	}
	if _, build, found := strings.Cut(version, "+"); !found || build == "" {
		t.Errorf("version = %q has no build metadata identifying the commit", version)
	}
	if got := strings.HasSuffix(version, ".dirty"); got != dirty {
		t.Errorf("version = %q: dirty suffix = %v, want %v", version, got, dirty)
	}
	for _, bad := range []string{"..", "+g+", " ", "\t", "_"} {
		if strings.Contains(version, bad) {
			t.Errorf("version = %q contains %q, which is not valid SemVer metadata", version, bad)
		}
	}
	if strings.HasPrefix(version, "v") {
		t.Errorf("version = %q keeps the tag's leading v, which is not part of a SemVer version", version)
	}
}

// Two builds of one commit have to agree on every timestamp they write, so the
// value and its provenance are both pinned here. The environments are real: a
// repository, a variable, and a directory that is neither.
func TestSourceDateEpochProvenance(t *testing.T) {
	requireGit(t)

	t.Run("the environment wins", func(t *testing.T) {
		repo := newRepo(t)
		commit(t, repo, "first")
		t.Setenv("SOURCE_DATE_EPOCH", "1700000000")

		value, source, err := sourceDateEpoch(repo)
		if err != nil {
			t.Fatal(err)
		}
		if value != 1700000000 || source != "environment" {
			t.Errorf("epoch = %d from %q, want 1700000000 from environment", value, source)
		}
	})

	t.Run("a blank variable is unset, not an error", func(t *testing.T) {
		repo := newRepo(t)
		t.Setenv("GIT_AUTHOR_DATE", "2001-02-03T04:05:06+0000")
		t.Setenv("GIT_COMMITTER_DATE", "2001-02-03T04:05:06+0000")
		commit(t, repo, "first")
		t.Setenv("SOURCE_DATE_EPOCH", "")

		value, source, err := sourceDateEpoch(repo)
		if err != nil {
			t.Fatal(err)
		}
		if value != 981173106 || source != "git-commit" {
			t.Errorf("epoch = %d from %q, want the commit timestamp 981173106 from git-commit", value, source)
		}
	})

	t.Run("a value that cannot be a source date is refused", func(t *testing.T) {
		repo := newRepo(t)
		commit(t, repo, "first")
		for _, raw := range []string{"yesterday", "-1", "1.5", "1700000000000", "253402300800"} {
			t.Setenv("SOURCE_DATE_EPOCH", raw)
			if _, _, err := sourceDateEpoch(repo); err == nil {
				t.Errorf("SOURCE_DATE_EPOCH=%q was accepted", raw)
			}
		}
		t.Setenv("SOURCE_DATE_EPOCH", "253402300799")
		if value, _, err := sourceDateEpoch(repo); err != nil || value != lastEpoch {
			t.Errorf("the last representable second was refused: %d, %v", value, err)
		}
	})

	t.Run("a directory outside git falls back to a fixed epoch", func(t *testing.T) {
		t.Setenv("SOURCE_DATE_EPOCH", "")
		value, source, err := sourceDateEpoch(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if value != 0 || source != "fixed-epoch" {
			t.Errorf("epoch = %d from %q, want 0 from fixed-epoch rather than the build clock", value, source)
		}
	})
}

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	base := []string{"-c", "user.email=test@example.invalid", "-c", "user.name=Radanor Test", "-c", "commit.gpgsign=false", "-c", "tag.gpgsign=false", "-C", dir}
	command := exec.Command("git", append(base, args...)...)
	command.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %s failed: %v: %s", strings.Join(args, " "), err, out)
	}
}

func newRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitRun(t, dir, "init", "-q")
	gitRun(t, dir, "config", "core.autocrlf", "false")
	return dir
}

func commit(t *testing.T, dir, message string) {
	t.Helper()
	path := filepath.Join(dir, "tracked.txt")
	content, err := os.ReadFile(path)
	if err != nil {
		content = nil
	}
	if err := os.WriteFile(path, append(content, []byte(message+"\n")...), 0600); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "add", "tracked.txt")
	gitRun(t, dir, "commit", "-q", "-m", message)
}

func tag(t *testing.T, dir, name string) {
	t.Helper()
	gitRun(t, dir, "tag", name)
}
