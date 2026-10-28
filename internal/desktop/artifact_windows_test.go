//go:build windows

package desktop

import (
	"bytes"
	"context"
	"debug/pe"
	"encoding/binary"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
	"unicode/utf16"
	"unsafe"

	"github.com/radanor/radanor-shield/internal/branding"
	"github.com/radanor/radanor-shield/internal/ipc"
	"github.com/radanor/radanor-shield/internal/winres"
	"golang.org/x/sys/windows"
)

func TestPackagedAutomaticBrowserLaunchAndReopen(t *testing.T) {
	binary := os.Getenv("RADANOR_SPARROW_TEST_BINARY")
	if binary == "" || os.Getenv("RADANOR_SPARROW_TEST_BROWSER_ONCE") != "1" {
		t.Skip("default-browser UI verification is opt-in")
	}
	dir := t.TempDir()
	run := filepath.Join(dir, "run")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary)
	cmd.Env = append(os.Environ(), "RADANOR_SPARROW_STATE_DIR="+dir, "RADANOR_SPARROW_RUNTIME_DIR="+run)
	configureChild(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal("automatic launch failed")
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	defer func() {
		_ = cmd.Process.Kill()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
	}()
	var origin string
	for i := 0; i < 200; i++ {
		value, err := os.ReadFile(filepath.Join(run, "desktop-origin"))
		if err == nil && validOrigin(string(value)) {
			origin = string(value)
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if origin == "" {
		t.Fatal("automatic application not ready")
	}
	// Match only this isolated origin, verify the authenticated Scan button,
	// then close its active tab. Address values never leave process memory.
	const ui = `
Add-Type -AssemblyName UIAutomationClient,UIAutomationTypes,System.Windows.Forms
$expected=([Uri]$env:RADANOR_TEST_ORIGIN).Authority
$buttonCondition=New-Object Windows.Automation.AndCondition(
 (New-Object Windows.Automation.PropertyCondition([Windows.Automation.AutomationElement]::NameProperty,'Scan now')),
 (New-Object Windows.Automation.PropertyCondition([Windows.Automation.AutomationElement]::ControlTypeProperty,[Windows.Automation.ControlType]::Button)))
$editCondition=New-Object Windows.Automation.PropertyCondition([Windows.Automation.AutomationElement]::ControlTypeProperty,[Windows.Automation.ControlType]::Edit)
for($i=0;$i -lt 120;$i++) {
 $windows=[Windows.Automation.AutomationElement]::RootElement.FindAll([Windows.Automation.TreeScope]::Children,[Windows.Automation.Condition]::TrueCondition)
 foreach($window in $windows) {
  if(-not $window.Current.Name.Contains('Radanor Sparrow')){continue}
  $matches=$false
  foreach($edit in $window.FindAll([Windows.Automation.TreeScope]::Descendants,$editCondition)) {
   try { $value=$edit.GetCurrentPattern([Windows.Automation.ValuePattern]::Pattern).Current.Value
    if($value -eq $expected -or $value.StartsWith($expected+'/') -or $value.StartsWith('http://'+$expected+'/')) {$matches=$true;break}
   } catch {}
  }
  if(-not $matches){continue}
  $button=$window.FindFirst([Windows.Automation.TreeScope]::Descendants,$buttonCondition)
  if($null -ne $button -and $button.Current.IsEnabled) {
   try {$button.SetFocus(); [Windows.Forms.SendKeys]::SendWait('^w'); exit 0} catch {}
  }
 }
 Start-Sleep -Milliseconds 250
}

exit 1
`
	verifyAndClose := func() {
		helper := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", ui)
		helper.Env = append(os.Environ(), "RADANOR_TEST_ORIGIN="+origin)
		configureChild(helper)
		if err := helper.Run(); err != nil {
			t.Fatal("default browser did not expose an authenticated test dashboard (credential diagnostics disabled)")
		}
	}
	verifyAndClose()
	log, err := os.ReadFile(filepath.Join(dir, "application.log"))
	if err != nil || !bytes.Contains(log, []byte("default browser")) {
		t.Fatal("browser UI was found but the isolated supervisor did not report WebView2 fallback")
	}
	select {
	case <-done:
		t.Fatal("closing tab stopped monitoring")
	default:
	}
	duplicate := exec.CommandContext(ctx, binary)
	duplicate.Env = cmd.Env
	configureChild(duplicate)
	if err := duplicate.Run(); err != nil {
		t.Fatal("second executable launch failed")
	}
	verifyAndClose()
	select {
	case <-done:
		t.Fatal("reopening dashboard stopped original parent")
	default:
	}
	t.Log("NATIVE: default browser automatically authenticated; closing its tab kept parent alive; second launch reopened authenticated dashboard")
}

func TestPackagedWebViewHideAndDuplicateReopen(t *testing.T) {
	binary := os.Getenv("RADANOR_SPARROW_TEST_BINARY")
	if binary == "" || os.Getenv("RADANOR_SPARROW_TEST_WEBVIEW") != "1" {
		t.Skip("native WebView2 UI verification is opt-in")
	}
	dir := t.TempDir()
	run := filepath.Join(dir, "run")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary)
	var nativeStderr bytes.Buffer
	cmd.Stderr = &nativeStderr
	cmd.Env = append(os.Environ(), "RADANOR_SPARROW_STATE_DIR="+dir, "RADANOR_SPARROW_RUNTIME_DIR="+run)
	// This GUI executable must receive normal startup visibility. HideWindow
	// overrides WebView2's first ShowWindow call and starts its window hidden.
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	var waitErr error
	go func() { waitErr = cmd.Wait(); close(done) }()
	var children []windows.Handle
	defer func() {
		_ = cmd.Process.Kill()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
		for _, child := range children {
			status, err := windows.WaitForSingleObject(child, 10000)
			windows.CloseHandle(child)
			if err != nil || status != windows.WAIT_OBJECT_0 {
				t.Error("isolated native child did not exit")
			}
		}
		// WebView2 descendants can finish profile writes after the supervisor
		// exits. Retry removal of only this test's own temporary directory.
		deadline := time.Now().Add(10 * time.Second)
		for {
			if err := os.RemoveAll(dir); err == nil {
				break
			} else if time.Now().After(deadline) {
				t.Errorf("isolated profile cleanup: %v", err)
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
	}()

	// Enumerate in-process: no PowerShell startup/compilation on each poll,
	// and no title-only match against another application (or an error dialog).
	enumWindows := user32.NewProc("EnumWindows")
	windowPID := user32.NewProc("GetWindowThreadProcessId")
	className := user32.NewProc("GetClassNameW")
	visible := user32.NewProc("IsWindowVisible")
	var hwnd uintptr
	callback := windows.NewCallback(func(h, _ uintptr) uintptr {
		var pid uint32
		windowPID.Call(h, uintptr(unsafe.Pointer(&pid)))
		if pid == uint32(cmd.Process.Pid) {
			var class [256]uint16
			className.Call(h, uintptr(unsafe.Pointer(&class[0])), uintptr(len(class)))
			if windows.UTF16ToString(class[:]) == "webview" {
				hwnd = h
			}
		}
		return 1
	})
	waitVisible := func(want bool) {
		t.Helper()
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			hwnd = 0
			enumWindows.Call(callback, 0)
			if hwnd != 0 {
				v, _, _ := visible.Call(hwnd)
				if (v != 0) == want {
					return
				}
			}
			select {
			case <-done:
				t.Fatalf("native supervisor exited: %v (runtime failure=%t)", waitErr, bytes.Contains(nativeStderr.Bytes(), []byte("runtime")))
			default:
			}
			time.Sleep(50 * time.Millisecond)
		}
		// Report only allowlisted status flags, never log contents or URLs.
		log, _ := os.ReadFile(filepath.Join(dir, "application.log"))
		t.Fatalf("PID %d: native window found=%t, wanted visible=%t; started=%t browserFallback=%t ipcListenFailure=%t", cmd.Process.Pid, hwnd != 0, want,
			bytes.Contains(log, []byte("application started")), bytes.Contains(log, []byte("default browser")), bytes.Contains(log, []byte("IPC listen failed")))
	}
	// CreateWindow/ShowWindow precede WebView2 initialization and installation
	// of Sparrow's WM_CLOSE handler. A visible HWND alone is not readiness.
	deadline := time.Now().Add(30 * time.Second)
	for {
		log, _ := os.ReadFile(filepath.Join(dir, "application.log"))
		if bytes.Contains(log, []byte("application started")) {
			break
		}
		select {
		case <-done:
			t.Fatalf("native startup exited: %v", waitErr)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("native startup did not become ready")
		}
		time.Sleep(50 * time.Millisecond)
	}
	waitVisible(true)
	verifyPackagedNativeDashboard(t, ctx, cmd.Process.Pid)
	// Hold process handles rather than names/PIDs so cleanup checks cannot
	// accidentally match a later process that reuses a PID.
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		t.Fatal(err)
	}
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	for err = windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
		if entry.ParentProcessID == uint32(cmd.Process.Pid) {
			handle, openErr := windows.OpenProcess(windows.SYNCHRONIZE, false, entry.ProcessID)
			if openErr != nil {
				windows.CloseHandle(snapshot)
				t.Fatal("cannot observe isolated child cleanup")
			}
			children = append(children, handle)
		}
	}
	windows.CloseHandle(snapshot)
	if len(children) == 0 {
		t.Fatal("native supervisor has no managed child")
	}
	original := hwnd
	procPostMessageW.Call(hwnd, wmClose, 0, 0)
	waitVisible(false)
	secret, err := ipc.LoadSecret(ipc.SecretPath(run))
	if err != nil {
		t.Fatal("cannot load isolated IPC secret")
	}
	health, err := ipc.Call(ctx, run, secret, "health", "")
	if err != nil || !health.OK {
		t.Fatal("managed core unavailable while native window is hidden")
	}
	// Exercise the same command dispatched by the notification-area Open menu.
	if os.Getenv("RADANOR_SPARROW_TEST_TRAY_MOUSE") == "1" {
		packagedTrayMouse(t, ctx, cmd.Process.Pid, "Open")
	} else {
		procPostMessageW.Call(hwnd, wmCommand, menuOpen, 0)
	}
	waitVisible(true)
	if hwnd != original {
		t.Fatal("notification-area Open replaced the original native window")
	}
	procPostMessageW.Call(hwnd, wmClose, 0, 0)
	waitVisible(false)
	select {
	case <-done:
		t.Fatal("X stopped the desktop supervisor")
	default:
	}
	duplicate := exec.CommandContext(ctx, binary)
	duplicate.Env = cmd.Env
	configureChild(duplicate)
	if err := duplicate.Run(); err != nil {
		t.Fatal("duplicate launch failed")
	}
	waitVisible(true)
	if hwnd != original {
		t.Fatal("duplicate launch replaced the original window")
	}
	if os.Getenv("RADANOR_SPARROW_TEST_TRAY_MOUSE") == "1" {
		packagedTrayMouse(t, ctx, cmd.Process.Pid, "Exit")
	} else {
		procPostMessageW.Call(hwnd, wmCommand, menuExit, 0)
	}
	select {
	case <-done:
		if waitErr != nil {
			t.Fatalf("explicit Exit failed: %v", waitErr)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("explicit Exit did not stop the supervisor")
	}
}

func packagedTrayMouse(t *testing.T, ctx context.Context, pid int, action string) {
	t.Helper()
	script, err := os.ReadFile(filepath.Join("..", "..", "validation", "windows", "Invoke-SparrowTrayMouse.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	// Only our numeric PID and fixed action reach the shell; no launch URL or
	// ticket is passed. The script uses OS pointer input, never WM_COMMAND.
	helper := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", "& {"+string(script)+"\n} -NativePID "+strconv.Itoa(pid)+" -Action "+action)
	configureChild(helper)
	output, err := helper.CombinedOutput()
	if err != nil {
		t.Fatalf("actual tray mouse %s failed: %v\n%s", action, err, output)
	}
	t.Log(string(output))
}

// UI Automation reads only the PID-owned native window. It never retrieves
// browser addresses, cookies, tickets, or document text for diagnostics.
func verifyPackagedNativeDashboard(t *testing.T, ctx context.Context, pid int) {
	t.Helper()
	const script = `
$ErrorActionPreference='Stop'
Add-Type -AssemblyName UIAutomationClient,UIAutomationTypes
$owner=New-Object Windows.Automation.PropertyCondition([Windows.Automation.AutomationElement]::ProcessIdProperty,[int]$env:RADANOR_NATIVE_TEST_PID)
$button=New-Object Windows.Automation.AndCondition(
 (New-Object Windows.Automation.PropertyCondition([Windows.Automation.AutomationElement]::NameProperty,'Scan now')),
 (New-Object Windows.Automation.PropertyCondition([Windows.Automation.AutomationElement]::ControlTypeProperty,[Windows.Automation.ControlType]::Button)))
for($i=0;$i -lt 120;$i++) {
 $window=[Windows.Automation.AutomationElement]::RootElement.FindFirst([Windows.Automation.TreeScope]::Children,$owner)
 if($null -ne $window) {
  $scan=$window.FindFirst([Windows.Automation.TreeScope]::Descendants,$button)
  if($null -ne $scan -and $scan.Current.IsEnabled) { exit 0 }
 }
 Start-Sleep -Milliseconds 250
}
exit 1
`
	helper := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	helper.Env = append(os.Environ(), "RADANOR_NATIVE_TEST_PID="+strconv.Itoa(pid))
	configureChild(helper)
	if err := helper.Run(); err != nil {
		t.Fatal("PID-owned native WebView2 did not expose an enabled authenticated Scan now button (credential diagnostics disabled)")
	}
}

func TestManagedChildDiesWhenWindowsJobHandleCloses(t *testing.T) {
	child, err := startManaged(fixtureCommand(t, "hang"), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	defer child.stopWithin(time.Second)
	// Exercise kill-on-close itself, without TerminateJobObject or Process.Kill.
	child.containment.close()
	child.containment = containment{} // Do not close a released/reusable handle twice.
	select {
	case <-child.done:
	case <-time.After(3 * time.Second):
		t.Fatal("managed child survived loss of its job handle")
	}
}

func TestWindowsDistributionHasGUISubsystem(t *testing.T) {
	binary := os.Getenv("RADANOR_SPARROW_TEST_BINARY")
	if binary == "" {
		t.Skip("set RADANOR_SPARROW_TEST_BINARY to the distribution executable")
	}
	file, err := pe.Open(binary)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	header, ok := file.OptionalHeader.(*pe.OptionalHeader64)
	if !ok || header.Subsystem != pe.IMAGE_SUBSYSTEM_WINDOWS_GUI {
		t.Fatal("distribution would leave a console window open")
	}
}

// The custom resource reader is not an independent Windows acceptance oracle.
// Exercise version.dll and user32 against the actual packaged executable.
func TestWindowsDistributionReadableByWindows(t *testing.T) {
	binary := os.Getenv("RADANOR_SPARROW_TEST_BINARY")
	if binary == "" {
		t.Skip("set RADANOR_SPARROW_TEST_BINARY to the distribution executable")
	}
	size, err := windows.GetFileVersionInfoSize(binary, nil)
	if err != nil || size == 0 {
		t.Fatalf("Windows cannot read executable version information: %v", err)
	}
	data := make([]byte, size)
	if err := windows.GetFileVersionInfo(binary, 0, size, unsafe.Pointer(&data[0])); err != nil {
		t.Fatal(err)
	}
	values := map[string]string{}
	for _, key := range []string{"CompanyName", "FileDescription", "ProductName", "FileVersion", "ProductVersion", "OriginalFilename", "LegalCopyright"} {
		var value *uint16
		var length uint32
		if err := windows.VerQueryValue(unsafe.Pointer(&data[0]), `\StringFileInfo\040904B0\`+key, unsafe.Pointer(&value), &length); err != nil || length <= 1 {
			t.Fatalf("Windows cannot read %s: %v", key, err)
		}
		values[key] = windows.UTF16PtrToString(value)
	}
	for key, want := range map[string]string{"CompanyName": branding.Company, "FileDescription": branding.DisplayName, "ProductName": branding.Product, "OriginalFilename": branding.WindowsExecutable, "LegalCopyright": branding.CopyrightNotice} {
		if values[key] != want {
			t.Errorf("Windows %s=%q, want %q", key, values[key], want)
		}
	}
	if values["FileVersion"] != values["ProductVersion"] {
		t.Fatal("file and product version differ")
	}
	module, err := windows.LoadLibraryEx(binary, 0, 0x22) // data + image resource only
	if err != nil {
		t.Fatal(err)
	}
	defer windows.FreeLibrary(module)
	icon, _, err := user32.NewProc("LoadImageW").Call(uintptr(module), 1, 1, 32, 32, 0)
	if icon == 0 {
		t.Fatalf("Windows cannot instantiate the executable shell icon: %v", err)
	}
	procDestroyIcon.Call(icon)
}

// This checks the source of the native window icon: the executable carries the
// approved artwork's bytes because internal/desktop embeds them. It cannot show
// that a file manager will display that icon, because Windows takes a file's
// icon from a PE resource, which the embed does not create — see
// TestWindowsDistributionCarriesIconAndVersionResources below, which asserts the
// resource itself.
func TestWindowsDistributionEmbedsApprovedNativeIcon(t *testing.T) {
	binary := os.Getenv("RADANOR_SPARROW_TEST_BINARY")
	if binary == "" {
		t.Skip("set RADANOR_SPARROW_TEST_BINARY to the distribution executable")
	}
	executable, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	icon, err := os.ReadFile(filepath.Join("assets", "radanor.ico"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(executable, icon) {
		t.Fatal("distribution does not embed the approved native icon bytes")
	}
}

// This is the distribution-level counterpart: the PE resource tree the shell and
// a file's Properties dialog actually read. It closes audit gap G4, where an
// icon check passed with no icon resource in the file at all, and it is what
// makes "the distribution carries the approved artwork" a claim about the
// shipped executable rather than about its embedded assets.
//
// The exhaustive parse of the tree is in internal/winres; here the executable
// only has to carry the same images, a group icon that names them, and a version
// resource that states the product and its version where Windows reads them.
func TestWindowsDistributionCarriesIconAndVersionResources(t *testing.T) {
	executable := os.Getenv("RADANOR_SPARROW_TEST_BINARY")
	if executable == "" {
		t.Skip("set RADANOR_SPARROW_TEST_BINARY to the distribution executable")
	}
	// Named for the file rather than for the package, because the resource
	// directory below is read with encoding/binary.
	entries, err := winres.ReadResources(executable)
	if err != nil {
		t.Fatalf("the distribution carries no readable resource tree: %v", err)
	}
	byType := map[uint32]map[uint32][]byte{}
	for _, entry := range entries {
		if byType[entry.Type] == nil {
			byType[entry.Type] = map[uint32][]byte{}
		}
		byType[entry.Type][entry.Name] = entry.Data
	}

	approved, err := os.ReadFile(filepath.Join("assets", "radanor.ico"))
	if err != nil {
		t.Fatal(err)
	}
	images, err := winres.ParseICO(approved)
	if err != nil {
		t.Fatal(err)
	}
	icons := byType[winres.ResourceIcon]
	if len(icons) != len(images) {
		t.Errorf("the distribution carries %d RT_ICON resources, want %d", len(icons), len(images))
	}
	for index, image := range images {
		got := icons[uint32(index+1)]
		if got == nil {
			t.Errorf("RT_ICON %d is missing", index+1)
			continue
		}
		// A rescaled or re-encoded image is a failure, not a repaint.
		if !bytes.Equal(got, image.Data) {
			t.Errorf("RT_ICON %d is not the approved %dx%d image byte for byte", index+1, image.Width, image.Height)
		}
	}
	group := byType[winres.ResourceGroupIcon]
	if len(group) != 1 {
		t.Fatalf("the distribution carries %d RT_GROUP_ICON resources, want 1", len(group))
	}
	directory := group[1]
	if len(directory) != 6+14*len(images) {
		t.Fatalf("the group icon is %d bytes, want %d", len(directory), 6+14*len(images))
	}
	for index, image := range images {
		at := 6 + index*14
		if id := binary.LittleEndian.Uint16(directory[at+12 : at+14]); int(id) != index+1 {
			t.Errorf("group entry %d names RT_ICON %d, want %d", index, id, index+1)
		}
		if size := binary.LittleEndian.Uint32(directory[at+8 : at+12]); int(size) != len(image.Data) {
			t.Errorf("group entry %d declares %d bytes, want %d", index, size, len(image.Data))
		}
	}

	version := byType[winres.ResourceVersion]
	if len(version) != 1 {
		t.Fatalf("the distribution carries %d RT_VERSION resources, want 1", len(version))
	}
	blob := version[1]
	offset := bytes.Index(blob, []byte{0xBD, 0x04, 0xEF, 0xFE}) // VS_FIXEDFILEINFO.dwSignature
	if offset < 0 || offset+16 > len(blob) {
		t.Fatal("the version resource carries no VS_FIXEDFILEINFO")
	}
	if ms, ls := binary.LittleEndian.Uint32(blob[offset+8:offset+12]), binary.LittleEndian.Uint32(blob[offset+12:offset+16]); ms == 0 && ls == 0 {
		t.Error("the distribution's numeric version is 0.0.0.0")
	}
	for _, want := range []string{branding.Product, branding.WindowsExecutable} {
		if !bytes.Contains(blob, utf16LE(want)) {
			t.Errorf("the version resource does not report %q", want)
		}
	}
}

// utf16LE encodes text the way a version resource stores it.
func utf16LE(value string) []byte {
	units := utf16.Encode([]rune(value))
	out := make([]byte, len(units)*2)
	for index, unit := range units {
		binary.LittleEndian.PutUint16(out[index*2:index*2+2], unit)
	}
	return out
}
