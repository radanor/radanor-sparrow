//go:build windows

package wininstaller

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/radanor/radanor-shield/internal/assessment"
	"github.com/radanor/radanor-shield/internal/gateway"
	"github.com/radanor/radanor-shield/internal/ipc"
	"github.com/radanor/radanor-shield/internal/lifecycle"
	"github.com/radanor/radanor-shield/internal/monitoring"
	"github.com/radanor/radanor-shield/internal/storage"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

func TestNativeInstallerLifecycle(t *testing.T) {
	if os.Getenv("RADANOR_NATIVE_INSTALLER") != "1" {
		t.Skip("explicit native installer acceptance opt-in")
	}
	if gateway.ValidateNotElevated() != nil {
		t.Skip("native installer acceptance requires a standard-user token")
	}
	runInstallerLifecycle(t, false)
}

// Hosted runners can exercise real silent NSIS operations without an unlocked
// desktop. This does not qualify UI behavior or standard-user token behavior.
func TestHeadlessInstallerLifecycle(t *testing.T) {
	if os.Getenv("RADANOR_HEADLESS_INSTALLER") != "1" {
		t.Skip("explicit headless installer opt-in")
	}
	runInstallerLifecycle(t, true)
}

func runInstallerLifecycle(t *testing.T, headless bool) {
	compiler := os.Getenv("MAKENSIS")
	a := os.Getenv("RADANOR_INSTALLER_PORTABLE_A")
	b := os.Getenv("RADANOR_INSTALLER_PORTABLE_B")
	if headless {
		a = b
	}
	if compiler == "" || a == "" || b == "" {
		t.Fatal("MAKENSIS and A/B portable directories required")
	}
	dir := t.TempDir()
	install := filepath.Join(dir, "Installed ü space")
	state := filepath.Join(dir, "private-state")
	run := filepath.Join(state, "run")
	idBytes := make([]byte, 12)
	if _, err := rand.Read(idBytes); err != nil {
		t.Fatal(err)
	}
	id := hex.EncodeToString(idBytes)
	regPath := `Software\Radanor\P8Acceptance\` + id
	unreg := `Software\Microsoft\Windows\CurrentVersion\Uninstall\SparrowP8-` + id
	menu := "Radanor P8 Acceptance-" + id
	menuPath := filepath.Join(os.Getenv("APPDATA"), "Microsoft", "Windows", "Start Menu", "Programs", menu)
	current := func() string {
		k, err := registry.OpenKey(registry.CURRENT_USER, regPath, registry.QUERY_VALUE)
		if err != nil {
			return ""
		}
		defer k.Close()
		v, _, _ := k.GetStringValue("Current")
		return v
	}
	compile := func(payload, mode string) string {
		t.Helper()
		info, err := ReadBuildInfo(payload)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = info.VerifyPortable(filepath.Join(payload, PortableName)); err != nil {
			t.Fatal(err)
		}
		ancestors, err := exec.Command("git", "-C", repoRoot(t), "rev-list", info.Commit).Output()
		if err != nil {
			t.Fatal(err)
		}
		ancestry := filepath.Join(dir, "ancestry-"+info.Commit)
		if err = os.WriteFile(ancestry, ancestors, 0600); err != nil {
			t.Fatal(err)
		}
		output := filepath.Join(dir, "setup-"+filepath.Base(payload)+"-"+mode+".exe")
		args := []string{"-V2", "-DACCEPTANCE_DIAGNOSTIC=" + filepath.Join(dir, "diagnostic.txt"), "-DPRODUCT_VERSION=" + info.Version, "-DVARIANT=" + info.Variant, "-DNUMERIC_VERSION=" + info.Numeric, "-DBUILD_SEQUENCE=" + info.Sequence, "-DBUILD_COMMIT=" + info.Commit, "-DPAYLOAD_EXE=" + filepath.Join(payload, PortableName), "-DPAYLOAD_LICENSE=" + filepath.Join(payload, "LICENSE"), "-DPAYLOAD_NOTICES=" + filepath.Join(payload, "THIRD_PARTY_NOTICES.md"), "-DPAYLOAD_BUILD_INFO=" + filepath.Join(payload, "BUILD-INFO"), "-DANCESTRY=" + ancestry, "-DAPP_ICON=" + filepath.Join(repoRoot(t), "web", "public", "favicon.ico"), "-DOUTFILE=" + output, "-DAPP_REG_KEY=" + regPath, "-DUNINSTALL_REG_KEY=" + unreg, "-DMENU_FOLDER=" + menu}
		if mode == "fail" {
			args = append(args, "-DACCEPTANCE_FAIL_AFTER_STAGE")
		}
		if mode == "pause" {
			args = append(args, "-DACCEPTANCE_PAUSE_AFTER_STAGE")
		}
		args = append(args, ScriptPath(repoRoot(t)))
		if out, err := exec.Command(compiler, args...).CombinedOutput(); err != nil {
			t.Fatalf("fixture compile: %v %s", err, out)
		}
		return output
	}
	setupA := compile(a, "normal")
	setupB := setupA
	if !headless {
		setupB = compile(b, "normal")
	}
	fault := compile(b, "fail")
	paused := compile(b, "pause")
	runSetup := func(exe string, want int) {
		t.Helper()
		cmd := exec.Command(exe)
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CmdLine: syscall.EscapeArg(exe) + " /S /D=" + install}
		err := cmd.Run()
		code := 0
		if err != nil {
			if failure, ok := err.(*exec.ExitError); ok {
				code = failure.ExitCode()
			} else {
				t.Fatal(err)
			}
		}
		if code != want {
			diagnostic, _ := os.ReadFile(filepath.Join(dir, "diagnostic.txt"))
			t.Fatalf("setup exit=%d want=%d diagnostic=%s", code, want, diagnostic)
		}
	}
	// Only this randomly namespaced registration/shortcut can be cleaned up.
	t.Cleanup(func() {
		if payload := current(); payload != "" {
			_ = exec.Command(filepath.Join(payload, "Uninstall.exe"), "/S").Run()
			for i := 0; i < 100 && current() != ""; i++ {
				time.Sleep(50 * time.Millisecond)
			}
		}
		_ = registry.DeleteKey(registry.CURRENT_USER, regPath)
		_ = registry.DeleteKey(registry.CURRENT_USER, unreg)
		_ = os.Remove(filepath.Join(menuPath, "Radanor Sparrow Community Edition.lnk"))
		_ = os.Remove(menuPath)
	})
	if err := os.MkdirAll(state, 0700); err != nil {
		t.Fatal(err)
	}
	seedInstallerProfile(t, state)
	checkInstallerProfile(t, state)
	db := filepath.Join(state, "monitoring.db")
	before, _ := os.ReadFile(db)
	runSetup(setupA, 0)
	first := current()
	if first == "" {
		t.Fatal("fresh install not registered")
	}
	checkInstalled := func(payload string) {
		t.Helper()
		want, _ := os.ReadFile(filepath.Join(payload, PortableName))
		got, err := os.ReadFile(filepath.Join(current(), PortableName))
		if err != nil || !bytes.Equal(got, want) {
			t.Fatal("installed payload differs from package")
		}
	}
	checkInstalled(a)
	if got, _ := os.ReadFile(db); !bytes.Equal(got, before) {
		t.Fatal("fresh installer touched private SQLite")
	}
	// Resolve the actual installed Start Menu link; launch its verified target
	// with explicit isolated state, avoiding Explorer's default-profile environment.
	link := filepath.Join(menuPath, "Radanor Sparrow Community Edition.lnk")
	helper := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", `[Console]::OutputEncoding=[Text.UTF8Encoding]::new($false); $s=New-Object -ComObject WScript.Shell; $s.CreateShortcut($env:RADANOR_LINK).TargetPath`)
	helper.Env = append(os.Environ(), "RADANOR_LINK="+link)
	out, err := helper.Output()
	resolved := strings.TrimSpace(string(out))
	linkInfo, linkErr := os.Stat(resolved)
	expectedInfo, expectedErr := os.Stat(filepath.Join(first, PortableName))
	if err != nil || linkErr != nil || expectedErr != nil || !os.SameFile(linkInfo, expectedInfo) {
		t.Fatal("Start Menu shortcut target is invalid")
	}
	if headless {
		guard, err := lifecycle.Acquire(run, "desktop")
		if err != nil {
			t.Fatal(err)
		}
		func() { defer guard.Close(); runSetup(setupB, 2) }()
	} else {
		startAndStopInstalled(t, resolved, state, run, func() { runSetup(setupB, 2) }, false)
		t.Log("NATIVE: fresh per-user install, Start Menu target launch and running-process replacement refusal passed")
	}
	runSetup(setupB, 0)
	second := current()
	if second == first {
		t.Fatal("upgrade did not activate a new generation")
	}
	checkInstalled(b)
	checkInstallerProfile(t, state)
	if !headless {
		runSetup(setupA, 3)
		if current() != second {
			t.Fatal("downgrade changed active generation")
		}
	}
	runSetup(setupB, 0)
	repaired := current()
	if repaired == second {
		t.Fatal("repair reused in-place payload")
	}
	checkInstalled(b)
	// A returned staging error must leave the existing registration/payload intact.
	runSetup(fault, 41)
	if current() != repaired {
		t.Fatal("failed setup changed active generation")
	}
	checkInstalled(b)
	// Kill ONLY the fixture installer after it finishes staging and pauses.
	cmd := exec.Command(paused)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CmdLine: syscall.EscapeArg(paused) + " /S /D=" + install}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	found := false
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		entries, _ := os.ReadDir(filepath.Join(install, "versions"))
		for _, entry := range entries {
			p := filepath.Join(install, "versions", entry.Name())
			if p != first && p != second && p != repaired {
				if _, err := os.Stat(filepath.Join(p, "Uninstall.exe")); err == nil {
					found = true
				}
			}
		}
		if found {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	if !found {
		t.Fatal("interruption fixture did not finish staging")
	}
	if current() != repaired {
		t.Fatal("interrupted setup changed activation")
	}
	checkInstalled(b)
	if !headless {
		startAndStopInstalled(t, filepath.Join(repaired, PortableName), state, run, nil, true)
	}
	runSetup(setupB, 0)
	checkInstalled(b)
	checkInstallerProfile(t, state)
	before, _ = os.ReadFile(db)
	uninstaller := filepath.Join(current(), "Uninstall.exe")
	// A non-application file lock must report failure and keep the current
	// registration/ownership marker usable for a later uninstall retry.
	lockedPath, _ := windows.UTF16PtrFromString(filepath.Join(current(), PortableName))
	locked, lockErr := windows.CreateFile(lockedPath, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, 0, 0)
	if lockErr != nil {
		t.Fatal(lockErr)
	}
	lockedUninstall := exec.Command(uninstaller)
	lockedUninstall.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CmdLine: syscall.EscapeArg(uninstaller) + " /S _?=" + current()}
	lockErr = lockedUninstall.Run()
	windows.CloseHandle(locked)
	if failure, ok := lockErr.(*exec.ExitError); !ok || failure.ExitCode() != 51 || current() == "" {
		t.Fatalf("locked uninstall did not retain registration with exit 51: %v", lockErr)
	}
	checkInstalled(b)
	unrelated := filepath.Join(install, "user-owned.txt")
	if err = os.WriteFile(unrelated, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = exec.Command(uninstaller, "/S").Run(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 200 && current() != ""; i++ {
		time.Sleep(50 * time.Millisecond)
	}
	if current() != "" {
		t.Fatal("uninstall registration survived")
	}
	if _, err = os.Stat(link); !os.IsNotExist(err) {
		t.Fatal("shortcut survived uninstall")
	}
	if got, _ := os.ReadFile(db); !bytes.Equal(got, before) {
		t.Fatal("uninstall changed private SQLite")
	}
	if got, _ := os.ReadFile(unrelated); string(got) != "keep" {
		t.Fatal("uninstall removed unrelated file")
	}
	if err = filepath.WalkDir(install, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() && path != unrelated {
			return fmt.Errorf("owned file survived uninstall: %s", entry.Name())
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	checkInstallerProfile(t, state)
	if headless {
		t.Log("HEADLESS: real silent install, shortcut resolution, held-mutex refusal, repair, staging failure, interruption recovery, locked-file uninstall retry and data preservation passed; UI and A-to-B upgrade are separate native acceptance")
	} else {
		t.Log("NATIVE: real A-to-B upgrade, downgrade refusal, repair, staging failure, killed-installer recovery, installed WebView/tray/duplicate behavior, uninstall and SQLite/settings/findings/history preservation passed")
	}
}

func seedInstallerProfile(t *testing.T, state string) {
	t.Helper()
	s, err := storage.Open(filepath.Join(state, "monitoring.db"), storage.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	now := time.Now()
	if err = s.PutSettings(ctx, map[string]string{"theme": "dark"}); err != nil {
		t.Fatal(err)
	}
	if err = s.Process(ctx, monitoring.Event{ID: "p8-fixture", Source: "p8.fixture", Type: "file.modified", Platform: "windows", OccurredAt: now, ObservedAt: now, Severity: monitoring.SeverityInfo, Evidence: monitoring.EvidenceTelemetry, SchemaVersion: monitoring.EventSchemaV1, Checkpoint: []byte("p8-checkpoint")}); err != nil {
		t.Fatal(err)
	}
	id, err := s.StartRun(ctx, "manual")
	if err != nil {
		t.Fatal(err)
	}
	result := assessment.CheckResult{CheckID: "p8-fixture", CheckVersion: "1.0.0", Category: "fixture", Title: "installer fixture", Result: assessment.Fail, Severity: assessment.SeverityHigh, Fresh: true, CollectedAt: now, EvaluatedAt: now, Observation: assessment.Observation{ID: "p8-fixture", Collector: "p8-fixture", Platform: "windows", Status: assessment.Collected, CollectedAt: now, Source: "fixture"}}
	if err = s.SaveCheckResults(ctx, id, []assessment.CheckResult{result}); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.ApplyFindings(ctx, id, []assessment.CheckResult{result}); err != nil {
		t.Fatal(err)
	}
	if err = s.FinishRun(ctx, id, assessment.ScanOutcome{Status: "completed", StartedAt: now, FinishedAt: now}); err != nil {
		t.Fatal(err)
	}
}
func checkInstallerProfile(t *testing.T, state string) {
	t.Helper()
	s, err := storage.Open(filepath.Join(state, "monitoring.db"), storage.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	settings, err := s.Settings(ctx)
	if err != nil || settings["theme"] != "dark" {
		t.Fatal("preference lost")
	}
	checkpoint, err := s.Load(ctx, "p8.fixture")
	if err != nil || string(checkpoint) != "p8-checkpoint" {
		t.Fatal("monitoring checkpoint lost")
	}
	events, err := s.Recent(ctx, storage.Query{Source: "p8.fixture", Limit: 20})
	if err != nil || len(events) != 1 || events[0].EventID != "p8-fixture" {
		t.Fatal("monitoring event history lost")
	}
	findings, err := s.Findings(ctx, "", 0, 20)
	if err != nil || len(findings) != 1 {
		t.Fatalf("finding preservation: count=%d error=%v", len(findings), err)
	}
	runs, err := s.Runs(ctx, 0, 20)
	if err != nil || len(runs) != 1 || runs[0].Status != "completed" {
		t.Fatal("assessment history lost")
	}
}

func startAndStopInstalled(t *testing.T, binary, state, run string, whileRunning func(), native bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	args := []string{"--no-browser"}
	if native {
		args = nil
	}
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Env = append(os.Environ(), "RADANOR_SPARROW_STATE_DIR="+state, "RADANOR_SPARROW_RUNTIME_DIR="+run)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	var waitErr error
	go func() { waitErr = cmd.Wait(); close(done) }()
	defer func() { _ = cmd.Process.Kill(); <-done }()
	var origin string
	deadline := time.Now().Add(25 * time.Second)
	for time.Now().Before(deadline) {
		value, err := os.ReadFile(filepath.Join(run, "desktop-origin"))
		log, _ := os.ReadFile(filepath.Join(state, "application.log"))
		if err == nil && bytes.Contains(log, []byte("application started")) {
			origin = string(value)
			break
		}
		select {
		case <-done:
			t.Fatalf("installed application exited: %v", waitErr)
		default:
		}
		time.Sleep(50 * time.Millisecond)
	}
	if origin == "" {
		t.Fatal("installed app not ready")
	}
	if whileRunning != nil {
		whileRunning()
	}
	if native {
		user := windows.NewLazySystemDLL("user32.dll")
		var hwnd uintptr
		pidProc := user.NewProc("GetWindowThreadProcessId")
		cb := windows.NewCallback(func(h, _ uintptr) uintptr {
			var pid uint32
			pidProc.Call(h, uintptr(unsafe.Pointer(&pid)))
			if pid == uint32(cmd.Process.Pid) {
				var class [64]uint16
				user.NewProc("GetClassNameW").Call(h, uintptr(unsafe.Pointer(&class[0])), 64)
				if windows.UTF16ToString(class[:]) == "webview" {
					hwnd = h
				}
			}
			return 1
		})
		deadline := time.Now().Add(25 * time.Second)
		for hwnd == 0 && time.Now().Before(deadline) {
			user.NewProc("EnumWindows").Call(cb, 0)
			if hwnd == 0 {
				time.Sleep(50 * time.Millisecond)
			}
		}
		if hwnd == 0 {
			t.Fatal("installed native WebView missing")
		}
		user.NewProc("PostMessageW").Call(hwnd, 0x10, 0, 0)
		time.Sleep(200 * time.Millisecond)
		visible, _, _ := user.NewProc("IsWindowVisible").Call(hwnd)
		if visible != 0 {
			t.Fatal("installed X did not hide")
		}
		if lock, err := lifecycle.Acquire(run, "core"); err == nil {
			lock.Close()
			t.Fatal("monitoring core stopped when the window hid")
		}
		mouse := func(action string) {
			script, err := os.ReadFile(filepath.Join(repoRoot(t), "validation", "windows", "Invoke-SparrowTrayMouse.ps1"))
			if err != nil {
				t.Fatal(err)
			}
			helper := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", "& {"+string(script)+fmt.Sprintf("\n} -NativePID %d -Action %s", cmd.Process.Pid, action))
			if out, err := helper.CombinedOutput(); err != nil {
				t.Fatalf("installed tray %s: %v %s", action, err, out)
			}
		}
		mouse("Open")
		user.NewProc("PostMessageW").Call(hwnd, 0x10, 0, 0)
		duplicate := exec.CommandContext(ctx, binary)
		duplicate.Env = cmd.Env
		if err := duplicate.Run(); err != nil {
			t.Fatal("installed duplicate failed")
		}
		visible, _, _ = user.NewProc("IsWindowVisible").Call(hwnd)
		if visible == 0 {
			t.Fatal("installed duplicate did not restore")
		}
		mouse("Exit")
	} else {
		secret, err := ipc.LoadSecret(ipc.SecretPath(run))
		if err != nil {
			t.Fatal(err)
		}
		ticket, err := ipc.Call(ctx, run, secret, "issue_ticket", "")
		if err != nil || !ticket.OK {
			t.Fatal("installed launch ticket failed")
		}
		jar, _ := cookiejar.New(nil)
		client := &http.Client{Jar: jar, Timeout: 5 * time.Second}
		body, _ := json.Marshal(map[string]string{"ticket": ticket.Value})
		request, _ := http.NewRequest("POST", origin+"/api/v1/session/exchange", bytes.NewReader(body))
		request.Header.Set("Origin", origin)
		response, err := client.Do(request)
		if err != nil {
			t.Fatal("installed session exchange failed")
		}
		var session struct {
			CSRF string `json:"csrf_token"`
		}
		err = json.NewDecoder(response.Body).Decode(&session)
		response.Body.Close()
		if err != nil || session.CSRF == "" {
			t.Fatal("installed authentication failed")
		}
		request, _ = http.NewRequest("POST", origin+"/api/v1/application/exit", nil)
		request.Header.Set("Origin", origin)
		request.Header.Set("X-CSRF-Token", session.CSRF)
		response, err = client.Do(request)
		if err != nil {
			t.Fatal("installed Exit failed")
		}
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
	}
	select {
	case <-done:
		if waitErr != nil {
			t.Fatalf("installed exit: %v", waitErr)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("installed supervisor survived Exit")
	}
	for i := 0; i < 100; i++ {
		lock, err := lifecycle.Acquire(run, "core")
		if err == nil {
			lock.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("installed managed core did not release its lock")
}
