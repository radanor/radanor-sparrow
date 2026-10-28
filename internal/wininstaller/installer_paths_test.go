package wininstaller

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// These tests are about how a path reaches makensis, not about what it says.
//
// The Windows CI failure they exist for: the .nsi appended "/RadanorSparrow.exe"
// to a directory produced by `cygpath -w`, so makensis was handed
//
//	D:\a\radanor-shield\radanor-shield\dist\windows/RadanorSparrow.exe
//
// and reported "no files found" for a file that was there and already verified.
// NSIS resolves a File path by splitting it at the platform separator to find the
// name portion, so that mixed path named `windows/RadanorSparrow.exe` inside
// `dist`.
//
// scripts/nsis-paths.sh holds the one implementation of the rule that avoids the
// whole class: whole, absolute paths, in the host's own separators, never joined
// inside the installer script. These tests run that file with a stand-in for Git
// Bash's cygpath, so the Windows form is exercised on a Linux host too.

// bashForTests returns the shell that runs scripts/nsis-paths.sh, or skips when
// the host has none. Bash is a real prerequisite rather than a test detail: the
// installer build is `bash scripts/build-windows-installer.sh` on both runners,
// and the helper uses bash's own string handling.
func bashForTests(t *testing.T) string {
	t.Helper()
	if found, err := exec.LookPath("bash"); err == nil {
		return found
	}
	t.Skip("bash is not available, so the NSIS path helpers cannot be exercised")
	return ""
}

func nsisPathsHelper(t *testing.T) string {
	t.Helper()
	return filepath.Join(repoRoot(t), "scripts", "nsis-paths.sh")
}

// buildTestProgram compiles a one-file Go program in dir and returns its path.
// The stand-ins are compiled rather than written as shell scripts so they run
// unchanged on the Windows runner, where a shebang naming an interpreter under a
// path with spaces does not.
func buildTestProgram(t *testing.T, dir, name, source string) string {
	t.Helper()
	sourceFile := filepath.Join(dir, name+".go")
	if err := os.WriteFile(sourceFile, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	binary := filepath.Join(dir, name)
	if out, err := exec.Command("go", "build", "-o", binary, sourceFile).CombinedOutput(); err != nil {
		t.Fatalf("build the %s stand-in: %v\n%s", name, err, out)
	}
	return binary
}

// fakeMakensis answers the compiler's version probe. The tests that use it reach
// the checks that run before any compilation.
func fakeMakensis(t *testing.T) string {
	t.Helper()
	return buildTestProgram(t, t.TempDir(), "makensis", `package main

import "fmt"

func main() { fmt.Println("v3.13") }
`)
}

// fakeCygpath compiles a stand-in for Git Bash's `cygpath -w`, mapping
// `/d/a/repo` to `D:\a\repo` as the real one does and putting any other POSIX
// path on the system drive (`/tmp/t` -> `C:\tmp\t`).
//
// It is a compiled program rather than a shell script so the same stand-in works
// on the Windows runner, where a shebang naming an interpreter under a path with
// spaces does not run. The directory it returns is meant to be the whole PATH of
// the helper: every command the helper itself uses is a shell builtin.
func fakeCygpath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	program := `package main

import (
	"fmt"
	"os"
	"strings"
)

func main() {
	var path string
	for _, argument := range os.Args[1:] {
		switch argument {
		case "-w", "--":
		default:
			path = argument
		}
	}
	if path == "" {
		fmt.Fprintln(os.Stderr, "the NSIS build only uses cygpath -w <path>")
		os.Exit(2)
	}
	if !strings.HasPrefix(path, "/") {
		fmt.Println(path)
		return
	}
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	drive := "C"
	if len(parts[0]) == 1 {
		drive = strings.ToUpper(parts[0])
		parts = parts[1:]
	}
	fmt.Printf("%s:\\%s\n", drive, strings.Join(parts, "\\"))
}
`
	buildTestProgram(t, dir, "cygpath", program)
	return dir
}

// pathHelpers resolves a path and validates the result the way the build does,
// then reports both, under the given PATH.
func pathHelpers(t *testing.T, shell, path string, env []string) (native, problem string) {
	t.Helper()
	command := exec.Command(shell, "-c", `export PATH="$RADANOR_PATH_TEST_PATH"
. "$1" || exit 3
value=$(nsis_native_path "$RADANOR_PATH_TEST_INPUT") || exit 4
printf 'native=%s\n' "$value"
printf 'problem=%s\n' "$(nsis_path_problem "$value")"
`, "bash", nsisPathsHelper(t))
	command.Env = pathTestEnvironment(env, path)
	out, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("resolve %q: %v\n%s", path, err, out)
	}
	for _, line := range strings.Split(strings.ReplaceAll(string(out), "\r\n", "\n"), "\n") {
		switch {
		case strings.HasPrefix(line, "native="):
			native = strings.TrimPrefix(line, "native=")
		case strings.HasPrefix(line, "problem="):
			problem = strings.TrimPrefix(line, "problem=")
		}
	}
	if native == "" {
		t.Fatalf("the path helper printed no value for %q:\n%s", path, out)
	}
	return native, problem
}

