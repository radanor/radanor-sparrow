#Requires -Version 5.1
<#
.SYNOPSIS
    Contract tests for the CE-7A Windows validation harness.

.DESCRIPTION
    The validation harness only runs on Windows, so nothing in CI would notice a
    harness that calls the product API wrongly until it was run on a real Windows
    host - and then the mistake would surface as a product failure rather than a
    harness bug.

    This file closes that gap with two independent kinds of check:

      1. Static: the harness source is read and every API page limit it uses is
         checked against the bounds the gateway actually enforces, and endpoints
         that accept no query string are checked for query strings.
      2. Behavioural: the cursor-walking helpers are driven with mock pages, so a
         walk that loses rows, loops forever, or reports a clipped read as
         complete fails here instead of on Windows.

    It runs on any platform with PowerShell: no product, no Windows, no network.

.EXAMPLE
    pwsh ./validation/windows/Test-HarnessContract.ps1
#>
[CmdletBinding()]
param(
    [string]$HarnessPath = ''
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$scriptRoot = Split-Path -Parent $PSCommandPath
if (-not $HarnessPath) { $HarnessPath = Join-Path $scriptRoot 'Invoke-RadanorValidation.ps1' }
Import-Module (Join-Path $scriptRoot 'RadanorValidation.psm1') -Force

$script:Passed = 0
$script:Failures = @()

function Assert-True {
    param([Parameter(Mandatory)][bool]$Condition, [Parameter(Mandatory)][string]$What)
    if ($Condition) {
        $script:Passed++
        Write-Host ("  PASS  {0}" -f $What)
    } else {
        $script:Failures += $What
        Write-Host ("  FAIL  {0}" -f $What)
    }
}

# Models one API page: rows are newest-first (descending cursor) and the page
# advertises the cursor of its own last row, exactly as the core does
# (cmd/radanor-sparrow-core/monitoring_api.go sets next_before = page[last].ID).
function New-MockPage {
    param(
        [int]$Count,
        [int]$StartId,          # highest id on this page
        [int]$Total = 0,
        [switch]$NoMore         # data ended: omit next_before
    )
    $events = @()
    for ($i = 0; $i -lt $Count; $i++) {
        $id = $StartId - $i
        $events += [pscustomobject]@{ cursor = $id; event_id = "evt-$id" }
    }
    $page = [pscustomobject]@{ schema = 'test'; events = $events; total = $Total }
    if (-not $NoMore -and $Count -gt 0) {
        $page | Add-Member -NotePropertyName next_before -NotePropertyValue ($StartId - $Count + 1)
    }
    return $page
}

# ---------------------------------------------------------------------------
Write-Host ''
Write-Host 'Cursor walking (Get-PagedEvents)'

# A walk that must cross several pages and then end because the data ended.
$state = @{ calls = @() }
$request = {
    param($path)
    $state.calls += $path
    switch (@($state.calls).Count) {
        1 { return (New-MockPage -Count 50 -StartId 1000) }
        2 { return (New-MockPage -Count 50 -StartId 950) }
        3 { return (New-MockPage -Count 50 -StartId 900) }
        default { return (New-MockPage -Count 5 -StartId 850 -Total 155 -NoMore) }
    }
}
$result = Get-PagedEvents -Request $request -MaxRows 500

Assert-True -Condition ($result.Rows.Count -eq 155) -What 'a multi-page read returns every row across pages'
Assert-True -Condition ($result.Pages -eq 4) -What 'the walk follows the cursor until the data ends'
Assert-True -Condition (-not $result.Truncated) -What 'a completed walk is not reported as truncated'
Assert-True -Condition (-not $result.Failed) -What 'a completed walk is not reported as failed'
Assert-True -Condition (@($state.calls | Where-Object { $_ -notlike '*limit=50*' }).Count -eq 0) `
    -What 'every page request uses the API maximum page size of 50'
Assert-True -Condition (@($state.calls | Where-Object { $_ -notlike '*before=*' }).Count -eq 1) `
    -What 'only the first page omits the cursor; later pages carry it'
Assert-True -Condition ($state.calls[1] -like '*before=951*') `
    -What 'the cursor for page two is the last id of page one, as the API documents'
Assert-True -Condition ($result.Rows[0].cursor -eq 1000 -and $result.Rows[154].cursor -eq 846) `
    -What 'rows stay in the order the API returned them, newest first'

# A read that stops at the caller's cap while the API still offers more.
$state2 = @{ n = 0 }
$endless = {
    param($path)
    $state2.n++
    return (New-MockPage -Count 50 -StartId (100000 - (($state2.n - 1) * 50)))
}
$clipped = Get-PagedEvents -Request $endless -MaxRows 100

Assert-True -Condition ($clipped.Rows.Count -eq 100) -What 'a capped read stops at the requested row cap'
Assert-True -Condition ($clipped.Truncated) -What 'a read that stopped at the cap is reported as truncated'
Assert-True -Condition ($state2.n -eq 2) -What 'a capped read stops requesting pages once the cap is reached'

# Transport failures and empty results must not look like data.
$nullPage = Get-PagedEvents -Request { param($p) return $null } -MaxRows 50
Assert-True -Condition ($nullPage.Failed -and $nullPage.Rows.Count -eq 0) `
    -What 'a failed page (HTTP 400 or an unparseable body) is reported as failed, not as zero rows of data'

$emptyPage = Get-PagedEvents -Request { param($p) return (New-MockPage -Count 0) } -MaxRows 50
Assert-True -Condition ((-not $emptyPage.Failed) -and $emptyPage.Rows.Count -eq 0) `
    -What 'an empty first page is a valid empty result'

$shapeError = Get-PagedEvents -Request { param($p) return ([pscustomobject]@{ unexpected = 1 }) } -MaxRows 50
Assert-True -Condition $shapeError.Failed -What 'a page without an events array is reported as failed'

# A source filter must be escaped, not spliced in.
$escaped = @{ path = '' }
$record = { param($p) $escaped.path = $p; return (New-MockPage -Count 0) }
Get-PagedEvents -Request $record -Source 'windows.eventlog/ops' | Out-Null
Assert-True -Condition ($escaped.path -like '*source=windows.eventlog%2Fops*') `
    -What 'a source filter is URL-escaped before it reaches the URL'

# A cursor that never advances must terminate rather than loop.
$stuck = @{ n = 0 }
$stuckResult = Get-PagedEvents -Request {
        param($p)
        $stuck.n++
        if ($stuck.n -gt 50) { throw 'the walk did not terminate' }
        return [pscustomobject]@{ events = @([pscustomobject]@{ cursor = 7; event_id = 'evt-7' }); next_before = 7 }
    } -MaxRows 1000
Assert-True -Condition ($stuck.n -lt 10) -What 'a non-advancing cursor terminates the walk instead of looping'

# ---------------------------------------------------------------------------
Write-Host ''
Write-Host 'Cursor walking (Get-PagedInventory)'

$invState = @{ calls = @() }
$invRequest = {
    param($path)
    $invState.calls += $path
    switch (@($invState.calls).Count) {
        1 { return [pscustomobject]@{ items = @(1..50 | ForEach-Object { [pscustomobject]@{ name = "n$_" } }); total = 120; next_after = 'n50'; sources = @('registry') } }
        2 { return [pscustomobject]@{ items = @(51..100 | ForEach-Object { [pscustomobject]@{ name = "n$_" } }); total = 120; next_after = 'n100' } }
        default { return [pscustomobject]@{ items = @(101..120 | ForEach-Object { [pscustomobject]@{ name = "n$_" } }); total = 120 } }
    }
}
$inv = Get-PagedInventory -Request $invRequest -Kind software -MaxRows 500

Assert-True -Condition ($inv.Rows.Count -eq 120) -What 'an inventory walk returns every row across pages'
Assert-True -Condition ($inv.Total -eq 120) -What 'the API total is carried through so a clipped read is visible'
Assert-True -Condition ($inv.Sources -contains 'registry') -What 'declared sources come from the first page'
Assert-True -Condition (-not $inv.Truncated) -What 'a completed inventory walk is not reported as truncated'
Assert-True -Condition ($invState.calls[1] -like '*after=n50*') -What 'the inventory cursor is the name cursor the API returns'

$invClipped = Get-PagedInventory -Request { param($p) return [pscustomobject]@{ items = @(1..50 | ForEach-Object { [pscustomobject]@{ name = "n$_" } }); total = 5000; next_after = 'n50' } } -Kind endpoints -MaxRows 50
Assert-True -Condition ($invClipped.Truncated -and $invClipped.Total -eq 5000) `
    -What 'an inventory read that hit the cap is truncated and still reports the API total'

$badKindRejected = $false
try { Get-PagedInventory -Request { param($p) return $null } -Kind 'passwords' | Out-Null } catch { $badKindRejected = $true }
Assert-True -Condition $badKindRejected -What 'an unknown inventory kind is rejected before any request is made'

$badCapRejected = $false
try { Get-PagedEvents -Request { param($p) return $null } -MaxRows 0 | Out-Null } catch { $badCapRejected = $true }
Assert-True -Condition $badCapRejected -What 'a non-positive row cap is rejected'

# ---------------------------------------------------------------------------
Write-Host ''
Write-Host 'Static API contract of the harness source'

Assert-True -Condition (Test-Path -LiteralPath $HarnessPath) -What "the harness source exists ($HarnessPath)"
$source = Get-Content -LiteralPath $HarnessPath -Raw
$lines = Get-Content -LiteralPath $HarnessPath

# The gateway rejects a page limit above 50 with HTTP 400
# (internal/gateway/server.go maxMonitoringLimit,
#  internal/gateway/assessment.go maxAssessmentLimit,
#  internal/storage/store.go DefaultLimits().MaxPageSize).
$limitMatches = [regex]::Matches($source, 'limit=(\d+)')
$badLimits = @($limitMatches | ForEach-Object { [int]$_.Groups[1].Value } | Where-Object { $_ -gt 50 -or $_ -lt 1 })
Assert-True -Condition ($limitMatches.Count -gt 0) -What 'the harness uses the API at all (the limit regex matched)'
Assert-True -Condition ($badLimits.Count -eq 0) `
    -What ("no API page limit exceeds the enforced maximum of 50 (found {0} limit value(s): {1})" -f $limitMatches.Count, (($limitMatches | ForEach-Object { $_.Value } | Sort-Object -Unique) -join ', '))

# Endpoints that take no query string at all.
$noQueryPaths = @('/api/v1/assessment/checks', '/api/v1/assessment/status', '/api/v1/inventory/status',
    '/api/v1/monitoring/status', '/api/v1/foundation', '/api/v1/settings')
foreach ($p in $noQueryPaths) {
    $needle = [regex]::Escape("'$($p)?")
    $offenders = @($lines | Where-Object { $_ -match $needle })
    Assert-True -Condition ($offenders.Count -eq 0) -What "$p is never called with a query string"
}

# The endpoints the harness must not use at all (unbounded or unknown reads).
$forbidden = @($lines | Where-Object { $_ -match '/api/v1/monitoring/events\?(?![^\s'']*limit=)' })
Assert-True -Condition ($forbidden.Count -eq 0) -What 'monitoring events are always requested with an explicit page limit'

Write-Host ''
if ($script:Failures.Count -gt 0) {
    Write-Host ("FAILED: {0} of {1} checks" -f $script:Failures.Count, ($script:Passed + $script:Failures.Count))
    $script:Failures | ForEach-Object { Write-Host "  - $_" }
    exit 1
}
Write-Host ("OK: all {0} contract checks passed" -f $script:Passed)
exit 0
