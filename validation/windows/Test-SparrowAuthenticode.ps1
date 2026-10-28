# Disposable, untrusted test certificates only. Never changes a trust store.
param([Parameter(Mandatory=$true)][string]$SignTool,
      [Parameter(Mandatory=$true)][string]$UnsignedExecutable,
      [string]$MakeNsis = $env:MAKENSIS,
      [string]$SevenZip = $env:RADANOR_7ZIP)
$ErrorActionPreference = 'Stop'
# CI launches pwsh -> go -> powershell.exe. The inherited PSModulePath can put
# PowerShell 7's modules ahead of 5.1's, hiding Cert: and Utility's Get-FileHash.
# Import this host's built-in modules explicitly; never rely on profile/module
# discovery, create a replacement drive, or skip certificate acceptance.
foreach ($module in @('Microsoft.PowerShell.Security', 'Microsoft.PowerShell.Utility')) {
    $manifest = Join-Path $PSHOME "Modules/$module/$module.psd1"
    Import-Module -Name $manifest -ErrorAction Stop
}
$null = Get-Item -LiteralPath 'Cert:\CurrentUser\My' -ErrorAction Stop
Write-Host "Certificate fixture: PowerShell $($PSVersionTable.PSVersion) built-in modules from $PSHOME; CurrentUser My available"
# PowerShell 5.1 uses .NET Framework; hosted pwsh uses the .NET reference pack.
$signingReferences = @('System.Security', 'System.Core')
if ($PSVersionTable.PSEdition -eq 'Core') {
    $signingReferences = @((Get-ChildItem (Join-Path $PSHOME 'ref') -Filter '*.dll').FullName)
    $pkcs = [Security.Cryptography.Pkcs.SignedCms].Assembly.Location
    $signingReferences = @($signingReferences | Where-Object { [IO.Path]::GetFileName($_) -ne [IO.Path]::GetFileName($pkcs) }) + $pkcs
}
$temp = Join-Path ([IO.Path]::GetTempPath()) ('sparrow-signing-test-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $temp | Out-Null
$cert = $null
$tsaCert = $null
function Check([bool]$Condition, [string]$Message) { if (-not $Condition) { throw $Message } }
function Refused([scriptblock]$Action, [string]$Reason) {
    $message = ''
    try { & $Action | Out-Host } catch { $message = $_.Exception.ToString() }
    Check ($message.Contains($Reason)) "Expected refusal [$Reason], detected [$message]"
    Write-Host "PASS refusal: $Reason"
}
function CompileFixture([string]$Source) {
    # NSIS retains its temporary uninstaller on a failed finalizer. Keep that
    # file inside this fixture's owned root, including on the deliberate failure.
    $previousTEMP = $env:TEMP; $previousTMP = $env:TMP
    try { $env:TEMP = $temp; $env:TMP = $temp; & $MakeNsis -V2 $Source }
    finally { $env:TEMP = $previousTEMP; $env:TMP = $previousTMP }
}
try {
    $cert = New-SelfSignedCertificate -Type CodeSigningCert -Subject ('CN=Sparrow disposable acceptance ' + [guid]::NewGuid().ToString('N')) -CertStoreLocation Cert:\CurrentUser\My -KeyExportPolicy NonExportable -NotAfter (Get-Date).AddDays(1)
    if (-not ('SparrowAuthenticode' -as [type])) { Add-Type -Path (Join-Path $PSScriptRoot 'SparrowAuthenticode.cs') -ReferencedAssemblies $signingReferences }
    $unsigned = Join-Path $temp 'unsigned.exe'
    Copy-Item -LiteralPath $UnsignedExecutable -Destination $unsigned
    Check ((Get-AuthenticodeSignature -LiteralPath $unsigned).Status -eq 'NotSigned') 'An unsigned fixture is required'
    # Exercise only the native-tool boundary as a unit. This does not fake a
    # Windows trust verdict or bypass any gate in the full verifier below.
    $tokens = $null; $errors = $null
    $ast = [Management.Automation.Language.Parser]::ParseFile((Join-Path $PSScriptRoot 'Assert-SparrowAuthenticode.ps1'), [ref]$tokens, [ref]$errors)
    Check ($errors.Count -eq 0) 'Verifier parse error'
    $boundary = @($ast.FindAll({ param($node) $node -is [Management.Automation.Language.FunctionDefinitionAst] -and $node.Name -eq 'Invoke-SparrowSignatureVerification' }, $true))
    Check ($boundary.Count -eq 1) 'Verifier native-tool boundary missing'
    . ([scriptblock]::Create($boundary[0].Extent.Text))
    foreach ($code in @(1, 2, 17)) {
        $failureTool = Join-Path $temp ('failed-tool-' + $code + '.cmd')
        [IO.File]::WriteAllText($failureTool, "@echo off`r`nexit /b $code`r`n")
        Refused { Invoke-SparrowSignatureVerification -Tool $failureTool -Artifact $unsigned } "exit $code"
    }
    $signed = Join-Path $temp 'sha256.exe'; Copy-Item -LiteralPath $unsigned -Destination $signed
    & $SignTool sign /s My /sha1 $cert.Thumbprint /fd SHA256 $signed
    Check ($LASTEXITCODE -eq 0) 'Disposable SHA256 signing failed'
    Check ([SparrowAuthenticode]::PayloadHash($unsigned) -ceq [SparrowAuthenticode]::PayloadHash($signed)) 'Signing changed covered payload'
    $verifier = [IO.File]::ReadAllText((Join-Path $PSScriptRoot 'Assert-SparrowAuthenticode.ps1'))
    $verifier = [scriptblock]::Create($verifier.Replace(([char]36 + 'PSScriptRoot'), ("'" + $PSScriptRoot.Replace("'", "''") + "'")))
    $hash = (Get-FileHash -LiteralPath $SignTool -Algorithm SHA256).Hash
    $arguments = @{ ExpectedSignerThumbprint=$cert.Thumbprint; ExpectedTimestampThumbprint=$cert.Thumbprint; ExpectedTimestampPolicyOID='1.2.3'; SignTool=$SignTool; SignToolSHA256=$hash }
    Refused { & $verifier -Path $unsigned @arguments } 'Missing or malformed embedded signature'
    Refused { & $verifier -Path $signed @arguments } 'Missing RFC3161 timestamp evidence'
    $wrong = $arguments.Clone(); $wrong.ExpectedSignerThumbprint = '0000000000000000000000000000000000000000'
    Refused { & $verifier -Path $signed @wrong } 'Signer mismatch'
    $unsignedVerifier = [scriptblock]::Create([IO.File]::ReadAllText((Join-Path $PSScriptRoot 'Assert-SparrowUnsignedWindows.ps1')))
    & $unsignedVerifier -Path $unsigned
    Refused { & $unsignedVerifier -Path $signed } 'Unexpected PE signature table'
    $badTool = $arguments.Clone(); $badTool.SignToolSHA256 = ('0' * 64)
    Refused { & $verifier -Path $signed @badTool } 'SignTool checksum mismatch'
    $weak = Join-Path $temp 'sha1.exe'; Copy-Item -LiteralPath $unsigned -Destination $weak
    & $SignTool sign /s My /sha1 $cert.Thumbprint /fd SHA1 $weak
    Check ($LASTEXITCODE -eq 0) 'Disposable weak digest fixture signing failed'
    Refused { & $verifier -Path $weak @arguments } 'Unapproved digest OID'
    $tsaCert = New-SelfSignedCertificate -Type Custom -Subject ('CN=Sparrow disposable timestamp ' + [guid]::NewGuid().ToString('N')) -CertStoreLocation Cert:\CurrentUser\My -KeyExportPolicy NonExportable -TextExtension @('2.5.29.37={critical}{text}1.3.6.1.5.5.7.3.8') -NotAfter (Get-Date).AddDays(1)
    Add-Type -Path (Join-Path $PSScriptRoot 'AuthenticodeTestFixtures.cs') -ReferencedAssemblies $signingReferences
    $timestampArguments = $arguments.Clone(); $timestampArguments.ExpectedTimestampThumbprint = $tsaCert.Thumbprint
    foreach ($case in @(@('imprint','Timestamp message imprint mismatch'), @('time','Timestamp outside signer validity'), @('signature','signature'), @('untrusted','Timestamp certificate chain is not trusted'))) {
        $fixture = Join-Path $temp ('timestamp-' + $case[0] + '.exe')
        [AuthenticodeTestFixtures]::Timestamp($signed, $fixture, $tsaCert, $case[0])
        Refused { & $verifier -Path $fixture @timestampArguments } $case[1]
    }
    $wrongPolicy = $timestampArguments.Clone(); $wrongPolicy.ExpectedTimestampPolicyOID = '1.2.4'
    Refused { & $verifier -Path $fixture @wrongPolicy } 'Timestamp policy OID mismatch'
    $tampered = Join-Path $temp 'tampered.exe'; Copy-Item -LiteralPath $signed -Destination $tampered
    $bytes = [IO.File]::ReadAllBytes($tampered); $bytes[1024] = $bytes[1024] -bxor 1; [IO.File]::WriteAllBytes($tampered, $bytes)
    Check ((Get-AuthenticodeSignature -LiteralPath $tampered).Status -eq 'HashMismatch') 'Windows did not detect tampering'
    Refused { & $verifier -Path $tampered @arguments } 'Missing RFC3161 timestamp evidence'
    Refused { & $unsignedVerifier -Path $tampered } 'Unexpected PE signature table'
    $savedPreference = $ErrorActionPreference
    try { $ErrorActionPreference = 'Continue'; & $SignTool verify /pa /all /tw $signed 2>&1 | Out-Host }
    finally { $ErrorActionPreference = $savedPreference }
    Check ($LASTEXITCODE -ne 0) 'Windows unexpectedly trusts the disposable signer'
    Write-Host 'PASS actual Windows HashMismatch and untrusted certificate refusal'
    # Development records cannot enter production signing, regardless of keys.
    $portable = Join-Path $temp 'portable'; New-Item -ItemType Directory -Path $portable | Out-Null
    [IO.File]::WriteAllText((Join-Path $portable 'BUILD-INFO'), "variant=development`n")
    $signer = [scriptblock]::Create([IO.File]::ReadAllText((Join-Path $PSScriptRoot 'Sign-SparrowWindowsArtifact.ps1')))
    $savedEnabled = $env:RADANOR_WINDOWS_SIGNING
    try { $env:RADANOR_WINDOWS_SIGNING = 'enabled'; Refused { & $signer -Path $unsigned -Kind Portable -PortableDirectory $portable } 'Only release BUILD-INFO' }
    finally { $env:RADANOR_WINDOWS_SIGNING = $savedEnabled }
    if (-not $MakeNsis -or -not $SevenZip) { throw 'NSIS and 7-Zip required for embedded-uninstaller acceptance' }
    $audit = Join-Path $temp 'signed-uninstaller.exe'; $setup = Join-Path $temp 'setup.exe'
    $hook = Join-Path $temp 'sign-test.cmd'
    [IO.File]::WriteAllText($hook, ('@echo off' + "`r`n" + '"' + $SignTool + '" sign /s My /sha1 ' + $cert.Thumbprint + ' /fd SHA256 "%~1"' + "`r`nif errorlevel 1 exit /b 1`r`n" + 'copy /b "%~1" "' + $audit + '" >nul' + "`r`nif errorlevel 1 exit /b 1`r`nexit /b 0`r`n"))
    $source = @'
Unicode true
RequestExecutionLevel user
Name "Sparrow disposable signing fixture"
OutFile "@SETUP@"
!uninstfinalize '"@HOOK@" "%1"' = 0
Section
WriteUninstaller "$EXEDIR\Uninstall.exe"
SectionEnd
Section "Uninstall"
SectionEnd
'@
    $source = $source.Replace('@SETUP@', $setup).Replace('@HOOK@', $hook)
    $nsi = Join-Path $temp 'fixture.nsi'; [IO.File]::WriteAllText($nsi, $source)
    CompileFixture $nsi
    Check ($LASTEXITCODE -eq 0) 'NSIS signing fixture compile failed'
    $extract = Join-Path $temp 'extracted'
    & $SevenZip x -y ('-o' + $extract) $setup | Out-Host
    Check ($LASTEXITCODE -eq 0) 'Signed uninstaller extraction failed'
    $embedded = @(Get-ChildItem -LiteralPath $extract -Recurse -File -Filter Uninstall.exe)
    Check ($embedded.Count -eq 1) 'Embedded uninstaller missing/ambiguous'
    Check ((Get-FileHash -LiteralPath $embedded[0].FullName -Algorithm SHA256).Hash -ceq (Get-FileHash -LiteralPath $audit -Algorithm SHA256).Hash) 'NSIS embedded bytes differ from signed uninstaller'
    Check ((Get-AuthenticodeSignature -LiteralPath $embedded[0].FullName).SignerCertificate.Thumbprint -ceq $cert.Thumbprint) 'Embedded uninstaller signer differs'
    # The setup itself is unsigned, but its embedded uninstaller is signed.
    # Community verification must reject the INNER signature independently.
    Copy-Item -LiteralPath $unsigned -Destination (Join-Path $portable 'RadanorSparrow.exe')
    Refused { & $unsignedVerifier -Setup $setup -PortableDirectory $portable -SevenZip $SevenZip } 'Unexpected PE signature table'
    Refused { & $verifier -Path $embedded[0].FullName @arguments } 'Missing RFC3161 timestamp evidence'
    Refused { & $verifier -Path $embedded[0].FullName @wrong } 'Signer mismatch'
    Write-Host 'PASS exact signed-uninstaller reconstruction and signer identity (untrusted disposable certificate)'
    [IO.File]::WriteAllText($hook, "@echo off`r`nexit /b 17`r`n")
    try { $ErrorActionPreference = 'Continue'; CompileFixture $nsi 2>&1 | Out-Host }
    finally { $ErrorActionPreference = $savedPreference }
    Check ($LASTEXITCODE -ne 0) 'NSIS ignored failing uninstaller signer'
    Write-Host 'PASS failed uninstaller signer aborts compilation'
    Write-Host 'NOT RUN: production trust, approved RFC3161 TSA, expired certificate with trusted timestamp'
} finally {
    if ($null -ne $cert) { Remove-Item -LiteralPath ('Cert:\CurrentUser\My\' + $cert.Thumbprint) -DeleteKey -ErrorAction Stop }
    if ($null -ne $tsaCert) { Remove-Item -LiteralPath ('Cert:\CurrentUser\My\' + $tsaCert.Thumbprint) -DeleteKey -ErrorAction Stop }
    Remove-Item -LiteralPath $temp -Recurse -Force
}