// problemFor asks the rule about a value directly, the way the build does before
// it hands the value to makensis.
func problemFor(t *testing.T, shell, value string, env []string) string {
	t.Helper()
	command := exec.Command(shell, "-c", `export PATH="$RADANOR_PATH_TEST_PATH"; . "$1" || exit 3; nsis_path_problem "$RADANOR_PATH_TEST_INPUT"`, "bash", nsisPathsHelper(t))
	command.Env = pathTestEnvironment(env, value)
	out, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("validate %q: %v\n%s", value, err, out)
	}
	return strings.TrimSpace(string(out))
}

func pathTestEnvironment(environment []string, input string) []string {
	path := ""
	for _, entry := range environment {
		if strings.HasPrefix(entry, "PATH=") {
			path = strings.TrimPrefix(entry, "PATH=")
		}
	}
	// Bash/MSYS synthesizes PATH on startup. Set it inside the shell, and carry
	// hostile quote/control-character fixtures through env rather than argv.
	path = filepath.ToSlash(path)
	if len(path) > 2 && path[1] == ':' {
		path = "/" + strings.ToLower(path[:1]) + path[2:]
	}
	result := append(os.Environ(), environment...)
	return append(result, "RADANOR_PATH_TEST_PATH="+path, "RADANOR_PATH_TEST_INPUT="+input, "MSYS2_ARG_CONV_EXCL=*")
}

// An empty PATH is what a host without cygpath looks like to the helper, and
// every command it uses is a shell builtin.
func linuxEnvironment() []string {
	return []string{"PATH="}
}

// gitBashEnvironment is the helper's view of the windows-2025 runner: cygpath is
// present, so the NSIS paths are Windows paths.
func gitBashEnvironment(t *testing.T) []string {
	t.Helper()
	return []string{"PATH=" + fakeCygpath(t)}
}

// lastComponent is the part NSIS keeps as the file name: it splits a File path at
// the platform separator, so whatever follows the final native separator is what
// makensis looks for inside the directory before it.
func lastComponent(path string, separator rune) string {
	if index := strings.LastIndex(path, string(separator)); index >= 0 {
		return path[index+1:]
	}
	return path
}

// The rule the build follows on each host: Linux keeps the POSIX path, Git Bash
// gets a native `C:\...` path, and both keep one separator convention with the
// file name as the last component. Directory names with spaces are part of a
// checkout, so they have to survive unchanged.
func TestPayloadPathsReachMakensisInTheHostsOwnForm(t *testing.T) {
	shell := bashForTests(t)
	windows := gitBashEnvironment(t)
	linux := linuxEnvironment()
	cases := []struct {
		name      string
		env       []string
		path      string
		want      string
		separator rune
	}{
		{
			name:      "Linux checkout",
			env:       linux,
			path:      "/home/runner/radanor-shield/dist/windows/RadanorSparrow.exe",
			want:      "/home/runner/radanor-shield/dist/windows/RadanorSparrow.exe",
			separator: '/',
		},
		{
			name:      "Linux checkout in a directory with spaces",
			env:       linux,
			path:      "/home/runner/my repo/radanor shield/dist/windows/RadanorSparrow.exe",
			want:      "/home/runner/my repo/radanor shield/dist/windows/RadanorSparrow.exe",
			separator: '/',
		},
		{
			name:      "Git Bash on the windows runner",
			env:       windows,
			path:      "/d/a/radanor-shield/radanor-shield/dist/windows/RadanorSparrow.exe",
			want:      `D:\a\radanor-shield\radanor-shield\dist\windows\RadanorSparrow.exe`,
			separator: '\\',
		},
		{
			name:      "Git Bash in a directory with spaces",
			env:       windows,
			path:      "/d/a/my repo/radanor shield/dist/windows/RadanorSparrow.exe",
			want:      `D:\a\my repo\radanor shield\dist\windows\RadanorSparrow.exe`,
			separator: '\\',
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			native, problem := pathHelpers(t, shell, testCase.path, testCase.env)
			if problem != "" {
				t.Fatalf("the build would refuse a usable path: %s (got %q)", problem, native)
			}
			if native != testCase.want {
				t.Errorf("makensis would receive %q, want %q", native, testCase.want)
			}
			if got := lastComponent(native, testCase.separator); got != PortableName {
				t.Errorf("NSIS would look for %q in %q instead of %q", got, native, PortableName)
			}
			if strings.ContainsRune(native, '/') && strings.ContainsRune(native, '\\') {
				t.Errorf("makensis would receive mixed separators: %q", native)
			}
		})
	}
}

