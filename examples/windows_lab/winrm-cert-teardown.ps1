#Requires -RunAsAdministrator
<#
.SYNOPSIS
    Removes everything winrm-cert-setup.ps1 created.

.DESCRIPTION
    Undoes the certificate lab and leaves the machine as close to its
    previous state as a script can. Running it again is always safe and
    finishes whatever an earlier run could not, and it never removes
    something the lab did not create: certificates, listeners and firewall
    rules are matched by the PleiadesGate tag, the account and its profile
    by the account's own SID, and exported files by the two names the setup
    writes. Certificate authentication is turned off only when no other
    certificate mapping is left to need it.

    Private keys go with their certificates. Removing only the certificate
    leaves its key file on disk with nothing pointing at it.

    The lab account's profile goes with the account. It holds only what the
    lab put there (the gate writes its test file to that account's
    desktop). A profile still in use is left in place, account and all, and
    so is anything else a step could not remove: each is named again at the
    end, and running the script again once the cause has gone finishes the
    job.

    WinRM has to be running for its configuration to be read at all. If it
    is stopped, the script starts it for the duration and stops it again at
    the end, rather than skip the WinRM steps and then delete the
    certificate that is the only way to recognise the listener they missed.
    If it cannot be started (its startup type is Disabled), the script
    changes nothing and says so.

    What the setup granted the lab account is revoked by that account's
    SID, from the lab-state.json the setup wrote (or, without it, by
    finding the SID where the setup puts it): its WinRM access entry, its
    logon-right denials, its COM launch entries on VirtualBox's servers,
    its Cryptographic Services entry, the DisableForceUnload value it
    found, and every ACL entry it added, including the deny
    at the root of each other fixed drive, which is removed across every
    file it was written into.

    Four things it leaves in place, three of which a switch removes,
    because each may predate the lab and a teardown that undoes somebody
    else's configuration is worse than one that leaves a little behind.
    The current setup changes none of them; an older one ran winrm
    quickconfig, which did:

      - The WinRM service. winrm quickconfig may have started something a
        person wanted, and a teardown that turns off remote management is a
        bigger decision than this script should make on its own. Pass
        -StopWinRM to stop it and set it to manual start.
      - The Remote Management Users grant in the RootSDDL. Removing the
        entry blindly risks damaging an ACL somebody else edited since.
        Pass -RestoreSDDL to remove just that one entry, whoever added it.
      - LocalAccountTokenFilterPolicy, which winrm quickconfig sets on a
        machine outside a domain so that a local administrator gets an
        unfiltered token over a remote connection. Pass
        -RestoreTokenFilterPolicy to remove it, which returns to the Windows
        default of filtering that token.
      - The HTTP listener on 5985 and the firewall exception winrm
        quickconfig made for it. This script never opened 5985 and may not
        have created either; with -StopWinRM nothing is listening anyway.

.EXAMPLE
    .\winrm-cert-teardown.ps1
    .\winrm-cert-teardown.ps1 -StopWinRM -RestoreSDDL -RestoreTokenFilterPolicy
#>
[CmdletBinding()]
param(
    [switch] $StopWinRM,
    [switch] $RestoreSDDL,
    [switch] $RestoreTokenFilterPolicy,
    [string] $LocalUser       = 'pleiades-gate',
    [string] $Upn             = 'pleiades-gate@pleiades.local',
    [string] $OutputDirectory = (Join-Path $env:USERPROFILE 'pleiades-gate')
)

$ErrorActionPreference = 'Continue'
$Tag = 'PleiadesGate'

# Shared with the setup, so revoking uses the same code that granted.
. (Join-Path $PSScriptRoot 'winrm-lab-common.ps1')

# What the setup granted, if it recorded it: the account's SID and every
# path it changed. Read before step 9 deletes it.
$statePath = Join-Path $OutputDirectory 'lab-state.json'
$state = $null
if (Test-Path -LiteralPath $statePath) { $state = Get-Content -LiteralPath $statePath -Raw | ConvertFrom-Json }

