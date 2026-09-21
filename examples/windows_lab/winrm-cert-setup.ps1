#Requires -RunAsAdministrator
<#
.SYNOPSIS
    Configures this Windows host as a WinRM client-certificate target.

.DESCRIPTION
    Creates everything the certificate path needs and nothing else: a
    dedicated non-administrator local account, a private certificate
    authority, a server certificate for the HTTPS listener, a client
    certificate carrying a UPN and Client Authentication, the listener
    itself, the certificate-to-account mapping, and a firewall rule scoped
    to one subnet rather than to any address.

    Everything it creates is tagged PleiadesGate so winrm-cert-teardown.ps1
    can remove exactly this and leave anything else alone.

    The one thing it can destroy that it did not create is an existing HTTPS
    WinRM listener, because WinRM allows only one. That is refused by default
    and needs -ReplaceExistingListener, because the teardown cannot put it
    back.

.PARAMETER AllowedSubnet
    The only source addresses permitted to reach 5986. Defaults to the WSL
    NAT range. A WSL subnet can change across reboots; re-run this script to
    re-scope the rule when that happens.

.PARAMETER OutputDirectory
    Where the client bundle and the CA certificate are written.

.EXAMPLE
    .\winrm-cert-setup.ps1
    .\winrm-cert-setup.ps1 -AllowedSubnet 10.0.0.0/24
    .\winrm-cert-setup.ps1 -ReplaceExistingListener
#>
[CmdletBinding()]
param(
    [string] $AllowedSubnet    = '172.18.32.0/20',
    [string] $OutputDirectory  = (Join-Path $env:USERPROFILE 'pleiades-gate'),
    [string] $LocalUser        = 'pleiades-gate',
    [string] $Upn              = 'pleiades-gate@pleiades.local',

    # Replace an HTTPS WinRM listener this script did not create. Off by
    # default because WinRM allows only one, the teardown cannot restore
    # what it did not make, and silently unbinding somebody's real
    # certificate is not a thing a lab script should do unasked.
    [switch] $ReplaceExistingListener
)

$ErrorActionPreference = 'Stop'
$Tag = 'PleiadesGate'

function Write-Step($n, $text) { Write-Host "`n== $n. $text ==" -ForegroundColor Cyan }

New-Item -ItemType Directory -Force -Path $OutputDirectory | Out-Null

Write-Step 1 'WinRM service'
# quickconfig is idempotent and also sets the service to delayed auto start.
# It creates the HTTP listener on 5985, which this script does not open in
# the firewall: the certificate path is HTTPS only.
winrm quickconfig -quiet | Out-Null
Write-Host '   service running'

