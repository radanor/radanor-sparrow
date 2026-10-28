//go:build windows

package wininstaller

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/radanor/radanor-shield/internal/gateway"
)

// Real native mouse input, deliberately separate from headless installer CI.
// Reuse the installed-app assertion sequence against the immutable portable.
func TestNativeTrayMouseLifecycle(t *testing.T) {
	if os.Getenv("RADANOR_NATIVE_TRAY") != "1" {
		t.Skip("explicit native tray mouse opt-in")
	}
	if gateway.ValidateNotElevated() != nil {
		t.Fatal("tray acceptance requires a non-elevated token")
	}
	binary := os.Getenv("RADANOR_SPARROW_TEST_BINARY")
	if binary == "" {
		t.Fatal("RADANOR_SPARROW_TEST_BINARY required")
	}
	state := t.TempDir()
	// WebView2 may finish profile writes after the supervisor exits. Match the
	// existing desktop fixture's bounded cleanup of only this owned directory.
	t.Cleanup(func() {
		deadline := time.Now().Add(10 * time.Second)
		for {
			if err := os.RemoveAll(state); err == nil {
				return
			} else if time.Now().After(deadline) {
				t.Errorf("isolated tray profile cleanup: %v", err)
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
	})
	startAndStopInstalled(t, binary, state, filepath.Join(state, "run"), nil, true)
}

func trayFunctionCase(t *testing.T, body string) {
	t.Helper()
	file := filepath.Join(t.TempDir(), "case.ps1")
	if err := os.WriteFile(file, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", `
$ErrorActionPreference='Stop'
Add-Type -AssemblyName UIAutomationClient,UIAutomationTypes,WindowsBase
$tokens=$null; $parseErrors=$null
$ast=[Management.Automation.Language.Parser]::ParseFile($env:RADANOR_TRAY_SCRIPT,[ref]$tokens,[ref]$parseErrors)
if($parseErrors.Count){throw $parseErrors[0].Message}
foreach($fn in $ast.FindAll({param($node) $node -is [Management.Automation.Language.FunctionDefinitionAst]},$true)) {
 . ([scriptblock]::Create($fn.Extent.Text))
}
. ([scriptblock]::Create([IO.File]::ReadAllText($env:RADANOR_TRAY_CASE)))
`)
	cmd.Env = append(os.Environ(), "RADANOR_TRAY_SCRIPT="+filepath.Join(repoRoot(t), "validation", "windows", "Invoke-SparrowTrayMouse.ps1"), "RADANOR_TRAY_CASE="+file, "RADANOR_TRAY_DIAGNOSTICS=")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("tray regression: %v %s", err, out)
	}
}

func TestTrayTargetRejectsStaleUIAPoint(t *testing.T) {
	trayFunctionCase(t, `
$script:native=[pscustomobject]@{Window=42;IconResult=0;IconRect=[pscustomobject]@{Left=1518;Top=1020;Right=1558;Bottom=1080}}
function Get-TrayNativeStatus { return $script:native }
function Get-SparrowButtonAtPoint($point) {
 $e=[pscustomobject]@{}
 $e | Add-Member ScriptMethod GetClickablePoint { return [System.Windows.Point]::new(1438,923) }
 return $e
}

# Captured failure: UIA still points into the former overflow, while GetRect
# maps this HWND/ID's hidden icon to the chevron. Never click that stale point.
if($null -ne (Get-TrayIconTarget)){throw 'Stale UIA coordinates accepted'}
$script:native.IconRect=[pscustomobject]@{Left=1412;Top=897;Right=1462;Bottom=947}
$target=Get-TrayIconTarget
if($target.Window -ne 42 -or $target.Point.X -ne 1437 -or $target.Point.Y -ne 922){throw 'Target is not derived from the native icon identity'}
$script:native.IconResult=-1
if($null -ne (Get-TrayIconTarget)){throw 'Unregistered icon accepted'}
`)
}

func TestTrayShellBoundsFallbackRemainsVisibleOnly(t *testing.T) {
	trayFunctionCase(t, `
$e=[pscustomobject]@{Current=[pscustomobject]@{IsOffscreen=$false;BoundingRectangle=[System.Windows.Rect]::new(1518,1020,40,60)}}
$e | Add-Member ScriptMethod GetClickablePoint { throw 'Provider does not implement clickable point' }
$point=Get-ControlPoint $e
if($point.X -ne 1538 -or $point.Y -ne 1050){throw 'Visible shell-control fallback lost'}
$e.Current.IsOffscreen=$true
$rejected=$false
try{Get-ControlPoint $e}catch{$rejected=$true}
if(-not $rejected){throw 'Offscreen shell-control coordinates accepted'}
`)
}

func TestTrayRetryReopensCollapsedOverflow(t *testing.T) {
	trayFunctionCase(t, `
$Action='Exit'; $NativePID=123
$script:visible=$false; $script:menu=$false; $script:opens=0; $script:rights=0; $script:selected=0
$script:chevron=[pscustomobject]@{Current=[pscustomobject]@{IsOffscreen=$false};Kind='chevron'}
$script:chevron | Add-Member ScriptMethod GetClickablePoint { return [System.Windows.Point]::new(1538,1050) }
function Get-TrayNativeStatus { return [pscustomobject]@{Window=42;IconResult=0;IconRect=[pscustomobject]@{Left=1518;Top=1020;Right=1558;Bottom=1080}} }
function Get-TrayIconTarget { if($script:visible){return [pscustomobject]@{Window=42}}; return $null }
function Named-Control($name,$kind,$owner) {
 if($kind -eq [Windows.Automation.ControlType]::Button){return $script:chevron}
 if($owner -ne 123 -or $name -ne 'Exit Radanor Sparrow'){throw 'Menu ownership lost'}
 if($script:menu){return [pscustomobject]@{Kind='menu'}}
 return $null
}
function Click-Element($element) {
 if($element.Kind -eq 'chevron'){$script:opens++;$script:visible=$true}
 else {$script:selected++}
}
function Click-TrayIcon($target) {
 $script:rights++
 # A focus/animation change consumes the first click and closes overflow.
 if($script:rights -eq 1){$script:visible=$false;$script:menu=$false}
 else {$script:menu=$true}
 return $true
}
Invoke-TrayMenuAction 0
if($script:opens -ne 2 -or $script:rights -ne 2 -or $script:selected -ne 1){throw 'Collapsed overflow was not recovered before retry'}
`)
}

func TestTrayDoesNotToggleOpenOverflowDuringUIARefresh(t *testing.T) {
	trayFunctionCase(t, `
$script:queries=0
function Get-TrayIconTarget { $script:queries++; if($script:queries -gt 1){return [pscustomobject]@{Window=42}}; return $null }
function Get-TrayNativeStatus { return [pscustomobject]@{Window=42;IconResult=0;IconRect=[pscustomobject]@{Left=1412;Top=897;Right=1462;Bottom=947}} }
function Named-Control($name,$kind,$owner) {
 $e=[pscustomobject]@{Current=[pscustomobject]@{IsOffscreen=$false}}
 $e | Add-Member ScriptMethod GetClickablePoint { return [System.Windows.Point]::new(1538,1050) }
 return $e
}
function Click-Element($element) { throw 'An already expanded overflow was toggled closed' }
if($null -eq (Wait-TrayIconTarget)){throw 'Live UIA refresh was not awaited'}
`)
}

func TestTrayMouseRequiresRevalidationAfterPointerMovement(t *testing.T) {
	trayFunctionCase(t, `
Add-Type 'public static class SparrowAcceptanceMouse { public static string Flags=""; public static bool SetCursorPos(int x,int y){return true;} public static void mouse_event(uint flags,uint x,uint y,uint data,System.UIntPtr extra){Flags+=flags+",";} }'
$target=[pscustomobject]@{Window=42;Point=[System.Windows.Point]::new(1437,922)}
function Get-TrayIconTarget { return $null }
if(Click-TrayIcon $target){throw 'Stale target was clicked'}
if([SparrowAcceptanceMouse]::Flags -ne ''){throw 'Mouse input emitted before revalidation'}
function Get-TrayIconTarget { return [pscustomobject]@{Window=99;Point=[System.Windows.Point]::new(1437,922)} }
if(Click-TrayIcon $target){throw 'Different native HWND was accepted'}
if([SparrowAcceptanceMouse]::Flags -ne ''){throw 'Mouse input emitted for different HWND'}
function Get-TrayIconTarget { return $target }
if(-not (Click-TrayIcon $target)){throw 'Valid revalidated target rejected'}
if([SparrowAcceptanceMouse]::Flags -ne '8,16,'){throw 'Real right-button down/up path was not used'}
`)
}
