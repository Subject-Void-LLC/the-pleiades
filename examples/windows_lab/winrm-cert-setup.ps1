#Requires -RunAsAdministrator
<#
.SYNOPSIS
    Configures this Windows host as a WinRM client-certificate target, for
    one least-privilege account.

.DESCRIPTION
    Creates everything the certificate path needs and nothing else, and
    gives the account it creates only what a lab run needs: to be reached
    over WinRM from one subnet, to open a shell, and to read and write the
    folders named on the command line. Everything it creates is tagged
    PleiadesGate, and what it grants is recorded in lab-state.json, so
    winrm-cert-teardown.ps1 removes exactly this and nothing else.

    What the account gets:
      - Access to WinRM through the service's own ACL (RootSDDL), granted to
        this account's SID alone with -ShellRights (read and execute by
        default), not to a group and not full control.
      - A certificate, mapped to it, that works over HTTPS on 5986 from
        -AllowedSubnet only.
      - Read access to each -ReadPath and modify access to each -WritePath,
        and on every fixed drive other than the system drive, nothing: it is
        denied at each root, and the named folders' own grants override the
        inherited deny. Setting that deny writes it into every file on those
        drives once, so the first run takes a while (-KeepOtherDrivesOpen
        skips it, and says so).
      - On the system drive, what any standard user has, except creating
        entries at the drive root or the ProgramData root, and writing
        under the Public profile.

    What it does not get, and why:
      - A password anyone knows. The password is random, is used once to
        map the certificate, and is never shown or written anywhere; the
        certificate is the only way in. The PFX passphrase is a different
        random value.
      - Membership of Remote Management Users, which also opens remote WMI
        access. -AddToRemoteManagementUsers adds it if this host turns out
        to need it for a shell.
      - Console, Remote Desktop, batch or service logon (-DenyRights). If a
        run shows the WinRM certificate logon itself needs one of these,
        drop that one from -DenyRights deliberately.
      - Administrator rights of any kind.

    What it changes on the machine beyond the account:
      - It starts the WinRM service. It does NOT run winrm quickconfig,
        which on a machine outside a domain sets LocalAccountTokenFilterPolicy
        (every local administrator gets an unfiltered token remotely) and
        opens the HTTP listener on 5985 to the whole Private network.
      - A private certificate authority, used to issue the server and client
        certificates and then deleted with its private key. Only its public
        certificate stays, in Trusted Root, so nothing on this machine can
        issue another certificate it would trust. Re-run this script to
        renew.
      - The client's private key leaves this machine inside the PFX and is
        deleted from the machine's store. The host keeps only the client's
        public certificate.
      - Certificate authentication is turned on for WinRM.

    The one thing it can destroy that it did not create is an existing HTTPS
    WinRM listener, because WinRM allows only one. That is refused by default
    and needs -ReplaceExistingListener, because the teardown cannot put it
    back.

.PARAMETER AllowedSubnet
    The only source addresses permitted to reach 5986. Defaults to the WSL
    NAT range. A WSL subnet can change across reboots; re-run this script to
    re-scope the rule when that happens.

.PARAMETER ReadPath
    Folders the account may read, such as installation media.

.PARAMETER WritePath
    Folders the account may create and change files in, such as a VM
    folder. Created if missing.

.PARAMETER ShellRights
    The rights granted in WinRM's RootSDDL, as SDDL generic rights. GXGR
    (execute and read) is the least expected to open a shell; widen it only
    if a run is refused, and say why.

.PARAMETER OutputDirectory
    Where client.pfx, its passphrase file, ca.pem and lab-state.json are
    written, readable only by the user running this script.

.EXAMPLE
    .\winrm-cert-setup.ps1
    .\winrm-cert-setup.ps1 -ReadPath G:\iso -WritePath G:\PleiadesLab
    .\winrm-cert-setup.ps1 -AllowedSubnet 10.0.0.0/24 -ReplaceExistingListener