# Every tagged name is the tag, a space, and a role ('PleiadesGate CA'), so
# the space is part of the match and 'PleiadesGateway' is not ours.
$TagPattern = "$Tag *"

# What this run could not remove. Each is printed where it happens and
# again at the end, so that a reader of the last screen is not told the job
# is done when it is not.
$leftovers = New-Object System.Collections.Generic.List[string]

function Write-Step($n, $text) { Write-Host "`n== $n. $text ==" -ForegroundColor Cyan }

# Records something this run left behind and says so now.
function Write-Leftover($text) {
    Write-Host "   WARNING: $text" -ForegroundColor Yellow
    $leftovers.Add($text)
}

# Where a certificate's private key file is, or $null when that cannot be
# worked out. Used only to confirm a key really went, because
# Remove-Item -DeleteKey reports nothing either way.
function Get-KeyFile($cert) {
    $rsa = $null
    try {
        $rsa = [Security.Cryptography.X509Certificates.RSACertificateExtensions]::GetRSAPrivateKey($cert)
        if ($rsa -is [Security.Cryptography.RSACng]) {
            $name = $rsa.Key.UniqueName
            $dirs = @("$env:ProgramData\Microsoft\Crypto\Keys", "$env:ProgramData\Microsoft\Crypto\SystemKeys")
        } elseif ($rsa -is [Security.Cryptography.RSACryptoServiceProvider]) {
            $name = $rsa.CspKeyContainerInfo.UniqueKeyContainerName
            $dirs = @("$env:ProgramData\Microsoft\Crypto\RSA\MachineKeys")
        } else {
            return $null
        }
        foreach ($dir in $dirs) {
            $path = Join-Path $dir $name
            if (Test-Path -LiteralPath $path) { return $path }
        }
    } catch {
        # A key that cannot be opened cannot be located either; the caller
        # reports that it could not confirm, rather than that it succeeded.
    } finally {
        if ($rsa) { $rsa.Dispose() }
    }
    return $null
}

# Every HTTPS listener, with the thumbprint of the certificate it is bound to.
function Get-HttpsListener {
    Get-ChildItem WSMan:\localhost\Listener -ErrorAction SilentlyContinue | ForEach-Object {
        $props = Get-ChildItem $_.PSPath
        if (($props | Where-Object Name -eq 'Transport').Value -eq 'HTTPS') {
            [pscustomobject]@{
                Path       = $_.PSPath
                Thumbprint = ($props | Where-Object Name -eq 'CertificateThumbprint').Value
            }
        }
    }
}

# Whether a certificate belongs to the lab: tagged, the same certificate as
# a tagged one in My, or naming the lab CA as its subject or issuer. More
# than one test because a copy in Root or TrustedPeople is not guaranteed to
# have kept its friendly name, the original in My may already be gone, and
# a lab CA left trusted is the one leftover here that matters.
function Test-LabCertificate($cert) {
    $cert.FriendlyName -like $TagPattern -or $ours -contains $cert.Thumbprint -or
        $cert.Subject -like "*CN=$TagPattern" -or $cert.Issuer -like "*CN=$TagPattern"
}

Write-Step 1 'WinRM service'
$winrmWasRunning = (Get-Service WinRM).Status -eq 'Running'
if ($winrmWasRunning) {
    Write-Host '   running'
} else {
    try {
        Start-Service WinRM -ErrorAction Stop
        Write-Host '   was stopped; started for this run, and stopped again at the end'
    } catch {
        Write-Host "   WinRM is stopped and could not be started: $($_.Exception.Message)" -ForegroundColor Yellow
        Write-Host '   Nothing was changed. Set its startup type to Manual and run this again.' -ForegroundColor Yellow
        exit 1
    }
}

Write-Step 2 'certificate-to-account mapping'
Get-ChildItem WSMan:\localhost\ClientCertificate -ErrorAction SilentlyContinue | ForEach-Object {
    $mapping = $_
    if ((Get-ChildItem $mapping.PSPath | Where-Object Name -eq 'Subject').Value -eq $Upn) {
        try {
            Remove-Item $mapping.PSPath -Recurse -Force -ErrorAction Stop
            Write-Host "   removed mapping for $Upn"
        } catch {
            Write-Leftover "the certificate mapping for $Upn, which could not be removed: $($_.Exception.Message)"
        }
    }
}

