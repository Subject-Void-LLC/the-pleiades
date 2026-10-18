// The PowerShell win.schtask runs. It is a constant: every value it acts
// on arrives as JSON on standard input and is read with ConvertFrom-Json,
// so no value is ever PowerShell syntax, whatever it holds.
package win

// schtaskScript reads {op, path, name, ...} from standard input and
// answers one line of JSON describing the task as it then is.
const schtaskScript = `$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
[Console]::OutputEncoding = [Text.Encoding]::UTF8
$in = [Console]::In.ReadToEnd() | ConvertFrom-Json
function Describe {
  # Only a task that is not there is absent: access denied or a Task
  # Scheduler failure is an error, never "the task does not exist".
  try { $t = Get-ScheduledTask -TaskPath $in.path -TaskName $in.name -ErrorAction Stop }
  catch { if ($_.CategoryInfo.Category -eq 'ObjectNotFound') { return @{ exists = $false } } else { throw } }
  $info = Get-ScheduledTaskInfo -TaskPath $in.path -TaskName $in.name -ErrorAction SilentlyContinue
  $a = @($t.Actions)[0]
  $g = @($t.Triggers)[0]
  $trigger = @{}
  if ($null -ne $g) {
    $kind = $g.CimClass.CimClassName
    $start = [string]$g.StartBoundary
    # A boundary written without an offset is the device's local time, so it
    # is read here, where that offset is known, and answered in UTC.
    $utc = if ($start -ne '') { ([datetimeoffset]::Parse($start, [Globalization.CultureInfo]::InvariantCulture)).ToUniversalTime().ToString('o') } else { '' }
    $trigger = @{ kind = $kind; start = $start; start_utc = $utc; days = [string]$g.DaysInterval }
  }
  @{
    exists = $true
    xml = (Export-ScheduledTask -TaskPath $in.path -TaskName $in.name)
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
      'at_logon'   { New-ScheduledTaskTrigger -AtLogOn }
      'daily'      { New-ScheduledTaskTrigger -Daily -At ([datetime]::ParseExact($in.at, 'HH:mm', [Globalization.CultureInfo]::InvariantCulture)) }
      'once'       { New-ScheduledTaskTrigger -Once -At ([datetimeoffset]::Parse($in.at, [Globalization.CultureInfo]::InvariantCulture).LocalDateTime) }
    }
    $principal = if ($in.logon -eq 'service') { New-ScheduledTaskPrincipal -UserId $in.user -LogonType ServiceAccount -RunLevel $in.run_level } else { New-ScheduledTaskPrincipal -UserId $in.user -LogonType S4U -RunLevel $in.run_level }
    $settings = New-ScheduledTaskSettingsSet
    $settings.Enabled = [bool]$in.enabled
    Register-ScheduledTask -TaskPath $in.path -TaskName $in.name -Action $action -Trigger $trigger -Principal $principal -Settings $settings -Description $in.description -Force | Out-Null
  }
  'restore' { Register-ScheduledTask -TaskPath $in.path -TaskName $in.name -Xml $in.xml -Force | Out-Null }
  'remove'  { Unregister-ScheduledTask -TaskPath $in.path -TaskName $in.name -Confirm:$false }
}
Describe | ConvertTo-Json -Compress -Depth 4
`