// The exact path from the failing Windows run. It is what the old .nsi produced
// by appending "/RadanorSparrow.exe" to a `cygpath -w` directory, and it is why
// makensis reported "no files found": the name portion after the final native
// separator is not the executable.
func TestThePathFromTheFailingRunIsRefused(t *testing.T) {
	shell := bashForTests(t)
	const failing = `D:\a\radanor-shield\radanor-shield\dist\windows/RadanorSparrow.exe`
	if got := lastComponent(failing, '\\'); got == PortableName {
		t.Fatalf("this test no longer reproduces the failure: NSIS would look for %q", got)
	} else if got != "windows/RadanorSparrow.exe" {
		t.Fatalf("the failing path yields the name portion %q, want the mixed tail", got)
	}
	if problem := problemFor(t, shell, failing, linuxEnvironment()); problem == "" {
		t.Error("the build would hand makensis the mixed-separator path that failed on Windows")
	}
}

// Values that cannot survive the trip into an NSIS string, or that NSIS would
// resolve against the wrong directory, are refused before the compiler runs.
func TestUnusablePathsAreRefusedBeforeMakensis(t *testing.T) {
	shell := bashForTests(t)
	linux := linuxEnvironment()
	for name, value := range map[string]string{
		"empty":                       "",
		"relative to the script":      `dist/windows/RadanorSparrow.exe`,
		"drive relative":              `C:dist\windows\RadanorSparrow.exe`,
		"mixed separators":            `D:\a\repo\dist\windows/RadanorSparrow.exe`,
		"mixed separators and spaces": `D:\a\my repo\dist\windows/RadanorSparrow.exe`,
		"double quote in the path":    `D:\a\re"po\RadanorSparrow.exe`,
		"NSIS variable in the path":   `D:\a\$TEMP\RadanorSparrow.exe`,
		"line break in the path":      "D:\\a\\repo\nRadanorSparrow.exe",
		"carriage return in the path": "D:\\a\\repo\rRadanorSparrow.exe",
		"tab in the path":             "D:\\a\\repo\tRadanorSparrow.exe",
	} {
		t.Run(name, func(t *testing.T) {
			if problem := problemFor(t, shell, value, linux); problem == "" {
				t.Fatalf("the build would hand makensis %q", value)
			}
		})
	}
	// The positive control: the same shapes with one convention are accepted, so
	// the rejections above are about the defect rather than a function that
	// always objects.
	for name, value := range map[string]string{
		"native Windows path":     `D:\a\repo\dist\windows\RadanorSparrow.exe`,
		"native UNC path":         `\\server\share\repo\dist\windows\RadanorSparrow.exe`,
		"native POSIX path":       "/home/runner/radanor-shield/dist/windows/RadanorSparrow.exe",
		"POSIX path with a space": `/home/runner/my repo/dist/windows/RadanorSparrow.exe`,
	} {
		t.Run("accepted "+name, func(t *testing.T) {
			if problem := problemFor(t, shell, value, linux); problem != "" {
				t.Errorf("a usable path was refused: %s (%q)", problem, value)
			}
		})
	}

	// On a host with cygpath, makensis is a Windows program: a drive path with
	// forward slashes is what `cygpath -m` would produce, and forward slashes are
	// the form the failing run proved NSIS does not resolve there. Refusing it
	// also means a future switch from -w to -m fails the build instead of CI.
	t.Run("forward slashes on a Windows host", func(t *testing.T) {
		windows := gitBashEnvironment(t)
		if problem := problemFor(t, shell, `D:/a/repo/dist/windows/RadanorSparrow.exe`, windows); problem == "" {
			t.Error("the build would hand a Windows makensis a forward-slash path")
		}
		if problem := problemFor(t, shell, `D:\a\repo\dist\windows\RadanorSparrow.exe`, windows); problem != "" {
			t.Errorf("a native Windows path was refused on a Windows host: %s", problem)
		}
	})
}