Write-Step 3 'HTTPS listener'
# Only a listener bound to this lab's own server certificate is removed, so
# a real HTTPS listener somebody else configured survives.
$ours = @(Get-ChildItem Cert:\LocalMachine\My -ErrorAction SilentlyContinue |
          Where-Object FriendlyName -like $TagPattern | ForEach-Object Thumbprint)
Get-HttpsListener | Where-Object { $ours -contains $_.Thumbprint } | ForEach-Object {
    $listener = $_
    try {
        Remove-Item $listener.Path -Recurse -Force -ErrorAction Stop
        Write-Host '   removed the HTTPS listener'
    } catch {
        Write-Host "   could not remove the HTTPS listener: $($_.Exception.Message)" -ForegroundColor Yellow
    }
}
# A listener still bound to a lab certificate keeps that certificate in
# step 5, because its thumbprint is the only thing that lets a later run
# recognise the listener as the lab's.
$stillBound = @(Get-HttpsListener | Where-Object { $ours -contains $_.Thumbprint } | ForEach-Object Thumbprint)
foreach ($thumbprint in $stillBound) {
    Write-Leftover "an HTTPS listener bound to lab certificate $thumbprint, and that certificate, kept so a later run can recognise the listener"
}
# A listener bound to a certificate that no longer exists at all cannot be
# proved to be the lab's, since the thumbprint was the proof, so it is named
# rather than removed. It serves nothing in that state, and it is what an
# earlier teardown leaves when it deleted the certificate but not the
# listener.
Get-HttpsListener |
    Where-Object { $ours -notcontains $_.Thumbprint -and -not (Test-Path "Cert:\LocalMachine\My\$($_.Thumbprint)") } |
    ForEach-Object {
        Write-Leftover "an HTTPS listener bound to certificate $($_.Thumbprint), which no longer exists; if it is the lab's, remove it with: Remove-Item '$($_.Path)' -Recurse"
    }

Write-Step 4 'certificate authentication'
# It is a machine-wide setting, so it is turned off only when no mapping is
# left that could need it. With none left, turning it off changes nothing
# for anyone else.
$remainingMappings = @(Get-ChildItem WSMan:\localhost\ClientCertificate -ErrorAction SilentlyContinue)
if ($remainingMappings.Count -gt 0) {
    Write-Host "   left on: $($remainingMappings.Count) certificate mapping(s) remain that may need it"
} else {
    try {
        Set-Item WSMan:\localhost\Service\Auth\Certificate $false -ErrorAction Stop
        Write-Host '   disabled'
    } catch {
        Write-Leftover "certificate authentication, which could not be disabled: $($_.Exception.Message)"
    }
}

Write-Step 5 'certificates and their private keys'
# The trust anchors go first, and without -DeleteKey: the copies in Root and
# TrustedPeople were added from the ones in My, a copy added that way can
# point at the same key container as the original, and the key is removed
# once, through the original, below.
foreach ($store in 'Cert:\LocalMachine\Root', 'Cert:\LocalMachine\TrustedPeople') {
    Get-ChildItem $store -ErrorAction SilentlyContinue | Where-Object { Test-LabCertificate $_ } | ForEach-Object {
        $cert = $_
        try {
            Remove-Item $cert.PSPath -Force -ErrorAction Stop
            Write-Host "   removed $($cert.Subject) from $store"
        } catch {
            Write-Leftover "$($cert.Subject) in $store, which could not be removed: $($_.Exception.Message)"
        }
    }
}
Get-ChildItem Cert:\LocalMachine\My -ErrorAction SilentlyContinue |
    Where-Object { $_.FriendlyName -like $TagPattern -and $stillBound -notcontains $_.Thumbprint } |
    ForEach-Object {
        $name    = $_.FriendlyName
        $hasKey  = $_.HasPrivateKey
        $keyFile = if ($hasKey) { Get-KeyFile $_ } else { $null }
        $path    = "Cert:\LocalMachine\My\$($_.Thumbprint)"
        try {
            if ($hasKey) {
                Remove-Item -Path $path -DeleteKey -Force -ErrorAction Stop
            } else {
                Remove-Item -Path $path -Force -ErrorAction Stop
            }
        } catch {
            Write-Leftover "$name, which could not be removed: $($_.Exception.Message)"
            return
        }
        if (-not $hasKey) {
            Write-Host "   removed $name"
        } elseif ($keyFile -and (Test-Path -LiteralPath $keyFile)) {
            Write-Leftover "the private key of $name, still at $keyFile"
        } elseif ($keyFile) {
            Write-Host "   removed $name and its private key"
        } else {
            Write-Host "   removed $name; its key file could not be located to confirm the key went"
        }
    }
