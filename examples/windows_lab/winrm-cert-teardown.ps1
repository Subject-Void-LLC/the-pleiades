#Requires -RunAsAdministrator
<#
.SYNOPSIS
    Removes everything winrm-cert-setup.ps1 created.

.DESCRIPTION
    Undoes the certificate lab and leaves the machine as close to its
    previous state as a script can. Every step is idempotent and every step
    that could remove something it did not create is scoped by the
    PleiadesGate tag, so running this on a machine that was never set up is
    harmless.

    Three things it does NOT do, each stated rather than silently skipped:

      - It does not stop or disable the WinRM service. winrm quickconfig
        may have started something a person wanted, and a teardown that
        turns off remote management is a bigger decision than this script
        should make on its own. Pass -StopWinRM to do it anyway.
      - It does not remove the HTTP listener on 5985, for the same reason:
        this script never opened it in the firewall and may not have
        created it.
      - It does not restore the RootSDDL. The grant it may have added is
        for Remote Management Users, a built-in group, and removing the
        entry blindly risks damaging an ACL somebody else edited since.
        Pass -RestoreSDDL to remove just that one entry.

.EXAMPLE
    .\winrm-cert-teardown.ps1
    .\winrm-cert-teardown.ps1 -StopWinRM -RestoreSDDL
#>
[CmdletBinding()]
param(
    [switch] $StopWinRM,
    [switch] $RestoreSDDL,
    [string] $LocalUser       = 'pleiades-gate',
    [string] $Upn             = 'pleiades-gate@pleiades.local',
    [string] $OutputDirectory = (Join-Path $env:USERPROFILE 'pleiades-gate')
)

$ErrorActionPreference = 'Continue'
$Tag = 'PleiadesGate'

function Write-Step($n, $text) { Write-Host "`n== $n. $text ==" -ForegroundColor Cyan }

Write-Step 1 'certificate-to-account mapping'
Get-ChildItem WSMan:\localhost\ClientCertificate -ErrorAction SilentlyContinue | ForEach-Object {
    if ((Get-ChildItem $_.PSPath | Where-Object Name -eq 'Subject').Value -eq $Upn) {
        Remove-Item $_.PSPath -Recurse -Force
        Write-Host "   removed mapping for $Upn"
    }
}

Write-Step 2 'HTTPS listener'
# Only a listener bound to this lab's own server certificate is removed, so
# a real HTTPS listener somebody else configured survives.
$ours = (Get-ChildItem Cert:\LocalMachine\My -ErrorAction SilentlyContinue |
         Where-Object FriendlyName -like "$Tag*").Thumbprint
Get-ChildItem WSMan:\localhost\Listener -ErrorAction SilentlyContinue | ForEach-Object {
    $props = Get-ChildItem $_.PSPath
    $transport  = ($props | Where-Object Name -eq 'Transport').Value
    $thumbprint = ($props | Where-Object Name -eq 'CertificateThumbprint').Value
    if ($transport -eq 'HTTPS' -and $ours -contains $thumbprint) {
        Remove-Item $_.PSPath -Recurse -Force
        Write-Host '   removed the HTTPS listener'
    }
}

Write-Step 3 'certificate authentication'
Set-Item WSMan:\localhost\Service\Auth\Certificate $false -ErrorAction SilentlyContinue
Write-Host '   disabled'

Write-Step 4 'certificates'
foreach ($store in 'Cert:\LocalMachine\My', 'Cert:\LocalMachine\Root', 'Cert:\LocalMachine\TrustedPeople') {
    Get-ChildItem $store -ErrorAction SilentlyContinue |
        Where-Object FriendlyName -like "$Tag*" |
        ForEach-Object { Remove-Item $_.PSPath -Force; Write-Host "   removed $($_.FriendlyName) from $store" }
}

Write-Step 5 'firewall rules'
Get-NetFirewallRule -DisplayName "$Tag*" -ErrorAction SilentlyContinue |
    ForEach-Object { Remove-NetFirewallRule -InputObject $_; Write-Host "   removed '$($_.DisplayName)'" }

Write-Step 6 "local account '$LocalUser'"
if (Get-LocalUser -Name $LocalUser -ErrorAction SilentlyContinue) {
    Remove-LocalUser -Name $LocalUser
    Write-Host '   removed'
    # The profile directory outlives the account and holds the desktop the
    # gate wrote its file to, so it is named rather than deleted: removing a
    # profile is not something a teardown should do without being asked.
    $profilePath = Join-Path $env:SystemDrive "Users\$LocalUser"
    if (Test-Path $profilePath) {
        Write-Host "   NOTE: the profile directory remains at $profilePath; delete it by hand if you want it gone"
    }
} else {
    Write-Host '   not present'
}

Write-Step 7 'exported files'
if (Test-Path $OutputDirectory) {
    Remove-Item $OutputDirectory -Recurse -Force
    Write-Host "   removed $OutputDirectory"
}

if ($RestoreSDDL) {
    Write-Step 8 'RootSDDL'
    $sddl = (Get-Item WSMan:\localhost\Service\RootSDDL).Value
    if ($sddl -match '\(A;;GA;;;RM\)') {
        Set-Item WSMan:\localhost\Service\RootSDDL ($sddl -replace '\(A;;GA;;;RM\)', '') -Force
        Write-Host '   removed the Remote Management Users grant'
    } else {
        Write-Host '   no grant to remove'
    }
}

if ($StopWinRM) {
    Write-Step 9 'WinRM service'
    Stop-Service WinRM -Force
    Set-Service WinRM -StartupType Manual
    Write-Host '   stopped and set to manual start'
} else {
    Restart-Service WinRM -ErrorAction SilentlyContinue
}

Write-Host "`n=== done ===" -ForegroundColor Green
Write-Host 'Two things winrm quickconfig changed that this script leaves alone:'
Write-Host '  - the WinRM firewall exception for the Private and Domain profiles'
Write-Host '  - LocalAccountTokenFilterPolicy, if it set it'
Write-Host 'Remove the second with:'
Write-Host "  Remove-ItemProperty 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Policies\System' -Name LocalAccountTokenFilterPolicy"
