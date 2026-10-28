param([Parameter(Mandatory=$true)][string]$Binary, [switch]$PropertiesUI)
$ErrorActionPreference = 'Stop'
$path = (Resolve-Path -LiteralPath $Binary).Path
if (-not ('SparrowAcceptanceResources' -as [type])) {
    Add-Type @'
using System;
using System.Runtime.InteropServices;
public static class SparrowAcceptanceResources {
    [StructLayout(LayoutKind.Sequential, CharSet=CharSet.Unicode)]
    public struct ShellInfo {
        public int cbSize; public uint fMask; public IntPtr hwnd;
        public string lpVerb, lpFile, lpParameters, lpDirectory;
        public int nShow; public IntPtr hInstApp, lpIDList; public string lpClass;
        public IntPtr hkeyClass; public uint dwHotKey; public IntPtr hIcon, hProcess;
    }
    [DllImport("shell32.dll", CharSet=CharSet.Unicode, SetLastError=true)]
    public static extern bool ShellExecuteEx(ref ShellInfo info);
    public static bool ShowProperties(string path) {
        ShellInfo info = new ShellInfo(); info.cbSize = Marshal.SizeOf(info);
        info.fMask = 12; info.lpVerb = "properties"; info.lpFile = path; info.nShow = 1;
        return ShellExecuteEx(ref info);
    }
    [DllImport("kernel32.dll", CharSet=CharSet.Unicode, SetLastError=true)]
    public static extern IntPtr LoadLibraryEx(string path, IntPtr file, uint flags);
    [DllImport("kernel32.dll", CharSet=CharSet.Unicode, SetLastError=true)]
    public static extern IntPtr FindResource(IntPtr module, IntPtr name, IntPtr type);
    [DllImport("kernel32.dll", SetLastError=true)]
    public static extern uint SizeofResource(IntPtr module, IntPtr resource);
    [DllImport("kernel32.dll", SetLastError=true)]
    public static extern IntPtr LoadResource(IntPtr module, IntPtr resource);
    [DllImport("user32.dll", CharSet=CharSet.Unicode, SetLastError=true)]
    public static extern IntPtr LoadImage(IntPtr module, IntPtr name, uint type, int width, int height, uint flags);
    [DllImport("user32.dll")]
    public static extern bool DestroyIcon(IntPtr icon);
    [DllImport("kernel32.dll")]
    public static extern bool FreeLibrary(IntPtr module);
}
'@
}
# Load as data only: do not execute the application or create any user state.
$module = [SparrowAcceptanceResources]::LoadLibraryEx($path, [IntPtr]::Zero, 0x22)
if ($module -eq [IntPtr]::Zero) { throw 'Windows could not load the artifact as resource data' }
try {
    $group = [SparrowAcceptanceResources]::FindResource($module, [IntPtr]1, [IntPtr]14)
    $groupError = [Runtime.InteropServices.Marshal]::GetLastWin32Error()
    $version = [SparrowAcceptanceResources]::FindResource($module, [IntPtr]1, [IntPtr]16)
    $versionError = [Runtime.InteropServices.Marshal]::GetLastWin32Error()
    $groupSize = [SparrowAcceptanceResources]::SizeofResource($module, $group)
    $versionSize = [SparrowAcceptanceResources]::SizeofResource($module, $version)
    $groupLoaded = [SparrowAcceptanceResources]::LoadResource($module, $group)
    $versionLoaded = [SparrowAcceptanceResources]::LoadResource($module, $version)
    $icon = [SparrowAcceptanceResources]::LoadImage($module, [IntPtr]1, 1, 32, 32, 0)
    $iconUsable = ($icon -ne [IntPtr]::Zero)
    if ($iconUsable) { [void][SparrowAcceptanceResources]::DestroyIcon($icon) }
    $info = [Diagnostics.FileVersionInfo]::GetVersionInfo($path)
    [pscustomobject]@{
        SHA256 = (Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash
        GroupIconFound = ($group -ne [IntPtr]::Zero)
        GroupIconError = $(if ($group -eq [IntPtr]::Zero) { $groupError } else { 0 })
        VersionResourceFound = ($version -ne [IntPtr]::Zero)
        VersionResourceError = $(if ($version -eq [IntPtr]::Zero) { $versionError } else { 0 })
        GroupIconSize = $groupSize
        VersionResourceSize = $versionSize
        GroupIconDataLoaded = ($groupLoaded -ne [IntPtr]::Zero)
        VersionDataLoaded = ($versionLoaded -ne [IntPtr]::Zero)
        ShellIconUsable = $iconUsable
        ProductName = $info.ProductName
        FileDescription = $info.FileDescription
        Brand = $info.CompanyName
        FileVersion = $info.FileVersion
        ProductVersion = $info.ProductVersion
        OriginalFilename = $info.OriginalFilename
        LegalCopyright = $info.LegalCopyright
        AuthenticodeStatus = [string](Get-AuthenticodeSignature -LiteralPath $path).Status
    } | ConvertTo-Json
    if (-not $iconUsable -or $groupSize -eq 0 -or $versionSize -eq 0 -or
        $group -eq [IntPtr]::Zero -or $version -eq [IntPtr]::Zero -or
        $info.ProductName -ne 'Radanor Sparrow' -or
        $info.FileDescription -ne 'Radanor Sparrow Community Edition' -or
        $info.CompanyName -ne 'Radanor' -or
        $info.OriginalFilename -ne 'RadanorSparrow.exe' -or
        [string]::IsNullOrWhiteSpace($info.FileVersion)) {
        throw 'Portable Windows resource acceptance failed; see the safe metadata above'
    }
} finally {
    [void][SparrowAcceptanceResources]::FreeLibrary($module)
}
if ($PropertiesUI) {
    Add-Type -AssemblyName UIAutomationClient,UIAutomationTypes,System.Windows.Forms
    if (-not [SparrowAcceptanceResources]::ShowProperties($path)) { throw 'Cannot open Windows Properties' }
    $dialog = $null
    try {
        $details = $null
        for ($attempt=0; $attempt -lt 50; $attempt++) {
            [Windows.Forms.Application]::DoEvents()
            $roots = [Windows.Automation.AutomationElement]::RootElement.FindAll([Windows.Automation.TreeScope]::Children,[Windows.Automation.Condition]::TrueCondition)
            foreach ($root in $roots) {
                if ($root.Current.Name -eq 'RadanorSparrow Properties' -or $root.Current.Name -eq 'RadanorSparrow.exe Properties') {
                    $dialog = $root
                    $details = $dialog.FindFirst([Windows.Automation.TreeScope]::Descendants,(New-Object Windows.Automation.PropertyCondition([Windows.Automation.AutomationElement]::NameProperty,'Details')))
                    if ($null -ne $details) { break }
                }
            }
            if ($null -ne $details) { break }
            Start-Sleep -Milliseconds 100
        }
        if ($null -eq $details) {
            if ($null -ne $dialog) {
                $tabs = $dialog.FindAll([Windows.Automation.TreeScope]::Descendants,(New-Object Windows.Automation.PropertyCondition([Windows.Automation.AutomationElement]::ControlTypeProperty,[Windows.Automation.ControlType]::TabItem)))
                Write-Output ('Properties tab labels: '+(($tabs | ForEach-Object { $_.Current.Name }) -join ', '))
            }
            throw 'Windows Properties Details tab was not exposed'
        }
        $details.GetCurrentPattern([Windows.Automation.SelectionItemPattern]::Pattern).Select()
        Start-Sleep -Milliseconds 500
        $nodes = $dialog.FindAll([Windows.Automation.TreeScope]::Descendants,[Windows.Automation.Condition]::TrueCondition)
        $names = @($nodes | ForEach-Object { $_.Current.Name })
        foreach ($expected in @($info.ProductName,$info.FileDescription,$info.FileVersion)) {
            if (-not ($names | Where-Object { $_.Contains($expected) })) { throw 'Expected product/version value was absent from Windows Properties Details' }
        }
        Write-Output 'EXPLORER: actual Properties Details exposes product, description and derived version'
    } finally {
        if ($null -ne $dialog) { $dialog.GetCurrentPattern([Windows.Automation.WindowPattern]::Pattern).Close() }
    }
}