#>
[CmdletBinding()]
param(
    [string]   $AllowedSubnet   = '172.18.32.0/20',
    [string[]] $ReadPath        = @(),
    [string[]] $WritePath       = @(),
    [string]   $ShellRights     = 'GXGR',
    [string[]] $DenyRights      = @('SeDenyInteractiveLogonRight', 'SeDenyRemoteInteractiveLogonRight',
                                    'SeDenyBatchLogonRight', 'SeDenyServiceLogonRight'),
    [string]   $OutputDirectory = (Join-Path $env:USERPROFILE 'pleiades-gate'),
    [string]   $LocalUser       = 'pleiades-gate',
    [string]   $Upn             = 'pleiades-gate@pleiades.local',
    [switch]   $AddToRemoteManagementUsers,

    # Leave the account a standard user's access to the other fixed drives
    # instead of denying it there. Off by default: see step 4.
    [switch]   $KeepOtherDrivesOpen,

    # Replace an HTTPS WinRM listener this script did not create. Off by
    # default because WinRM allows only one, the teardown cannot restore
    # what it did not make, and silently unbinding somebody's real
    # certificate is not a thing a lab script should do unasked.
    [switch]   $ReplaceExistingListener
)

$ErrorActionPreference = 'Stop'
$Tag = 'PleiadesGate'

function Write-Step($n, $text) { Write-Host "`n== $n. $text ==" -ForegroundColor Cyan }

# Shared with the teardown, so granting and revoking use the same code.
. (Join-Path $PSScriptRoot 'winrm-lab-common.ps1')

# An X509Certificate2 holding only a certificate's public part, so adding it
# to a store cannot carry a link to the private key along with it.
function Get-PublicOnly($cert, [string] $friendlyName) {
    $public = New-Object Security.Cryptography.X509Certificates.X509Certificate2 -ArgumentList (, $cert.RawData)
    $public.FriendlyName = $friendlyName
    $public
}

# Adds a certificate to a LocalMachine store by name.
function Add-ToStore($cert, [string] $storeName) {
    $store = New-Object Security.Cryptography.X509Certificates.X509Store($storeName, 'LocalMachine')
    $store.Open('ReadWrite')
    try { $store.Add($cert) } finally { $store.Close() }
}

Write-Step 1 'WinRM service, without quickconfig'
Set-Service WinRM -StartupType Automatic
Start-Service WinRM
Write-Host '   running; no HTTP listener, firewall rule or token policy was changed'

Write-Step 2 "local account '$LocalUser' (standard user, no password anyone knows)"
$password = ConvertTo-SecureString (New-Secret 24) -AsPlainText -Force
if (Get-LocalUser -Name $LocalUser -ErrorAction SilentlyContinue) {
    Set-LocalUser -Name $LocalUser -Password $password -PasswordNeverExpires $true -UserMayChangePassword $false
} else {
    New-LocalUser -Name $LocalUser -Password $password -FullName 'Pleiades Gate Target' `
        -Description "$Tag lab: WinRM by certificate only" -PasswordNeverExpires `
        -AccountNeverExpires -UserMayNotChangePassword | Out-Null
}
$sid = (Get-LocalUser -Name $LocalUser).SID.Value
# A service identity, not a person: keep it off the sign-in screen.
$hideKey = 'HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion\Winlogon\SpecialAccounts\UserList'
New-Item -Path $hideKey -Force | Out-Null
New-ItemProperty -Path $hideKey -Name $LocalUser -Value 0 -PropertyType DWord -Force | Out-Null
$rmGroup = 'S-1-5-32-580'
$isMember = [bool](Get-LocalGroupMember -SID $rmGroup -ErrorAction SilentlyContinue | Where-Object { $_.SID.Value -eq $sid })
if ($AddToRemoteManagementUsers -and -not $isMember) {
    Add-LocalGroupMember -SID $rmGroup -Member $sid
    Write-Host '   added to Remote Management Users, as asked'
} elseif (-not $AddToRemoteManagementUsers -and $isMember) {
    Remove-LocalGroupMember -SID $rmGroup -Member $sid
    Write-Host '   removed from Remote Management Users, which an earlier run granted'
}
Write-Host "   $sid"

Write-Step 3 'logon rights denied'
if ($DenyRights.Count -gt 0) {
    Set-DenyRights -Sid $sid -Rights $DenyRights
    $DenyRights | ForEach-Object { Write-Host "   $_" }
} else {
    Write-Host '   none, as asked'
}

