param(
    [Parameter(Mandatory=$true)][int]$NativePID,
    [Parameter(Mandatory=$true)][ValidateSet('Open','Exit')][string]$Action
)
$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName UIAutomationClient,UIAutomationTypes,WindowsBase
if (-not ('SparrowAcceptanceMouse' -as [type])) {
    Add-Type @'
using System;
using System.Runtime.InteropServices;
using System.Text;
public static class SparrowAcceptanceMouse {
    [DllImport("user32.dll")] public static extern bool SetCursorPos(int x, int y);
    [DllImport("user32.dll")] public static extern void mouse_event(uint flags, uint x, uint y, uint data, UIntPtr extra);
    [StructLayout(LayoutKind.Sequential)] public struct Rect { public int Left, Top, Right, Bottom; }
    [StructLayout(LayoutKind.Sequential)] struct IconID { public uint Size; public IntPtr Window; public uint ID; public Guid Guid; }
    delegate bool EnumProc(IntPtr window, IntPtr ignored);
    [DllImport("user32.dll")] static extern bool EnumWindows(EnumProc callback, IntPtr ignored);
    [DllImport("user32.dll")] static extern uint GetWindowThreadProcessId(IntPtr window, out uint pid);
    [DllImport("user32.dll", CharSet=CharSet.Unicode)] static extern int GetClassName(IntPtr window, StringBuilder name, int length);
    [DllImport("user32.dll")] static extern bool IsWindowVisible(IntPtr window);
    [DllImport("shell32.dll")] static extern int Shell_NotifyIconGetRect(ref IconID id, out Rect rect);
    public sealed class Status {
        public long Window; public bool Visible; public int IconResult; public Rect IconRect;
    }
    public static Status Inspect(uint pid) {
        var status = new Status { IconResult = -1 };
        EnumWindows((window, ignored) => {
            uint owner; GetWindowThreadProcessId(window, out owner);
            if(owner == pid) {
                var name = new StringBuilder(64); GetClassName(window, name, 64);
                if(name.ToString() == "webview") status.Window = window.ToInt64();
            }
            return true;
        }, IntPtr.Zero);
        if(status.Window != 0) {
            status.Visible = IsWindowVisible(new IntPtr(status.Window));
            var id = new IconID { Size = (uint)Marshal.SizeOf(typeof(IconID)), Window = new IntPtr(status.Window), ID = 1 };
            status.IconResult = Shell_NotifyIconGetRect(ref id, out status.IconRect);
        }
        return status;
    }
}
'@
}
$script:trayClickHistory = [Collections.Generic.List[object]]::new()
$script:trayClickAttempt = -1
function Get-ControlPoint($element) {
    try { return $element.GetClickablePoint() } catch {
        $bounds = $element.Current.BoundingRectangle
        if ($element.Current.IsOffscreen -or $bounds.Width -le 0 -or $bounds.Height -le 0) {
            throw 'Tray mouse target has no visible clickable bounds'
        }
        return [System.Windows.Point]::new(($bounds.X+$bounds.Width/2), ($bounds.Y+$bounds.Height/2))
    }
}
function Click-Element($element, [bool]$right=$false) {
    $point = Get-ControlPoint $element
    if (-not [SparrowAcceptanceMouse]::SetCursorPos([int]$point.X, [int]$point.Y)) { throw 'Cannot position mouse' }
    Start-Sleep -Milliseconds 150
    if ($env:RADANOR_TRAY_DIAGNOSTICS) {
        $script:trayClickHistory.Add([pscustomobject]@{Right=$right;X=$point.X;Y=$point.Y})
    }
    if ($right) { $down=8; $up=16 } else { $down=2; $up=4 }
    [SparrowAcceptanceMouse]::mouse_event($down,0,0,0,[UIntPtr]::Zero)
    Start-Sleep -Milliseconds 80
    [SparrowAcceptanceMouse]::mouse_event($up,0,0,0,[UIntPtr]::Zero)
}
function Named-Control([string]$name, $controlType, [int]$owner=0) {
    $conditions = @(
        (New-Object Windows.Automation.PropertyCondition([Windows.Automation.AutomationElement]::NameProperty,$name)),
        (New-Object Windows.Automation.PropertyCondition([Windows.Automation.AutomationElement]::ControlTypeProperty,$controlType)))
    if ($owner -ne 0) { $conditions += New-Object Windows.Automation.PropertyCondition([Windows.Automation.AutomationElement]::ProcessIdProperty,$owner) }
    $condition = New-Object Windows.Automation.AndCondition -ArgumentList (,[Windows.Automation.Condition[]]$conditions)
    return [Windows.Automation.AutomationElement]::RootElement.FindFirst([Windows.Automation.TreeScope]::Descendants,$condition)
}
function Write-TrayDiagnostic([string]$stage, [int]$attempt=-1) {
    if (-not $env:RADANOR_TRAY_DIAGNOSTICS) { return }
    if ($env:RADANOR_TRAY_DIAGNOSTICS_MODE -ne 'all' -and $stage -ne 'failure') { return }
    if ($attempt -lt 0) { $attempt=$script:trayClickAttempt }
    [pscustomobject]@{Time=[DateTime]::UtcNow.ToString('o');Action=$Action;Stage=$stage;Attempt=$attempt;Alive=($null -ne (Get-Process -Id $NativePID -ErrorAction SilentlyContinue));Native=[SparrowAcceptanceMouse]::Inspect($NativePID);Clicks=$script:trayClickHistory.ToArray();Probe=$script:lastTrayProbe;Reason=$script:trayTargetReason} |
        ConvertTo-Json -Depth 7 -Compress | Add-Content -Encoding UTF8 -LiteralPath $env:RADANOR_TRAY_DIAGNOSTICS
}
function Test-TrayPoint($point, $rect) {
    return $rect.Right -gt $rect.Left -and $rect.Bottom -gt $rect.Top -and
        $point.X -ge $rect.Left -and $point.X -lt $rect.Right -and
        $point.Y -ge $rect.Top -and $point.Y -lt $rect.Bottom
}
function Get-TrayNativeStatus { return [SparrowAcceptanceMouse]::Inspect($NativePID) }
function Get-SparrowButtonAtPoint($point, $rect) {
    $script:lastTrayProbe=@()
    $condition=New-Object Windows.Automation.AndCondition(
        (New-Object Windows.Automation.PropertyCondition([Windows.Automation.AutomationElement]::NameProperty,'Radanor Sparrow')),
        (New-Object Windows.Automation.PropertyCondition([Windows.Automation.AutomationElement]::ControlTypeProperty,[Windows.Automation.ControlType]::Button)))
    foreach ($element in [Windows.Automation.AutomationElement]::RootElement.FindAll([Windows.Automation.TreeScope]::Descendants,$condition)) {
        try {
            $bounds=$element.Current.BoundingRectangle
            if($env:RADANOR_TRAY_DIAGNOSTICS){$script:lastTrayProbe+=@([pscustomobject]@{Offscreen=$element.Current.IsOffscreen;ContainsPoint=$bounds.Contains($point)})}
            if (-not $element.Current.IsOffscreen -and $bounds.Contains($point) -and
                (Test-TrayPoint (Get-ControlPoint $element) $rect)) { return $element }
        } catch [Windows.Automation.ElementNotAvailableException] {
            # An overflow refresh invalidates individual UIA elements.
        }
    }
    return $null
}
function Get-TrayIconTarget {
    $script:trayTargetReason='native'
    $native = Get-TrayNativeStatus
    if ($native.Window -eq 0 -or $native.IconResult -ne 0) { return $null }
    $r = $native.IconRect
    $point = [System.Windows.Point]::new(($r.Left+$r.Right)/2, ($r.Top+$r.Bottom)/2)
    if (-not (Test-TrayPoint $point $r)) { return $null }
    # Query the actual HWND/uID icon location, then match live UIA geometry.
    # A global FindFirst tooltip match can be stale while overflow is closing.
    $element = Get-SparrowButtonAtPoint $point $r
    $script:trayTargetReason='hit-missing'
    if ($null -eq $element) { return $null }
    try {
        $script:trayTargetReason='clickable-outside'
        if (-not (Test-TrayPoint (Get-ControlPoint $element) $r)) { return $null }
    } catch { $script:trayTargetReason='clickable-unavailable'; return $null }
    $script:trayTargetReason='ready'
    return [pscustomobject]@{Window=$native.Window;Point=$point;Element=$element;Native=$native}
}
function Wait-TrayIconTarget {
    $target = Get-TrayIconTarget
    if ($null -ne $target) { return $target }
    $native = Get-TrayNativeStatus
    if ($native.Window -eq 0 -or $native.IconResult -ne 0) { throw 'Isolated supervisor has no registered notification icon' }
    foreach ($name in @('Show Hidden Icons','Show hidden icons','Hidden icon menu','Notification Chevron')) {
        $overflow = Named-Control $name ([Windows.Automation.ControlType]::Button)
        if ($null -eq $overflow -or $overflow.Current.IsOffscreen) { continue }
        # GetRect maps a collapsed icon to the chevron. Do not blindly toggle an
        # already open overflow when UIA is merely rebuilding its elements.
        if (Test-TrayPoint (Get-ControlPoint $overflow) $native.IconRect) {
            Click-Element $overflow
            Write-TrayDiagnostic 'overflow-click'
        }
        break
    }
    $deadline = [DateTime]::UtcNow.AddSeconds(2)
    do {
        $target = Get-TrayIconTarget
        if ($null -ne $target) { return $target }
        Start-Sleep -Milliseconds 50
    } while ([DateTime]::UtcNow -lt $deadline)
    return $null
}
function Click-TrayIcon($target) {
    if (-not [SparrowAcceptanceMouse]::SetCursorPos([int]$target.Point.X, [int]$target.Point.Y)) { throw 'Cannot position mouse' }
    # Pointer movement can dismiss/change overflow. Re-resolve after moving,
    # immediately before real input, rather than clicking cached coordinates.
    $fresh = Get-TrayIconTarget
    if ($null -eq $fresh -or $fresh.Window -ne $target.Window -or
        $fresh.Point.X -ne $target.Point.X -or $fresh.Point.Y -ne $target.Point.Y) { return $false }
    if ($env:RADANOR_TRAY_DIAGNOSTICS) {
        $script:trayClickHistory.Add([pscustomobject]@{Right=$true;X=$fresh.Point.X;Y=$fresh.Point.Y;Native=$fresh.Native})
    }
    [SparrowAcceptanceMouse]::mouse_event(8,0,0,0,[UIntPtr]::Zero)
    Start-Sleep -Milliseconds 80
    [SparrowAcceptanceMouse]::mouse_event(16,0,0,0,[UIntPtr]::Zero)
    return $true
}
function Invoke-TrayMenuAction([int]$menuTimeoutMilliseconds=8000) {
    for ($clickAttempt=0; $clickAttempt -lt 3; $clickAttempt++) {
        $script:trayClickAttempt=$clickAttempt
        # Resolve/reopen overflow on EVERY retry, including after a menu timeout.
        $target = Wait-TrayIconTarget
        if ($null -eq $target -or -not (Click-TrayIcon $target)) { continue }
        Write-TrayDiagnostic 'right-click' $clickAttempt
        $deadline = [DateTime]::UtcNow.AddMilliseconds($menuTimeoutMilliseconds)
        do {
            $item = Named-Control ($Action+' Radanor Sparrow') ([Windows.Automation.ControlType]::MenuItem) $NativePID
            if ($null -ne $item) {
                Write-TrayDiagnostic 'menu-found' $clickAttempt
                Click-Element $item
                Write-Output ('MOUSE: tray '+$Action+' selected for isolated supervisor')
                return
            }
            if ([DateTime]::UtcNow -ge $deadline) { break }
            Start-Sleep -Milliseconds 100
        } while ([DateTime]::UtcNow -lt $deadline)
        Write-TrayDiagnostic 'menu-timeout' $clickAttempt
    }
    throw 'PID-owned Sparrow tray menu item was not exposed'
}
try {
    # Never inspect argv, browser addresses, session cookies or document text.
    $process = Get-Process -Id $NativePID
    if ($process.ProcessName -ne 'RadanorSparrow') { throw 'Expected isolated Sparrow supervisor' }
    Write-TrayDiagnostic 'start'
    Invoke-TrayMenuAction
} catch { Write-TrayDiagnostic 'failure'; throw }
