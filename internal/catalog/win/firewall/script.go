// The PowerShell win.firewall.rule runs. It is a constant: every value it
// acts on arrives as JSON on standard input and is read with
// ConvertFrom-Json, so no value is ever PowerShell syntax, whatever it holds.
package firewall

// firewallScript reads {op, name, set} from standard input and answers one
// line of JSON describing the rule as it then is.
//
// Every read and write is of the local PersistentStore, the rules the
// machine keeps itself. A rule that is not there is answered as absent, with
// policy set when the active store still has one by that name (a Group Policy
// rule, which a change here would not keep); any other failure is an error,
// never "the rule does not exist". set's keys are the method's parameter
// names, mapped here onto the cmdlets' own; Set-NetFirewallRule renames with
// NewDisplayName. Like win.schtask's, its comments live here, since the
// script travels on a command line of at most 8191 characters
// (TestFirewallScript_FitsTheCommandLine).
const firewallScript = `$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = [Text.Encoding]::UTF8
$in = [Console]::In.ReadToEnd() | ConvertFrom-Json
$st = @{ PolicyStore = 'PersistentStore' }
function D {
try { $r = Get-NetFirewallRule -Name $in.name @st }
catch {
if ($_.CategoryInfo.Category -ne 'ObjectNotFound') { throw }
$p = $null
try { $p = Get-NetFirewallRule -Name $in.name -PolicyStore ActiveStore } catch {}
return @{ exists = $false; policy = [bool]$p }
}
$pf = $r | Get-NetFirewallPortFilter @st
$af = $r | Get-NetFirewallAddressFilter @st
@{
exists = $true
display_name = [string]$r.DisplayName
description = [string]$r.Description
group = [string]$r.Group
enabled = [string]$r.Enabled
direction = [string]$r.Direction
action = [string]$r.Action
profiles = [string]$r.Profile
protocol = [string]$pf.Protocol
local_port = @($pf.LocalPort | ForEach-Object { [string]$_ })
remote_port = @($pf.RemotePort | ForEach-Object { [string]$_ })
local_address = @($af.LocalAddress | ForEach-Object { [string]$_ })
remote_address = @($af.RemoteAddress | ForEach-Object { [string]$_ })
program = [string]($r | Get-NetFirewallApplicationFilter @st).Program
service = [string]($r | Get-NetFirewallServiceFilter @st).Service
}
}
$m = @{ display_name = 'DisplayName'; description = 'Description'; enabled = 'Enabled'; direction = 'Direction'; action = 'Action'
profiles = 'Profile'; protocol = 'Protocol'; local_port = 'LocalPort'; remote_port = 'RemotePort'; local_address = 'LocalAddress'
remote_address = 'RemoteAddress'; program = 'Program'; service = 'Service' }
$h = @{}
if ($in.set) { foreach ($k in $in.set.PSObject.Properties) { $h[$m[$k.Name]] = $k.Value } }
switch ($in.op) {
'create' { New-NetFirewallRule -Name $in.name @st @h | Out-Null }
'set' {
if ($h.ContainsKey('DisplayName')) { $h.NewDisplayName = $h.DisplayName; $h.Remove('DisplayName') }
Set-NetFirewallRule -Name $in.name @st @h
}
'remove' { Remove-NetFirewallRule -Name $in.name @st }
}
D | ConvertTo-Json -Compress -Depth 3
`
