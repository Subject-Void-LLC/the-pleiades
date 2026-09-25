<#
.SYNOPSIS
    Helpers shared by winrm-cert-setup.ps1 and winrm-cert-teardown.ps1.

.DESCRIPTION
    Dot-sourced by both scripts, so granting and revoking use the same code.
    Nothing here needs elevation to load, which is what lets the policy file
    editing be tested on its own; the functions that change the machine
    need it when they run.
#>

# A random secret from the operating system's cryptographic generator.
# Get-Random is not one in Windows PowerShell, so it is not used for this.
function New-Secret([int] $Bytes = 24) {
    $buffer = New-Object byte[] $Bytes
    $rng = [Security.Cryptography.RandomNumberGenerator]::Create()
    try { $rng.GetBytes($buffer) } finally { $rng.Dispose() }
    [Convert]::ToBase64String($buffer)
}

# The SID a secedit holder token names, or $null. secedit writes a
# well-known group as *S-1-5-32-..., but a local account it can resolve by
# NAME (Guest, or the lab account after it has been granted a right), so a
# token has to be resolved before it can be compared with a SID.
function Resolve-PolicyToken([string] $Token) {
    if ($Token.StartsWith('*')) { return $Token.Substring(1) }
    try {
        return (New-Object Security.Principal.NTAccount($Token)).Translate([Security.Principal.SecurityIdentifier]).Value
    } catch {
        return $null
    }
}

# Every holder token in a secedit export's [Privilege Rights] section,
# keyed by right.
function Get-RightsHolders([string[]] $Exported) {
    $current = @{}
    $inRights = $false
    foreach ($line in $Exported) {
        if ($line -match '^\s*\[(.+)\]\s*$') { $inRights = ($Matches[1] -eq 'Privilege Rights'); continue }
        if ($inRights -and $line -match '^\s*(Se\w+)\s*=\s*(.*)$') {
            $current[$Matches[1]] = @($Matches[2].Split(',') | ForEach-Object { $_.Trim() } | Where-Object { $_ })
        }
    }
    $current
}

# Returns a complete secedit policy file that sets only the rights in
# $Rights, each with $Sid added (or, with -Remove, taken out) and every
# other holder kept, because secedit replaces a right's whole list with
# what the file says. $Exported is a secedit /export of USER_RIGHTS.
# $Aliases are the other tokens that name the same account (its name, as
# secedit exports it), so a re-run does not list it twice and a removal
# takes whichever form is there. Pure text in and text out.
function Edit-RightsInf([string[]] $Exported, [string] $Sid, [string[]] $Rights, [switch] $Remove, [string[]] $Aliases = @()) {
    $current = Get-RightsHolders $Exported
    $out = @('[Unicode]', 'Unicode=yes', '[Version]', 'signature="$CHICAGO$"', 'Revision=1', '[Privilege Rights]')
    foreach ($right in $Rights) {
        $holders = @()
        if ($current.ContainsKey($right)) { $holders = @($current[$right]) }
        $holders = @($holders | Where-Object { $_ -ne "*$Sid" -and $Aliases -notcontains $_ })
        if (-not $Remove) { $holders += "*$Sid" }
        $out += "$right = " + ($holders -join ',')
    }
    $out
}

# Adds $Sid to (or, with -Remove, removes it from) each right in $Rights in
# this machine's local security policy. Needs elevation.
function Set-DenyRights([string] $Sid, [string[]] $Rights, [switch] $Remove) {
    $work = Join-Path $env:TEMP ('pleiades-rights-' + [guid]::NewGuid())
    New-Item -ItemType Directory -Path $work | Out-Null
    try {
        $exported = Join-Path $work 'current.inf'
        secedit /export /cfg $exported /areas USER_RIGHTS /quiet | Out-Null
        if ($LASTEXITCODE -ne 0) { throw "secedit /export exited $LASTEXITCODE" }
        $before = Get-Content $exported
        # The account's name, where secedit wrote one, counts as the account.
        $aliases = @((Get-RightsHolders $before).Values | ForEach-Object { $_ } |
            Where-Object { -not $_.StartsWith('*') -and (Resolve-PolicyToken $_) -eq $Sid } | Select-Object -Unique)
        $policy = Join-Path $work 'policy.inf'
        Edit-RightsInf $before $Sid $Rights -Remove:$Remove -Aliases $aliases | Set-Content -Path $policy -Encoding Unicode
        secedit /configure /db (Join-Path $work 'policy.sdb') /cfg $policy /areas USER_RIGHTS /quiet | Out-Null
        $configureExit = $LASTEXITCODE

        # Read the policy back rather than trust the exit code, which is
        # non-zero for warnings as well as failures: a right the file meant
        # to change and did not is a grant or a denial nobody intended, and
        # it should stop the script, not scroll past.
        $check = Join-Path $work 'after.inf'
        secedit /export /cfg $check /areas USER_RIGHTS /quiet | Out-Null
        $after = Get-RightsHolders (Get-Content $check)
        foreach ($right in $Rights) {
            # Resolved, not string-matched: secedit exports a local account
            # by name, so looking for *SID alone reports a right that took
            # effect as one that did not.
            $holders = @()
            if ($after.ContainsKey($right)) { $holders = @($after[$right]) }
            $present = @($holders | Where-Object { (Resolve-PolicyToken $_) -eq $Sid }).Count -gt 0
            if ($present -eq [bool]$Remove) {
                throw "$right still $(if ($Remove) { 'names' } else { 'lacks' }) $Sid after secedit /configure (exit $configureExit)"
            }
        }
    } finally {
        Remove-Item -LiteralPath $work -Recurse -Force -ErrorAction SilentlyContinue
    }
}

# Runs icacls with its arguments, continuing past files it cannot change,
# and returns how many files failed, from icacls' own summary line.
function Invoke-Icacls {
    $output = & icacls @args /c /q 2>&1
    $summary = @($output | Where-Object { "$_" -match 'Failed processing (\d+) files' })
    if ($summary.Count -gt 0 -and "$($summary[-1])" -match 'Failed processing (\d+) files') { return [int]$Matches[1] }
    return 0
}

# The root of every fixed NTFS drive except the system drive, which is
# where a standard user's default access is broader than a lab account
# needs and where no Windows component depends on that account.
function Get-OtherFixedDriveRoot {
    $system = [IO.Path]::GetPathRoot($env:SystemRoot)
    Get-Volume | Where-Object { $_.DriveLetter -and $_.DriveType -eq 'Fixed' -and $_.FileSystemType -eq 'NTFS' } |
        ForEach-Object { "$($_.DriveLetter):\" } | Where-Object { $_ -ne $system }
}

# Whether $Path's own ACL (not what it inherits) names $Sid.
function Test-AclNames([string] $Path, [string] $Sid) {
    $acl = Get-Acl -LiteralPath $Path -ErrorAction SilentlyContinue
    if (-not $acl) { return $false }
    foreach ($rule in $acl.GetAccessRules($true, $false, [Security.Principal.SecurityIdentifier])) {
        if ($rule.IdentityReference.Value -eq $Sid) { return $true }
    }
    $false
}