Write-Step 4 'file system'
# On the system drive the account keeps a standard user's access, which
# Windows and VirtualBox need, minus creating entries at the drive root and
# at the ProgramData root (neither inherited, so nothing below changes) and
# writing anywhere under the Public profile.
$systemRoot = [IO.Path]::GetPathRoot($env:SystemRoot)
$programData = [Environment]::GetFolderPath('CommonApplicationData')
$publicProfile = $env:PUBLIC
$failed = 0
$failed += Invoke-Icacls $systemRoot /deny "*${sid}:(WD,AD)"
$failed += Invoke-Icacls $programData /deny "*${sid}:(WD,AD)"
$failed += Invoke-Icacls $publicProfile /deny "*${sid}:(OI)(CI)(W,D,DC)"
Write-Host "   ${systemRoot}, ${programData}: no new entries at the top; ${publicProfile}: read only"

# Every other fixed drive grants Authenticated Users Modify by default, so
# the account is denied everything there, and the folders named below get
# their own grants, which are nearer and so win. NTFS writes an inherited
# ACE into every file under the root it is set on, so this touches every
# file on those drives once (folders that block inheritance, such as
# WindowsApps, keep their own ACLs and are not touched). It is timed.
$deniedRoots = @()
if ($KeepOtherDrivesOpen) {
    Write-Host '   other drives left open, as asked: the account can use them as any signed-in user can' -ForegroundColor Yellow
} else {
    foreach ($root in @(Get-OtherFixedDriveRoot)) {
        $clock = [Diagnostics.Stopwatch]::StartNew()
        $rootFailed = Invoke-Icacls $root /deny "*${sid}:(OI)(CI)(F)"
        $failed += $rootFailed
        $deniedRoots += $root
        # icacls reports on the root it was given; Windows then writes the
        # inherited entry into everything below and reports nothing per file.
        Write-Host ("   denied at {0} in {1:N0} s" -f $root, $clock.Elapsed.TotalSeconds)
        if ($rootFailed -gt 0) { Write-Host "   icacls reported $rootFailed failure(s) at $root" -ForegroundColor Yellow }
    }
}
foreach ($path in $ReadPath) {
    if (-not (Test-Path -LiteralPath $path -PathType Container)) { throw "ReadPath '$path' is not a folder" }
    $failed += Invoke-Icacls $path /grant "*${sid}:(OI)(CI)(RX)"
    Write-Host "   read: $path"
}
foreach ($path in $WritePath) {
    New-Item -ItemType Directory -Force -Path $path | Out-Null
    $failed += Invoke-Icacls $path /grant "*${sid}:(OI)(CI)(M)"
    Write-Host "   modify: $path"
}
if ($failed -gt 0) {
    Write-Host "   icacls reported $failed failure(s) in total; re-running this script retries them" -ForegroundColor Yellow
}

Write-Step 5 'certificates'
# A previous run's certificates go first, keys and all. The originals'
# thumbprints are kept: step 6 needs them to recognise the listener a
# previous run bound, which would otherwise look like somebody else's.
$previousThumbprints = @(Get-ChildItem Cert:\LocalMachine\My |
    Where-Object FriendlyName -like "$Tag *" | ForEach-Object Thumbprint)
Get-ChildItem Cert:\LocalMachine\Root, Cert:\LocalMachine\TrustedPeople |
    Where-Object { $_.FriendlyName -like "$Tag *" -or $_.Issuer -like "*CN=$Tag *" -or $_.Subject -like "*CN=$Tag *" } |
    Remove-Item -Force
Get-ChildItem Cert:\LocalMachine\My | Where-Object FriendlyName -like "$Tag *" |
    ForEach-Object { Remove-Item -Path "Cert:\LocalMachine\My\$($_.Thumbprint)" -DeleteKey -Force }

