// The PowerShell win.schtask runs. It is a constant: every value it acts
// on arrives as JSON on standard input and is read with ConvertFrom-Json,
// so no value is ever PowerShell syntax, whatever it holds.
package win

// schtaskScript reads {op, path, name, ...} from standard input and
// answers one line of JSON describing the task as it then is.
//
// It travels as powershell.exe -EncodedCommand, UTF-16LE in base64, inside
// a command line the WinRM service's cmd.exe caps at 8191 characters, so
// each character of it costs about 2.7 there: its comments live here, and
// $q holds the task's path and name for every cmdlet to splat
// (TestSchtaskScript_FitsTheCommandLine holds it to the cap). winrmexec's
// own prologue already silences progress records.
//
// Describe treats only a task that is not there as absent: access denied
// or a Task Scheduler failure is an error, never "the task does not
// exist". A trigger's start boundary written without an offset is the
// device's local time, so it is read on the device, where that offset is
// known, and answered in UTC as well. Register-ScheduledTask refuses an
// empty -Description, so one is passed only when a task has one.
const schtaskScript = `$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = [Text.Encoding]::UTF8
$in = [Console]::In.ReadToEnd() | ConvertFrom-Json
$q = @{ TaskPath = $in.path; TaskName = $in.name }
$inv = [Globalization.CultureInfo]::InvariantCulture
function Describe {
try { $t = Get-ScheduledTask @q -ErrorAction Stop }
catch { if ($_.CategoryInfo.Category -eq 'ObjectNotFound') { return @{ exists = $false } } else { throw } }
$info = Get-ScheduledTaskInfo @q -ErrorAction SilentlyContinue
$a = @($t.Actions)[0]
$g = @($t.Triggers)[0]
$trigger = @{}
if ($null -ne $g) {
$start = [string]$g.StartBoundary
$utc = if ($start -ne '') { ([datetimeoffset]::Parse($start, $inv)).ToUniversalTime().ToString('o') } else { '' }
$trigger = @{ kind = $g.CimClass.CimClassName; start = $start; start_utc = $utc; days = [string]$g.DaysInterval }
}
@{
exists = $true
xml = (Export-ScheduledTask @q)
enabled = [bool]$t.Settings.Enabled
state = [string]$t.State
description = [string]$t.Description
execute = [string]$a.Execute
arguments = [string]$a.Arguments
working_directory = [string]$a.WorkingDirectory
actions = @($t.Actions).Count
triggers = @($t.Triggers).Count
trigger = $trigger
user = [string]$t.Principal.UserId
logon_type = [string]$t.Principal.LogonType
run_level = [string]$t.Principal.RunLevel
last_run_time = [string]$info.LastRunTime
last_task_result = [string]$info.LastTaskResult
next_run_time = [string]$info.NextRunTime
}
}
switch ($in.op) {
'register' {
$action = if ($in.arguments -ne '') { New-ScheduledTaskAction -Execute $in.execute -Argument $in.arguments } else { New-ScheduledTaskAction -Execute $in.execute }
if ($in.working_directory -ne '') { $action.WorkingDirectory = $in.working_directory }
$trigger = switch ($in.trigger) {
'at_startup' { New-ScheduledTaskTrigger -AtStartup }
'at_logon' { New-ScheduledTaskTrigger -AtLogOn }
'daily' { New-ScheduledTaskTrigger -Daily -At ([datetime]::ParseExact($in.at, 'HH:mm', $inv)) }
'once' { New-ScheduledTaskTrigger -Once -At ([datetimeoffset]::Parse($in.at, $inv).LocalDateTime) }
}
$logon = if ($in.logon -eq 'service') { 'ServiceAccount' } else { 'S4U' }
$principal = New-ScheduledTaskPrincipal -UserId $in.user -LogonType $logon -RunLevel $in.run_level
$settings = New-ScheduledTaskSettingsSet
$settings.Enabled = [bool]$in.enabled
$more = @{}
if ($in.description -ne '') { $more.Description = $in.description }
Register-ScheduledTask @q @more -Action $action -Trigger $trigger -Principal $principal -Settings $settings -Force | Out-Null
}
'restore' { Register-ScheduledTask @q -Xml $in.xml -Force | Out-Null }
'remove' { Unregister-ScheduledTask @q -Confirm:$false }
}
Describe | ConvertTo-Json -Compress -Depth 4
`
