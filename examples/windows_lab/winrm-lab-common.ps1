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

# COM local launch and local activation: COM_RIGHTS_EXECUTE, EXECUTE_LOCAL
# and ACTIVATE_LOCAL, CCDCSW in SDDL. The least a caller needs to start a
# COM server on this machine or to reach one already running, and what the
# machine-wide launch limit already allows Everyone.
$ComLocalLaunch = 0xB

# The AppIDs of the two COM servers VBoxManage starts, found by executable
# name as VirtualBox's installer registers them: VBoxSVC, the per-user API
# server, and VBoxSDS, the system service each VBoxSVC registers with.
# Returns nothing for one that is not registered.
function Get-VirtualBoxAppId {
    foreach ($exe in 'VBoxSVC.exe', 'VBoxSDS.exe') {
        $id = (Get-ItemProperty -LiteralPath "Registry::HKEY_LOCAL_MACHINE\SOFTWARE\Classes\AppID\$exe" `
            -Name AppID -ErrorAction SilentlyContinue).AppID
        if ($id) { $id }
    }
}

# Grants, or with -Remove revokes, COM local launch and activation on one
# AppID for one SID, and returns whether the AppID changed.
#
# An AppID with no LaunchPermission of its own uses the machine's
# DefaultLaunchPermission, which lets in Administrators, SYSTEM and
# interactive users but not a network logon such as WinRM's. So a grant
# starts from a copy of that default: starting from an empty list would
# lock out everyone the default lets in, including the person at the
# console. For the same reason a revoke that leaves exactly the default
# removes the value, which restores the AppID's behavior whatever runs came
# before. A revoke that finds no entry for the SID writes nothing.
#
# -Root is where AppIDs live; only a test points it elsewhere, so the logic
# can be exercised without writing to the machine's registry.
function Set-ComLaunchGrant([string] $AppId, [string] $Sid, [switch] $Remove,
                            [string] $Root = 'Registry::HKEY_LOCAL_MACHINE\SOFTWARE\Classes\AppID') {
    $key = Join-Path $Root $AppId
    $own = (Get-ItemProperty -LiteralPath $key -Name LaunchPermission -ErrorAction SilentlyContinue).LaunchPermission
    $default = (Get-ItemProperty -LiteralPath 'Registry::HKEY_LOCAL_MACHINE\SOFTWARE\Microsoft\Ole' `
        -Name DefaultLaunchPermission -ErrorAction SilentlyContinue).DefaultLaunchPermission
    if ($Remove -and -not $own) { return $false }
    $base = if ($own) { $own } else { $default }
    if (-not $base) { throw "$AppId has no launch permission of its own and this machine has no default to start from" }

    $descriptor = New-Object Security.AccessControl.RawSecurityDescriptor -ArgumentList $base, 0
    $target = New-Object Security.Principal.SecurityIdentifier -ArgumentList $Sid
    $acl = $descriptor.DiscretionaryAcl
    $removed = 0
    for ($i = $acl.Count - 1; $i -ge 0; $i--) {
        if ($acl[$i] -is [Security.AccessControl.CommonAce] -and $acl[$i].SecurityIdentifier -eq $target) {
            $acl.RemoveAce($i)
            $removed++
        }
    }
    if ($Remove -and $removed -eq 0) { return $false }
    if (-not $Remove) {
        $ace = New-Object Security.AccessControl.CommonAce -ArgumentList 'None', 'AccessAllowed', $ComLocalLaunch, $target, $false, $null
        $acl.InsertAce($acl.Count, $ace)
    }

    $defaultSddl = if ($default) { (New-Object Security.AccessControl.RawSecurityDescriptor -ArgumentList $default, 0).GetSddlForm('Access') }
    if ($Remove -and $descriptor.GetSddlForm('Access') -eq $defaultSddl) {
        Remove-ItemProperty -LiteralPath $key -Name LaunchPermission
    } else {
        $bytes = New-Object byte[] $descriptor.BinaryLength
        $descriptor.GetBinaryForm($bytes, 0)
        New-ItemProperty -LiteralPath $key -Name LaunchPermission -Value $bytes -PropertyType Binary -Force | Out-Null
    }
    $true
}

# Grants, or with -Remove revokes, one SID's entry in a service's own DACL,
# and returns whether the DACL changed. The entry is replaced, never
# duplicated, and nothing else in the DACL is touched. A SACL, when the
# service has one, is written back as it was read.
function Set-ServiceGrant([string] $Service, [string] $Sid, [string] $Rights, [switch] $Remove) {
    $sddl = ((& sc.exe sdshow $Service) -join '').Trim()
    if ($LASTEXITCODE -ne 0 -or -not $sddl.StartsWith('D:')) { throw "reading $Service's security descriptor failed: $sddl" }
    $descriptor = New-Object Security.AccessControl.RawSecurityDescriptor -ArgumentList $sddl
    $target = New-Object Security.Principal.SecurityIdentifier -ArgumentList $Sid
    $acl = $descriptor.DiscretionaryAcl
    $removed = 0
    for ($i = $acl.Count - 1; $i -ge 0; $i--) {
        if ($acl[$i] -is [Security.AccessControl.CommonAce] -and $acl[$i].SecurityIdentifier -eq $target) {
            $acl.RemoveAce($i)
            $removed++
        }
    }
    if ($Remove -and $removed -eq 0) { return $false }
    if (-not $Remove) {
        $template = New-Object Security.AccessControl.RawSecurityDescriptor -ArgumentList "D:(A;;$Rights;;;$Sid)"
        $acl.InsertAce($acl.Count, $template.DiscretionaryAcl[0])
    }
    $sacl = if ($sddl -match '(S:.*)$') { $Matches[1] } else { '' }
    $output = & sc.exe sdset $Service ($descriptor.GetSddlForm('Access') + $sacl)
    if ($LASTEXITCODE -ne 0) { throw "writing $Service's security descriptor failed: $output" }
    $true
}

# Where Windows reads "Do not forcefully unload the user registry at user
# logoff". Set, a user's registry stays loaded after that user's last
# session ends for as long as a process still holds it open.
$ForceUnloadPolicyKey = 'HKLM:\SOFTWARE\Policies\Microsoft\Windows\System'

# The policy's current value as recorded for a teardown: the number, or
# 'absent'.
function Get-ForceUnloadPolicy {
    $value = (Get-ItemProperty -Path $ForceUnloadPolicyKey -Name DisableForceUnload -ErrorAction SilentlyContinue).DisableForceUnload
    if ($null -eq $value) { 'absent' } else { [int]$value }
}

# Sets the policy to $Value, where 'absent' removes it.
function Set-ForceUnloadPolicy($Value) {
    if ("$Value" -eq 'absent') {
        Remove-ItemProperty -Path $ForceUnloadPolicyKey -Name DisableForceUnload -ErrorAction SilentlyContinue
    } else {
        New-Item -Path $ForceUnloadPolicyKey -Force | Out-Null
        New-ItemProperty -Path $ForceUnloadPolicyKey -Name DisableForceUnload -Value ([int]$Value) -PropertyType DWord -Force | Out-Null
    }
}

# VirtualBox's own autostart service, installed per account: the one way
# VirtualBox offers to start a VM under a service logon rather than the
# caller's.
$VBoxAutostartExe = Join-Path $env:ProgramFiles 'Oracle\VirtualBox\VBoxAutostartSvc.exe'

# The service name VBoxAutostartSvc gives an account on this machine: its
# fixed prefix, the computer name in lower case, and the account name.
function Get-VBoxAutostartServiceName([string] $LocalUser) {
    "VBoxAutostartSvc$($env:COMPUTERNAME.ToLower())$LocalUser"
}

# The machine environment variable VBoxAutostartSvc reads its policy file
# from, as recorded for a teardown: the value, or 'absent'.
function Get-AutostartConfigVariable {
    $value = [Environment]::GetEnvironmentVariable('VBOXAUTOSTART_CONFIG', 'Machine')
    if ($null -eq $value) { 'absent' } else { $value }
}

# Sets the variable to $Value, where 'absent' removes it.
function Set-AutostartConfigVariable([string] $Value) {
    if ($Value -eq 'absent') { $Value = $null }
    [Environment]::SetEnvironmentVariable('VBOXAUTOSTART_CONFIG', $Value, 'Machine')
}

# Removes the account's autostart service and the service-logon right its
# installer granted, and returns whether there was a service to remove.
function Remove-VBoxAutostart([string] $LocalUser, [string] $Sid) {
    $name = Get-VBoxAutostartServiceName $LocalUser
    if (-not (Get-Service -Name $name -ErrorAction SilentlyContinue)) { return $false }
    Stop-Service -Name $name -Force -ErrorAction SilentlyContinue
    $output = & $VBoxAutostartExe delete --user=".\$LocalUser" 2>&1
    if ($LASTEXITCODE -ne 0) { throw "removing $name failed: $output" }
    Set-DenyRights -Sid $Sid -Rights 'SeServiceLogonRight' -Remove
    $true
}