$ca = New-SelfSignedCertificate -Subject "CN=$Tag Root CA" -FriendlyName "$Tag CA" `
    -CertStoreLocation Cert:\LocalMachine\My -KeyUsage CertSign, CRLSign, DigitalSignature `
    -KeyLength 2048 -HashAlgorithm SHA256 -NotAfter (Get-Date).AddYears(1) `
    -TextExtension @('2.5.29.19={text}CA=true&pathlength=0')

# IP addresses must be IP SANs, not DNS SANs: -DnsName types every value as
# DNS, and a client dialling by address then rejects the certificate.
$ips = Get-NetIPAddress -AddressFamily IPv4 |
       Where-Object { $_.IPAddress -notlike '169.254.*' } | ForEach-Object IPAddress
$san = "2.5.29.17={text}DNS=$env:COMPUTERNAME&DNS=localhost&IPAddress=127.0.0.1&" +
       (($ips | ForEach-Object { "IPAddress=$_" }) -join '&')
$server = New-SelfSignedCertificate -Subject "CN=$env:COMPUTERNAME" -FriendlyName "$Tag Server" `
    -Signer $ca -CertStoreLocation Cert:\LocalMachine\My -KeyLength 2048 -HashAlgorithm SHA256 `
    -NotAfter (Get-Date).AddYears(1) -TextExtension @('2.5.29.37={text}1.3.6.1.5.5.7.3.1', $san)
Write-Host "   server certificate covers $($ips -join ', ')"

# The UPN in the subject alternative name is what the mapping matches, and
# Client Authentication in the extended key usage is what lets Windows
# accept it for this purpose.
$client = New-SelfSignedCertificate -Subject "CN=$LocalUser" -FriendlyName "$Tag Client" `
    -Signer $ca -CertStoreLocation Cert:\LocalMachine\My -KeyLength 2048 -HashAlgorithm SHA256 `
    -KeyExportPolicy Exportable -NotAfter (Get-Date).AddYears(1) `
    -TextExtension @('2.5.29.37={text}1.3.6.1.5.5.7.3.2', "2.5.29.17={text}UPN=$Upn")

# Trust goes in as public certificates only.
Add-ToStore (Get-PublicOnly $ca "$Tag CA") 'Root'
Add-ToStore (Get-PublicOnly $client "$Tag Client") 'TrustedPeople'

Write-Step 6 'HTTPS listener and certificate authentication'
# WinRM allows one HTTPS listener per address, so making ours means removing
# whatever is there, which is refused by default for a listener this script
# did not create.
$ourThumbprints = @($ca.Thumbprint, $server.Thumbprint, $client.Thumbprint) + $previousThumbprints
$foreign = @(Get-ChildItem WSMan:\localhost\Listener | ForEach-Object {
    $props = Get-ChildItem $_.PSPath
    if (($props | Where-Object Name -eq 'Transport').Value -ne 'HTTPS') { return }
    $thumb = ($props | Where-Object Name -eq 'CertificateThumbprint').Value
    if ($ourThumbprints -notcontains $thumb) {
        [pscustomobject]@{ Path = $_.PSPath; Thumbprint = $thumb }
    }
})
if ($foreign.Count -gt 0 -and -not $ReplaceExistingListener) {
    foreach ($f in $foreign) { Write-Host "   existing HTTPS listener, certificate $($f.Thumbprint)" -ForegroundColor Yellow }
    throw ("This host already has an HTTPS WinRM listener that this script did not create, and WinRM " +
           "allows only one. Re-run with -ReplaceExistingListener to replace it, noting the thumbprint " +
           "above first: the teardown cannot put it back.")
}
foreach ($f in $foreign) {
    Write-Host "   REPLACING an existing HTTPS listener, certificate $($f.Thumbprint)" -ForegroundColor Yellow
    Remove-Item $f.Path -Recurse -Force
}
Get-ChildItem WSMan:\localhost\Listener | ForEach-Object {
    $props = Get-ChildItem $_.PSPath
    if (($props | Where-Object Name -eq 'Transport').Value -eq 'HTTPS') { Remove-Item $_.PSPath -Recurse -Force }
}
New-Item WSMan:\localhost\Listener -Transport HTTPS -Address * `
    -CertificateThumbPrint $server.Thumbprint -Force | Out-Null
Set-Item WSMan:\localhost\Service\Auth\Certificate $true

Write-Step 7 "WinRM access for '$LocalUser' alone"
# The service has its own ACL, and a mapped account the ACL does not name
# authenticates and is then refused a shell. The grant names this account's
# SID, so no other account gains anything from it, and a previous run's
# grant for the same SID is replaced rather than duplicated.
$sddl = (Get-Item WSMan:\localhost\Service\RootSDDL).Value
$cleaned = $sddl -replace ("\(A;;[A-Z]+;;;" + [regex]::Escape($sid) + "\)"), ''
$granted = $cleaned -replace '(D:P)', ('$1' + "(A;;$ShellRights;;;$sid)")
Set-Item WSMan:\localhost\Service\RootSDDL $granted -Force
Write-Host "   $ShellRights for $sid"

Write-Step 8 'certificate-to-account mapping'
Get-ChildItem WSMan:\localhost\ClientCertificate -ErrorAction SilentlyContinue | ForEach-Object {
    if ((Get-ChildItem $_.PSPath | Where-Object Name -eq 'Subject').Value -eq $Upn) {
        Remove-Item $_.PSPath -Recurse -Force
    }
}
# The mapping is the one place the password is used; WinRM keeps it
# encrypted. Nothing else ever sees it.
New-Item WSMan:\localhost\ClientCertificate -Subject $Upn -URI * -Issuer $ca.Thumbprint `
    -Credential (New-Object Management.Automation.PSCredential($LocalUser, $password)) -Force | Out-Null