foreach ($store in 'Cert:\LocalMachine\Root', 'Cert:\LocalMachine\TrustedPeople', 'Cert:\LocalMachine\My') {
    Get-ChildItem $store -ErrorAction SilentlyContinue |
        Where-Object { (Test-LabCertificate $_) -and $stillBound -notcontains $_.Thumbprint } |
        ForEach-Object { Write-Leftover "$($_.Subject) ($($_.Thumbprint)), still in $store" }
}

Write-Step 6 'firewall rules'
Get-NetFirewallRule -DisplayName $TagPattern -ErrorAction SilentlyContinue | ForEach-Object {
    $rule = $_
    try {
        Remove-NetFirewallRule -InputObject $rule -ErrorAction Stop
        Write-Host "   removed '$($rule.DisplayName)'"
    } catch {
        Write-Leftover "firewall rule '$($rule.DisplayName)', which could not be removed: $($_.Exception.Message)"
    }
}

Write-Step 7 "what '$LocalUser' was granted"
# By SID, from the setup's record or from the account itself, so nothing
# another account holds is touched.
$grantedSid = $null
if ($state -and $state.sid) { $grantedSid = [string]$state.sid }
elseif ($existing = Get-LocalUser -Name $LocalUser -ErrorAction SilentlyContinue) { $grantedSid = $existing.SID.Value }
if (-not $grantedSid) {
    Write-Host '   no account and no record of one; nothing to revoke'
} else {
    # Remote Management Users, which an older setup always granted.
    if (Get-LocalGroupMember -SID 'S-1-5-32-580' -ErrorAction SilentlyContinue | Where-Object { $_.SID.Value -eq $grantedSid }) {
        try {
            Remove-LocalGroupMember -SID 'S-1-5-32-580' -Member $grantedSid -ErrorAction Stop
            Write-Host '   removed from Remote Management Users'
        } catch { Write-Leftover "membership of Remote Management Users: $($_.Exception.Message)" }
    }
    # The RootSDDL entry naming this SID; it can name no one else.
    $sddl = (Get-Item WSMan:\localhost\Service\RootSDDL).Value
    $pattern = "\(A;;[A-Z]+;;;" + [regex]::Escape($grantedSid) + "\)"
    if ($sddl -match $pattern) {
        try {
            Set-Item WSMan:\localhost\Service\RootSDDL ($sddl -replace $pattern, '') -Force -ErrorAction Stop
            Write-Host '   removed its WinRM access entry'
        } catch { Write-Leftover "its RootSDDL entry: $($_.Exception.Message)" }
    }
    # The logon rights it was denied.
    $rights = @('SeDenyInteractiveLogonRight', 'SeDenyRemoteInteractiveLogonRight', 'SeDenyBatchLogonRight', 'SeDenyServiceLogonRight')
    if ($state -and $state.denyRights) { $rights = @($state.denyRights) }
    if ($rights.Count -gt 0) {
        try {
            Set-DenyRights -Sid $grantedSid -Rights $rights -Remove
            Write-Host "   removed from $($rights.Count) logon right(s)"
        } catch { Write-Leftover "its logon-right denials: $($_.Exception.Message)" }
    }
    # VirtualBox's COM servers. The revoke touches an AppID only where it
    # names this SID, so the recorded list and the installed one are both
    # safe to try.
    $comIds = @(@(Get-VirtualBoxAppId) + @(if ($state) { $state.comAppIds }) | Where-Object { $_ } | Sort-Object -Unique)
    foreach ($id in $comIds) {
        try {
            if (Set-ComLaunchGrant -AppId $id -Sid $grantedSid -Remove) { Write-Host "   removed its COM launch entry on $id" }
        } catch { Write-Leftover "its COM launch entry on ${id}: $($_.Exception.Message)" }
    }
    # Cryptographic Services, where the setup's entry names this SID alone.
    try {
        if (Set-ServiceGrant -Service CryptSvc -Sid $grantedSid -Remove) { Write-Host '   removed its Cryptographic Services entry' }
    } catch { Write-Leftover "its Cryptographic Services entry: $($_.Exception.Message)" }
    # The machine-wide policy, put back to what the setup found.
    if ($state -and $state.PSObject.Properties['forceUnloadPrior'] -and $null -ne $state.forceUnloadPrior) {
        try {
            Set-ForceUnloadPolicy $state.forceUnloadPrior
            Write-Host "   restored DisableForceUnload to $($state.forceUnloadPrior)"
        } catch { Write-Leftover "DisableForceUnload, which should be $($state.forceUnloadPrior): $($_.Exception.Message)" }
    }
    # File system. A drive-root deny is removed across every file it was
    # written into, which takes as long as setting it did.
    $denies = @()
    $grants = @()
    if ($state) {
        $denies = @($state.deniedRoots) + @($state.systemRoot, $state.programData, $state.publicPath)
        $grants = @($state.readPaths) + @($state.writePaths)
    } else {
        # No record: look for this SID on the places the setup changes,
        # and touch only one where it is actually present. The folders a
        # setup was given with -ReadPath and -WritePath are not knowable
        # without the record.
        $candidates = @(Get-OtherFixedDriveRoot) + @([IO.Path]::GetPathRoot($env:SystemRoot),
            [Environment]::GetFolderPath('CommonApplicationData'), $env:PUBLIC)
        $denies = @($candidates | Where-Object { Test-AclNames $_ $grantedSid })
        Write-Leftover "any grant on folders given to the setup with -ReadPath or -WritePath, since $statePath is missing; remove one with: icacls <folder> /remove *$grantedSid"
    }
    foreach ($path in @($denies | Where-Object { $_ })) {
        if (-not (Test-Path -LiteralPath $path)) { continue }
        $clock = [Diagnostics.Stopwatch]::StartNew()
        $failed = Invoke-Icacls $path /remove:d "*$grantedSid"
        if ($failed -gt 0) { Write-Leftover "its deny entry at $path, where icacls reported $failed failure(s)" }
        Write-Host ("   removed its deny entry from {0} in {1:N0} s" -f $path, $clock.Elapsed.TotalSeconds)
    }
    foreach ($path in @($grants | Where-Object { $_ })) {
        if (-not (Test-Path -LiteralPath $path)) { continue }
        $failed = Invoke-Icacls $path /remove:g "*$grantedSid"
        if ($failed -gt 0) { Write-Leftover "its grant at $path, where icacls reported $failed failure(s)" }
        Write-Host "   removed its grant from $path"
    }
}
$hideKey = 'HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion\Winlogon\SpecialAccounts\UserList'
if ($null -ne (Get-ItemProperty $hideKey -Name $LocalUser -ErrorAction SilentlyContinue)) {
    Remove-ItemProperty $hideKey -Name $LocalUser -ErrorAction SilentlyContinue
    Write-Host '   removed its sign-in screen entry'
}