// Every payload file the installer carries has to resolve through the same rule,
// including when the checkout sits in a directory with spaces. The Linux form is
// checked against the filesystem; the Git Bash form is checked for the name
// portion NSIS will look up, which is the property that failed on Windows.
func TestEveryPayloadPathResolves(t *testing.T) {
	shell := bashForTests(t)
	payload := filepath.Join(t.TempDir(), "my repo", "radanor shield", "dist", "windows")
	if err := os.MkdirAll(payload, 0o755); err != nil {
		t.Fatal(err)
	}
	names := []string{PortableName, "LICENSE", "THIRD_PARTY_NOTICES.md"}
	contents := map[string]string{}
	for _, name := range names {
		contents[name] = "payload: " + name + "\n"
		if err := os.WriteFile(filepath.Join(payload, name), []byte(contents[name]), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// One stand-in for both halves of the test, so the Windows form comes from
	// the same conversion throughout.
	windows := gitBashEnvironment(t)
	for _, name := range names {
		posix := filepath.Join(payload, name)
		t.Run(name, func(t *testing.T) {
			native, problem := pathHelpers(t, shell, posix, linuxEnvironment())
			if problem != "" {
				t.Fatalf("the build would refuse %s: %s", posix, problem)
			}
			if native != posix {
				t.Fatalf("makensis would receive %q for %q", native, posix)
			}
			info, err := os.Stat(native)
			if err != nil {
				t.Fatalf("the path the build would pass does not resolve: %v", err)
			}
			if info.Size() != int64(len(contents[name])) {
				t.Errorf("%s resolved to a different file", native)
			}

			converted, problem := pathHelpers(t, shell, posix, windows)
			if problem != "" {
				t.Fatalf("the build would refuse the Windows form of %s: %s", posix, problem)
			}
			if got := lastComponent(converted, '\\'); got != name {
				t.Errorf("NSIS would look for %q instead of %q in %q", got, name, converted)
			}
			if strings.ContainsRune(converted, '/') {
				t.Errorf("the Windows path still contains a forward slash: %q", converted)
			}
		})
	}
}

// The build script has to use that rule for every path it passes, and it has to
// check the files exist immediately before the compiler runs, so a missing file
// is reported here rather than as makensis's "no files found".
func TestBuildScriptPassesWholePathsAndChecksThemFirst(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "scripts", "build-windows-installer.sh"))
	if err != nil {
		t.Fatal(err)
	}
	script := string(raw)
	if !strings.Contains(script, `. "$repo/scripts/nsis-paths.sh"`) {
		t.Error("the build script does not source scripts/nsis-paths.sh, so its path rule is a second implementation")
	}
	invocation := strings.Index(script, `"$makensis" -V2`)
	if invocation < 0 {
		t.Fatal("the build script does not invoke makensis")
	}
	for _, define := range []string{"PAYLOAD_EXE", "PAYLOAD_LICENSE", "PAYLOAD_NOTICES", "APP_ICON", "OUTFILE"} {
		at := strings.Index(script, "deffile "+define+" ")
		if at < 0 {
			t.Errorf("the build script does not pass %s through the path rule", define)
			continue
		}
		if at > invocation {
			t.Errorf("the build script passes %s after makensis has already been invoked", define)
		}
	}
	if !strings.Contains(script, `"${deffiles[@]}"`) {
		t.Error("the build script does not pass the checked paths to makensis")
	}
	for _, stale := range []string{"-DPAYLOAD_DIR", "winpath"} {
		if strings.Contains(script, stale) {
			t.Errorf("the build script still uses %q; each payload file is passed as its own absolute path", stale)
		}
	}
}

// The three payload paths are checked before makensis runs, so a payload that
// disappears between the identity check and the compile is reported by name
// rather than as NSIS's "no files found".
func TestBuildScriptRefusesAPayloadWithoutTheExecutable(t *testing.T) {
	shell := bashForTests(t)
	// The missing-file branch is reached before any Go or makensis invocation, so
	// it can be exercised by sourcing nothing and running the real script only up
	// to that point: a payload directory with the record but no executable.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, BuildInfoName), []byte("version=0.1.0-dev.0+g0123456789ab\nvariant=development\nnumeric=0.1.0.0\ncommit="+testCommit+"\nbinary."+PortableName+"="+testSum+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(shell, filepath.Join(repoRoot(t), "scripts", "build-windows-installer.sh"), "--portable-dir", dir)
	command.Env = append(os.Environ(), "MAKENSIS="+fakeMakensis(t))
	out, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("the build script accepted a payload directory with no executable:\n%s", out)
	}
	if !strings.Contains(string(out), PortableName) {
		t.Errorf("the refusal does not name the missing file:\n%s", out)
	}
}