$password = $null
Write-Host "   $Upn -> $LocalUser"

Write-Step 9 "firewall, $AllowedSubnet only"
Get-NetFirewallRule -DisplayName "$Tag *" -ErrorAction SilentlyContinue | Remove-NetFirewallRule
New-NetFirewallRule -DisplayName "$Tag WinRM HTTPS" -Direction Inbound -Action Allow `
    -Protocol TCP -LocalPort 5986 -RemoteAddress $AllowedSubnet -Profile Any | Out-Null

Write-Step 10 'export the client identity, then delete keys this host does not need'
# The output directory is readable by the user running this script and by
# SYSTEM, and by nobody else; files written into it inherit that.
New-Item -ItemType Directory -Force -Path $OutputDirectory | Out-Null
& icacls $OutputDirectory /inheritance:r /grant:r "${env:USERDOMAIN}\${env:USERNAME}:(OI)(CI)F" '*S-1-5-18:(OI)(CI)F' | Out-Null
$pfxPath = Join-Path $OutputDirectory 'client.pfx'
$passphrasePath = Join-Path $OutputDirectory 'client.pfx.passphrase'
$passphrase = New-Secret 24
Export-PfxCertificate -Cert $client -FilePath $pfxPath `
    -Password (ConvertTo-SecureString $passphrase -AsPlainText -Force) | Out-Null
[IO.File]::WriteAllText($passphrasePath, $passphrase)
$passphrase = $null
# The authority as PEM, which is what a device's tls_ca_pem takes, so the
# host is verified without adding this lab's CA to the client's roots.
$caPemPath = Join-Path $OutputDirectory 'ca.pem'
$caPem = "-----BEGIN CERTIFICATE-----`n" +
    [Convert]::ToBase64String($ca.RawData, [Base64FormattingOptions]::InsertLineBreaks) +
    "`n-----END CERTIFICATE-----`n"
[IO.File]::WriteAllText($caPemPath, $caPem)
# The client's key now lives only in the PFX, and the CA's nowhere: neither
# is needed on this machine, and a CA key here could issue certificates this
# machine trusts.
Remove-Item -Path "Cert:\LocalMachine\My\$($client.Thumbprint)" -DeleteKey -Force
Remove-Item -Path "Cert:\LocalMachine\My\$($ca.Thumbprint)" -DeleteKey -Force
Write-Host '   client and CA private keys removed from this machine'

# What was granted, for the teardown. It names paths and a SID, no secret.
[ordered]@{
    sid          = $sid
    readPaths    = @($ReadPath)
    writePaths   = @($WritePath)
    deniedRoots  = @($deniedRoots)
    systemRoot   = $systemRoot
    programData  = $programData
    publicPath   = $publicProfile
    denyRights   = @($DenyRights)
    shellRights  = $ShellRights
} | ConvertTo-Json | Set-Content -Path (Join-Path $OutputDirectory 'lab-state.json') -Encoding UTF8

Restart-Service WinRM
Write-Host "`n=== done ===" -ForegroundColor Green
Write-Host "bundle      $pfxPath"
Write-Host "passphrase  $passphrasePath (readable by you and SYSTEM only)"
Write-Host "authority   $caPemPath"
Write-Host "`nAdd this host to Pleiades, pinning its authority, from the directory above:"
Write-Host '   pleiades add-host <device> --type windows_server --set host=<address> --set port=5986 --set "tls_ca_pem=$(cat ca.pem)"'
Write-Host "Import the bundle, then delete it and its passphrase file:"
Write-Host "   pleiades add-credential <device> --pfx client.pfx --passphrase-stdin < client.pfx.passphrase"
Write-Host "If a first connection is refused a shell, re-run with -ShellRights widened or"
Write-Host "-AddToRemoteManagementUsers; if the logon itself fails, drop one entry from -DenyRights."
Write-Host "Undo all of this with winrm-cert-teardown.ps1"