Write-Step 8 "local account '$LocalUser' and its profile"
# The profile is found by the account's SID, so no other account's profile
# can match. It goes first, and the account only once it has gone: if the
# profile cannot be removed, the account is kept so that a later run can
# still find the profile through it. Remove-CimInstance takes the directory
# and its ProfileList registry entry together; deleting the folder by hand
# leaves that entry pointing at nothing.
$account = Get-LocalUser -Name $LocalUser -ErrorAction SilentlyContinue
if (-not $account) {
    Write-Host '   account not present'
} elseif ([int]($account.SID.Value -split '-')[-1] -lt 1000) {
    # Relative IDs below 1000 are the accounts Windows creates itself
    # (Administrator is 500, Guest 501), which no lab created.
    Write-Leftover "'$LocalUser', a built-in account; this script removes only the lab's own"
} else {
    $sidFilter = "SID = '$($account.SID.Value)'"
    $userProfile = Get-CimInstance Win32_UserProfile -Filter $sidFilter -ErrorAction SilentlyContinue
    if ($userProfile -and $userProfile.Loaded) {
        # A shell the gate left open keeps the profile loaded, and
        # restarting WinRM ends it. Looked at again after a pause, because a
        # profile unloads a moment after its last process exits.
        Restart-Service WinRM -ErrorAction SilentlyContinue
        Start-Sleep -Seconds 5
        $userProfile = Get-CimInstance Win32_UserProfile -Filter $sidFilter -ErrorAction SilentlyContinue
    }
    if ($userProfile -and $userProfile.Loaded) {
        Write-Leftover "the account '$LocalUser' and its profile at $($userProfile.LocalPath), because the profile is in use; run this again once that session has ended"
    } else {
        $profileGone = $true
        if ($userProfile) {
            try {
                Remove-CimInstance -InputObject $userProfile -ErrorAction Stop
                Write-Host "   removed the profile at $($userProfile.LocalPath)"
            } catch {
                $profileGone = $false
                Write-Leftover "the account '$LocalUser' and its profile at $($userProfile.LocalPath), which could not be removed ($($_.Exception.Message)); the account was kept so a later run can find the profile"
            }
        }
        if ($profileGone) {
            try {
                Remove-LocalUser -SID $account.SID -ErrorAction Stop
                Write-Host '   removed the account'
            } catch {
                Write-Leftover "the account '$LocalUser', which could not be removed: $($_.Exception.Message)"
            }
        }
    }
}
# A profile an earlier run left with no account, which an earlier version of
# this script did. One is recognised by three things at once: its folder is
# this account's name (or that name with the suffix Windows adds when the
# plain name is taken), its SID belongs to this machine's own accounts, and
# that SID no longer names any account. Only a local SID qualifies because
# a domain SID also fails to resolve whenever the domain controller is out
# of reach, which says nothing about whether the account still exists.
$machineSid = [string](Get-LocalUser | Select-Object -First 1).SID.AccountDomainSid
Get-CimInstance Win32_UserProfile -ErrorAction SilentlyContinue |
    Where-Object { $_.LocalPath -and -not $_.Loaded } |
    ForEach-Object {
        $candidate = $_
        $leaf = Split-Path $candidate.LocalPath -Leaf
        if ($leaf -ne $LocalUser -and $leaf -notlike "$LocalUser.*") { return }
        $sid = New-Object Security.Principal.SecurityIdentifier($candidate.SID)
        if ([string]$sid.AccountDomainSid -ne $machineSid) { return }
        try {
            [void]$sid.Translate([Security.Principal.NTAccount])
            return
        } catch [Security.Principal.IdentityNotMappedException] {
            # The account is gone; this is the orphan being looked for.
        } catch {
            return
        }
        try {
            Remove-CimInstance -InputObject $candidate -ErrorAction Stop
            Write-Host "   removed the orphaned profile at $($candidate.LocalPath)"
        } catch {
            Write-Leftover "the orphaned profile at $($candidate.LocalPath), which could not be removed: $($_.Exception.Message)"
        }
    }