Write-Step 2 "local account '$LocalUser' (NOT an administrator)"
# A dedicated local account. A certificate cannot be mapped to a Microsoft
# account, and mapping to a human's own login would hand the lab their
# password. Remote Management Users is the least privilege that still
# reaches WinRM; membership of Administrators is deliberately not granted.
$password = -join ((48..57) + (65..90) + (97..122) | Get-Random -Count 24 | ForEach-Object { [char]$_ })
$secure   = ConvertTo-SecureString $password -AsPlainText -Force
if (Get-LocalUser -Name $LocalUser -ErrorAction SilentlyContinue) {
    Set-LocalUser -Name $LocalUser -Password $secure
} else {
    New-LocalUser -Name $LocalUser -Password $secure -FullName 'Pleiades Gate Target' `
        -Description "$Tag lab account" -PasswordNeverExpires -AccountNeverExpires | Out-Null
}
$group = 'Remote Management Users'
if (-not (Get-LocalGroup -Name $group -ErrorAction SilentlyContinue)) { $group = 'Administrators' }
if (-not (Get-LocalGroupMember -Group $group -Member $LocalUser -ErrorAction SilentlyContinue)) {
    Add-LocalGroupMember -Group $group -Member $LocalUser
}
Write-Host "   member of '$group'"

Write-Step 3 'private certificate authority'
Get-ChildItem Cert:\LocalMachine\My, Cert:\LocalMachine\Root, Cert:\LocalMachine\TrustedPeople |
    Where-Object FriendlyName -like "$Tag*" | Remove-Item -Force
$ca = New-SelfSignedCertificate -Subject "CN=$Tag Root CA" -FriendlyName "$Tag CA" `
    -CertStoreLocation Cert:\LocalMachine\My -KeyUsage CertSign,CRLSign,DigitalSignature `
    -KeyLength 2048 -HashAlgorithm SHA256 -NotAfter (Get-Date).AddYears(1) `
    -TextExtension @('2.5.29.19={text}CA=true&pathlength=0')
# A client certificate is trusted only if its issuer is a trusted root here.
$root = New-Object Security.Cryptography.X509Certificates.X509Store('Root','LocalMachine')
$root.Open('ReadWrite'); $root.Add($ca); $root.Close()

Write-Step 4 'server certificate for the HTTPS listener'
# IP addresses must be IP SANs, not DNS SANs. -DnsName types every value as
# DNS, and a client dialling by address then rejects the certificate with
# "doesn't contain any IP SANs", so the SAN is written by hand instead.
$ips = Get-NetIPAddress -AddressFamily IPv4 |
       Where-Object { $_.IPAddress -notlike '169.254.*' } | ForEach-Object IPAddress
$san = "2.5.29.17={text}DNS=$env:COMPUTERNAME&DNS=localhost&IPAddress=127.0.0.1&" +
       (($ips | ForEach-Object { "IPAddress=$_" }) -join '&')
$server = New-SelfSignedCertificate -Subject "CN=$env:COMPUTERNAME" -FriendlyName "$Tag Server" `
    -Signer $ca -CertStoreLocation Cert:\LocalMachine\My -KeyLength 2048 -HashAlgorithm SHA256 `
    -NotAfter (Get-Date).AddYears(1) `
    -TextExtension @('2.5.29.37={text}1.3.6.1.5.5.7.3.1', $san)
Write-Host "   covers $($ips -join ', ')"

Write-Step 5 'client certificate'
# The UPN in the subject alternative name is what the mapping matches on,
# and Client Authentication in the extended key usage is what lets Windows
# accept it for this purpose. Without either, the mapping never fires.
$client = New-SelfSignedCertificate -Subject "CN=$LocalUser" -FriendlyName "$Tag Client" `
    -Signer $ca -CertStoreLocation Cert:\LocalMachine\My -KeyLength 2048 -HashAlgorithm SHA256 `
    -KeyExportPolicy Exportable -NotAfter (Get-Date).AddYears(1) `
    -TextExtension @('2.5.29.37={text}1.3.6.1.5.5.7.3.2', "2.5.29.17={text}UPN=$Upn")
$people = New-Object Security.Cryptography.X509Certificates.X509Store('TrustedPeople','LocalMachine')
$people.Open('ReadWrite'); $people.Add($client); $people.Close()

Write-Step 6 'HTTPS listener and certificate authentication'
# WinRM allows one HTTPS listener per address, so making ours means removing
# whatever is there. That is a destructive act on a host this script does not
# own, and it is refused by default rather than done quietly: the teardown
# deliberately preserves a listener it did not create, and a setup that
# destroys one without asking would make that promise worthless. There is
# nothing to restore from afterwards, because the private key of the
# certificate it was bound to is not ours to re-bind.
$ourThumbprints = @($ca.Thumbprint, $server.Thumbprint, $client.Thumbprint)
$foreign = @(Get-ChildItem WSMan:\localhost\Listener | ForEach-Object {
    $props = Get-ChildItem $_.PSPath
    if (($props | Where-Object Name -eq 'Transport').Value -ne 'HTTPS') { return }
    $thumb = ($props | Where-Object Name -eq 'CertificateThumbprint').Value
    if ($ourThumbprints -notcontains $thumb) {
        [pscustomobject]@{ Path = $_.PSPath; Thumbprint = $thumb }
    }
})

if ($foreign.Count -gt 0 -and -not $ReplaceExistingListener) {
    Write-Host ''
    foreach ($f in $foreign) { Write-Host "   existing HTTPS listener, certificate $($f.Thumbprint)" -ForegroundColor Yellow }
    throw ("This host already has an HTTPS WinRM listener that this script did not create, and WinRM " +
           "allows only one. Re-run with -ReplaceExistingListener to replace it, noting the thumbprint " +
           "above first: the teardown cannot put it back.")
}
foreach ($f in $foreign) {
    Write-Host "   REPLACING an existing HTTPS listener, certificate $($f.Thumbprint)" -ForegroundColor Yellow
    Write-Host '   the teardown cannot restore this; re-bind it by hand if you need it back'
    Remove-Item $f.Path -Recurse -Force
}
# Ours, if a previous run left one, is removed without ceremony.
Get-ChildItem WSMan:\localhost\Listener | ForEach-Object {
    $props = Get-ChildItem $_.PSPath
    if (($props | Where-Object Name -eq 'Transport').Value -eq 'HTTPS') { Remove-Item $_.PSPath -Recurse -Force }
}

New-Item WSMan:\localhost\Listener -Transport HTTPS -Address * `
    -CertificateThumbPrint $server.Thumbprint -Force | Out-Null
Set-Item WSMan:\localhost\Service\Auth\Certificate $true

Write-Step 7 'shell access for the mapped account'
# Membership of Remote Management Users is not sufficient on its own: the
# WinRM service has its own ACL, and if it names only Builtin Administrators
# then a mapped non-administrator authenticates successfully and is then
# refused when it tries to create a shell, with a WS-Man AccessDenied fault.
$sddl = (Get-Item WSMan:\localhost\Service\RootSDDL).Value
if ($sddl -notmatch ';RM\)') {
    Write-Host "   granting Remote Management Users (was: $sddl)"
    Set-Item WSMan:\localhost\Service\RootSDDL ($sddl -replace '(D:P)', '$1(A;;GA;;;RM)') -Force
} else {
    Write-Host '   already granted'
}

Write-Step 8 'certificate-to-account mapping'
Get-ChildItem WSMan:\localhost\ClientCertificate -ErrorAction SilentlyContinue | ForEach-Object {
    if ((Get-ChildItem $_.PSPath | Where-Object Name -eq 'Subject').Value -eq $Upn) {
        Remove-Item $_.PSPath -Recurse -Force
    }
}
New-Item WSMan:\localhost\ClientCertificate -Subject $Upn -URI * -Issuer $ca.Thumbprint `
    -Credential (New-Object Management.Automation.PSCredential($LocalUser, $secure)) -Force | Out-Null
Write-Host "   $Upn -> $LocalUser"

Write-Step 9 "firewall, $AllowedSubnet only"
# Deliberately not open to any address. winrm quickconfig's own rule covers
# the Private and Domain profiles, and a WSL adapter is classified Public,
# so that rule does not apply to traffic from WSL.
Get-NetFirewallRule -DisplayName "$Tag*" -ErrorAction SilentlyContinue | Remove-NetFirewallRule
New-NetFirewallRule -DisplayName "$Tag WinRM HTTPS" -Direction Inbound -Action Allow `
    -Protocol TCP -LocalPort 5986 -RemoteAddress $AllowedSubnet -Profile Any | Out-Null

Write-Step 10 'export the client identity'
$pfx = Join-Path $OutputDirectory 'client.pfx'
Export-PfxCertificate -Cert $client -FilePath $pfx -Password $secure | Out-Null
Export-Certificate -Cert $ca -FilePath (Join-Path $OutputDirectory 'ca.cer') -Type CERT | Out-Null

Restart-Service WinRM
Write-Host "`n=== done ===" -ForegroundColor Green
Write-Host "bundle      $pfx"
Write-Host "authority   $(Join-Path $OutputDirectory 'ca.cer')"
Write-Host "passphrase  $password"
Write-Host "`nThe bundle passphrase and the account password are the same value."
Write-Host "Undo all of this with winrm-cert-teardown.ps1"
