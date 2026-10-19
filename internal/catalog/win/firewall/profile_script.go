// The PowerShell win.firewall.profile runs, a constant reading every value
// it acts on as JSON from standard input.
package firewall

// profileScript reads {op, name, set} from standard input, sets the
// profile in the machine's own store when op is set, and answers one line
// of JSON: each managed setting as the machine keeps it (PersistentStore)
// and as it is in force (ActiveStore, where Group Policy's settings win).
// set's keys are the method's parameter names, mapped here onto
// Set-NetFirewallProfile's own. Its function has a verb-noun name: a short
// one can be a built-in alias, which PowerShell runs in its place (r is
// Invoke-History, FAILURE_PATTERNS 530).
const profileScript = `$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = [Text.Encoding]::UTF8
$in = [Console]::In.ReadToEnd() | ConvertFrom-Json
$m = @{ enabled = 'Enabled'; default_inbound_action = 'DefaultInboundAction'; default_outbound_action = 'DefaultOutboundAction'
log_allowed = 'LogAllowed'; log_blocked = 'LogBlocked'; log_file = 'LogFileName'; log_max_size_kb = 'LogMaxSizeKilobytes' }
function Read-Profile($store) {
$p = Get-NetFirewallProfile -Name $in.name -PolicyStore $store
$o = @{}
foreach ($k in $m.Keys) { $o[$k] = [string]$p.($m[$k]) }
$o
}
if ($in.op -eq 'set') {
$h = @{}
foreach ($k in $in.set.PSObject.Properties) { $h[$m[$k.Name]] = $k.Value }
if ($h.ContainsKey('LogMaxSizeKilobytes')) { $h.LogMaxSizeKilobytes = [uint64]$h.LogMaxSizeKilobytes }
Set-NetFirewallProfile -Name $in.name -PolicyStore PersistentStore @h
}
@{ persistent = (Read-Profile 'PersistentStore'); active = (Read-Profile 'ActiveStore') } | ConvertTo-Json -Compress
`