Write-Step 9 'exported files'
# Only the files the setup writes, by literal path, and the directory
# only if that leaves it empty. The setup accepts a directory that already
# exists, so the directory itself is not evidence that everything in it is
# the lab's.
# ca.cer is what the setup wrote before it wrote ca.pem.
foreach ($file in 'client.pfx', 'client.pfx.passphrase', 'ca.pem', 'ca.cer', 'lab-state.json') {
    $path = Join-Path $OutputDirectory $file
    if (Test-Path -LiteralPath $path) {
        try {
            Remove-Item -LiteralPath $path -Force -ErrorAction Stop
            Write-Host "   removed $path"
        } catch {
            Write-Leftover "$path, which could not be removed: $($_.Exception.Message)"
        }
    }
}
if (Test-Path -LiteralPath $OutputDirectory) {
    $rest = @(Get-ChildItem -LiteralPath $OutputDirectory -Force)
    if ($rest.Count -eq 0) {
        Remove-Item -LiteralPath $OutputDirectory
        Write-Host "   removed $OutputDirectory"
    } else {
        Write-Host "   left $OutputDirectory in place; it holds $($rest.Count) item(s) the setup did not write"
    }
}

if ($RestoreSDDL) {
    Write-Step 10 'RootSDDL'
    $sddl = (Get-Item WSMan:\localhost\Service\RootSDDL).Value
    if ($sddl -match '\(A;;GA;;;RM\)') {
        try {
            Set-Item WSMan:\localhost\Service\RootSDDL ($sddl -replace '\(A;;GA;;;RM\)', '') -Force -ErrorAction Stop
            Write-Host '   removed the Remote Management Users grant'
        } catch {
            Write-Leftover "the Remote Management Users grant in RootSDDL, which could not be removed: $($_.Exception.Message)"
        }
    } else {
        Write-Host '   no grant to remove'
    }
}

