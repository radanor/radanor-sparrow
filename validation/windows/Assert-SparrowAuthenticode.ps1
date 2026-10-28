# Windows trust AND independently parsed CMS evidence must both pass.
param(
    [Parameter(Mandatory=$true)][string[]]$Path,
    [Parameter(Mandatory=$true)][string]$ExpectedSignerThumbprint,
    [string]$ExpectedTimestampThumbprint = $env:RADANOR_TIMESTAMP_THUMBPRINT,
    [string]$ExpectedTimestampPolicyOID = $env:RADANOR_TIMESTAMP_POLICY_OID,
    [string]$SignTool = $env:RADANOR_SIGNTOOL,
    [string]$SignToolSHA256 = $env:RADANOR_SIGNTOOL_SHA256
)
$ErrorActionPreference = 'Stop'
function Invoke-SparrowSignatureVerification([string]$Tool, [string]$Artifact) {
    # /tw warns on a missing timestamp. Warning (2), error and crash all fail.
    & $Tool verify /pa /all /v /tw $Artifact
    if ($LASTEXITCODE -ne 0) { throw "SignTool verification failed (including warnings): exit $LASTEXITCODE" }
}
# PowerShell 5.1 uses .NET Framework; hosted pwsh uses the .NET reference pack.
$signingReferences = @('System.Security', 'System.Core')
if ($PSVersionTable.PSEdition -eq 'Core') {
    $signingReferences = @((Get-ChildItem (Join-Path $PSHOME 'ref') -Filter '*.dll').FullName)
    $pkcs = [Security.Cryptography.Pkcs.SignedCms].Assembly.Location
    $signingReferences = @($signingReferences | Where-Object { [IO.Path]::GetFileName($_) -ne [IO.Path]::GetFileName($pkcs) }) + $pkcs
}
foreach ($thumbprint in @($ExpectedSignerThumbprint, $ExpectedTimestampThumbprint)) {
    if ($thumbprint -notmatch '^[0-9A-Fa-f]{40}$') { throw 'Approved signer and timestamp certificate thumbprints must each be exactly 40 hex characters' }
}
if ($ExpectedTimestampPolicyOID -notmatch '^[0-2](\.[0-9]+){2,}$') { throw 'Approved RFC3161 timestamp policy OID required' }
if (-not [IO.Path]::IsPathRooted($SignTool) -or $SignToolSHA256 -notmatch '^[0-9A-Fa-f]{64}$') {
    throw 'An absolute, independently approved SDK SignTool path and SHA256 are required'
}
$tool = (Resolve-Path -LiteralPath $SignTool).Path
if ((Get-FileHash -LiteralPath $tool -Algorithm SHA256).Hash -ine $SignToolSHA256) { throw 'SignTool checksum mismatch' }
Write-Host "SignTool: $tool SHA256=$SignToolSHA256"
if (-not ('SparrowAuthenticode' -as [type])) {
    Add-Type -Path (Join-Path $PSScriptRoot 'SparrowAuthenticode.cs') -ReferencedAssemblies $signingReferences
}
foreach ($candidate in $Path) {
    $resolved = (Resolve-Path -LiteralPath $candidate).Path
    $before = (Get-FileHash -LiteralPath $resolved -Algorithm SHA256).Hash
    $evidence = [SparrowAuthenticode]::CheckEvidence($resolved, $ExpectedSignerThumbprint, $ExpectedTimestampThumbprint, $ExpectedTimestampPolicyOID)
    $signature = Get-AuthenticodeSignature -LiteralPath $resolved
    if ($signature.Status -ne 'Valid' -or $null -eq $signature.SignerCertificate -or
        $signature.SignerCertificate.Thumbprint -ine $ExpectedSignerThumbprint -or
        $null -eq $signature.TimeStamperCertificate -or
        $signature.TimeStamperCertificate.Thumbprint -ine $ExpectedTimestampThumbprint) {
        throw "Windows Authenticode trust failed: [$resolved] status [$($signature.Status)]"
    }
    Invoke-SparrowSignatureVerification -Tool $tool -Artifact $resolved
    if ((Get-FileHash -LiteralPath $resolved -Algorithm SHA256).Hash -cne $before) { throw 'Artifact changed during verification' }
    Write-Host "Authenticode verified: $resolved signer=$ExpectedSignerThumbprint timestampSigner=$ExpectedTimestampThumbprint $evidence SHA256=$before"
}
