#Requires -Version 5.1
[CmdletBinding()]
param([string]$WorkRoot = '')
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
$scriptRoot = Split-Path -Parent $PSCommandPath
Import-Module (Join-Path $scriptRoot 'RadanorValidation.psm1') -Force
if (-not $WorkRoot) { $WorkRoot = Join-Path ([IO.Path]::GetTempPath()) ('radanor-ce7a-regression-' + [guid]::NewGuid().ToString('N')) }
$passed = 0
function Assert-Native {
    param([bool]$Condition, [string]$Name)
    if (-not $Condition) { throw "FAIL: $Name" }
    $script:passed++
    Write-Host "PASS: $Name"
}

# A real loopback HTTP server runs on a managed thread, independently of the
# PowerShell request pipeline. It never receives or records product credentials.
Add-Type -TypeDefinition @'
using System;
using System.Net;
using System.Net.Sockets;
using System.IO;
using System.Text;
using System.Threading;
public sealed class ShieldHttpFixture : IDisposable {
    readonly TcpListener listener;
    readonly Thread thread;
    volatile bool stopping;
    public string Fault { get; private set; }
    public int Port { get; private set; }
    public ShieldHttpFixture() {
        listener = new TcpListener(IPAddress.Loopback, 0);
        listener.Start(); Port = ((IPEndPoint)listener.LocalEndpoint).Port;
        thread = new Thread(Run); thread.IsBackground = true; thread.Start();
    }
    void Run() {
        try { while (true) {
            using (var client = listener.AcceptTcpClient()) {
                var stream = client.GetStream();
                var reader = new StreamReader(stream);
                string first = reader.ReadLine();
                string line; do { line = reader.ReadLine(); } while (!String.IsNullOrEmpty(line));
                string path = first.Split(' ')[1];
                if (path == "/timeout") { Thread.Sleep(2000); continue; }
                int status = path == "/malformed" ? 200 : Int32.Parse(path.Substring(1));
                string body = status == 204 ? "" : path == "/malformed" ? "{broken-json" : "{\"fixture\":true}";
                byte[] bytes = Encoding.UTF8.GetBytes(body);
                byte[] header = Encoding.ASCII.GetBytes("HTTP/1.1 " + status + " Fixture\r\nContent-Type: application/json\r\nContent-Length: " + bytes.Length + "\r\nConnection: close\r\n\r\n");
                stream.Write(header, 0, header.Length); stream.Write(bytes, 0, bytes.Length);
            }
        } } catch (Exception ex) { if (!stopping) Fault = ex.GetType().Name + ": " + ex.Message; }
    }
    public void Dispose() { stopping = true; listener.Stop(); thread.Join(3000); }
}
'@
$server = New-Object ShieldHttpFixture
try {
    foreach ($status in @(200,204,401,403,404)) {
        $r = Invoke-ShieldHttp -Uri ('http://127.0.0.1:{0}/{1}' -f $server.Port, $status)
        Assert-Native ($r.StatusCode -eq $status -and -not $r.TransportError) "real HTTP $status preserved"
        if ($status -ne 204) { Assert-Native ($r.Body -match 'fixture') "HTTP $status response body preserved" }
    }
    $r = Invoke-ShieldHttp -Uri ('http://127.0.0.1:{0}/malformed' -f $server.Port)
    Assert-Native ($r.StatusCode -eq 200 -and $r.Body -eq '{broken-json') 'malformed JSON preserved as response body'
    $r = Invoke-ShieldHttp -Uri ('http://127.0.0.1:{0}/timeout' -f $server.Port) -TimeoutSeconds 1
    Assert-Native ($r.StatusCode -eq 0 -and $r.TransportError -and $r.Error) 'timeout reported as transport failure'
} finally { $server.Dispose() }
Assert-Native (-not $server.Fault) ("HTTP fixture thread completed: " + $server.Fault)
$r = Invoke-ShieldHttp -Uri ('http://127.0.0.1:{0}/200' -f $server.Port) -TimeoutSeconds 1
Assert-Native ($r.StatusCode -eq 0 -and $r.TransportError -and $r.Error) 'connection refused reported as transport failure'
$module = Get-Module RadanorValidation
$r = & $module {
    function Invoke-WebRequest { throw (New-Object System.InvalidOperationException('fixture without Response property')) }
    Invoke-ShieldHttp -Uri 'http://127.0.0.1/'
}
Assert-Native ($r.StatusCode -eq 0 -and $r.TransportError -and $r.Error -match 'without Response') 'exception without Response handled under strict mode'

Assert-Native ((@(Resolve-ShieldStages -Stages @('startup,ipc')).Count) -eq 2) 'comma-separated stages expanded'
Assert-Native ((@(Resolve-ShieldStages -Stages @('startup','ipc')).Count) -eq 2) 'array stages accepted'
foreach ($selection in @(@('bogus'), @(''), @('startup,'))) {
    $rejected = $false
    try { Resolve-ShieldStages -Stages $selection | Out-Null } catch { $rejected = $true }
    Assert-Native $rejected "invalid stage selection rejected: [$selection]"
}
$rejected = $false
try { Resolve-ShieldStages -Stages @() | Out-Null } catch { $rejected = $true }
Assert-Native $rejected 'empty stage array rejected'

$paths = New-IsolatedValidationRoot -Root $WorkRoot
$file = Join-Path $paths.StateDir 'existing.txt'
Set-Content -LiteralPath $file -Value 'existing readable content'
$before = (Get-Acl -LiteralPath $file).Sddl
$null = New-IsolatedValidationRoot -Root $WorkRoot
Assert-Native ((Get-Content -LiteralPath $file -Raw).Trim() -eq 'existing readable content') 'repeated setup preserves existing file access'
Assert-Native ((Get-Acl -LiteralPath $file).Sddl -eq $before) 'repeated setup preserves existing child security descriptor'
Assert-Native (Test-ShieldPrivateAcl -Path $file) 'inherited current-user-only file ACL accepted'
Assert-Native (Test-ShieldPrivateAcl -Path $paths.StateDir) 'protected current-user-only directory ACL accepted'
$acl = Get-Acl -LiteralPath $file
$everyone = New-Object System.Security.Principal.SecurityIdentifier('S-1-1-0')
$acl.AddAccessRule((New-Object System.Security.AccessControl.FileSystemAccessRule($everyone, 'Read', 'Allow')))
(Get-Item -LiteralPath $file).SetAccessControl($acl)
Assert-Native (-not (Test-ShieldPrivateAcl -Path $file)) 'broad Everyone allow rejected'
$acl.RemoveAccessRuleAll((New-Object System.Security.AccessControl.FileSystemAccessRule($everyone, 'Read', 'Allow')))
(Get-Item -LiteralPath $file).SetAccessControl($acl)
$empty = New-Object System.Security.AccessControl.FileSecurity
$empty.SetAccessRuleProtection($true, $false)
(Get-Item -LiteralPath $file).SetAccessControl($empty)
Assert-Native (-not (Test-ShieldPrivateAcl -Path $file)) 'empty protected DACL rejected'
# Restore this intentionally restricted fixture so the isolated directory stays usable.
(Get-Item -LiteralPath $file).SetAccessControl($acl)
Write-Host "Native harness regression: $passed checks passed. Fixtures: $WorkRoot"
exit 0