if ($RestoreTokenFilterPolicy) {
    Write-Step 11 'LocalAccountTokenFilterPolicy'
    # Absent is the Windows default, and it takes effect at the next remote
    # sign-in with no restart.
    $policyKey = 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Policies\System'
    if ($null -ne (Get-ItemProperty $policyKey -Name LocalAccountTokenFilterPolicy -ErrorAction SilentlyContinue)) {
        try {
            Remove-ItemProperty $policyKey -Name LocalAccountTokenFilterPolicy -ErrorAction Stop
            Write-Host '   removed; a local administrator signing in remotely gets a filtered token again'
        } catch {
            Write-Leftover "LocalAccountTokenFilterPolicy, which could not be removed: $($_.Exception.Message)"
        }
    } else {
        Write-Host '   not set'
    }
}

Write-Step 12 'WinRM service'
if ($StopWinRM) {
    Stop-Service WinRM -Force -ErrorAction SilentlyContinue
    Set-Service WinRM -StartupType Manual -ErrorAction SilentlyContinue
    if ((Get-Service WinRM).Status -eq 'Stopped') {
        Write-Host '   stopped and set to manual start'
    } else {
        Write-Leftover 'the WinRM service, which could not be stopped'
    }
} elseif ($winrmWasRunning) {
    Restart-Service WinRM -ErrorAction SilentlyContinue
    Write-Host '   restarted, so it picks up the changes above'
} else {
    Stop-Service WinRM -Force -ErrorAction SilentlyContinue
    Write-Host '   stopped again, as it was before this run'
}

if ($leftovers.Count -gt 0) {
    Write-Host "`n=== done, but $($leftovers.Count) thing(s) were not removed ===" -ForegroundColor Yellow
    foreach ($item in $leftovers) { Write-Host "  - $item" -ForegroundColor Yellow }
    Write-Host 'Run this again, with the same switches, once the cause has gone; it picks up where this run stopped.'
} else {
    Write-Host "`n=== done ===" -ForegroundColor Green
}
Write-Host 'Left in place by design (see the top of this script):'
Write-Host '  - the HTTP listener on 5985 and its firewall exception, if present'
if ($winrmWasRunning -and -not $StopWinRM) { Write-Host '  - the WinRM service, still running (-StopWinRM stops it)' }
if (-not $RestoreSDDL)                     { Write-Host '  - the Remote Management Users grant in RootSDDL, if present (-RestoreSDDL removes it, whoever added it)' }
if (-not $RestoreTokenFilterPolicy)        { Write-Host '  - LocalAccountTokenFilterPolicy, if set (-RestoreTokenFilterPolicy removes it)' }
