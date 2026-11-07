// Package facts: what facts.gather reads from a Windows host, over WinRM
// (Phase 41c). Each probe is a fixed PowerShell script that prints one JSON
// document, escaped to ASCII on the host so the console's code page cannot
// change a name on its way here; the parsing is in Go, as every other
// platform's is. The min and hardware facts come from CIM, the packages from
// the registry's Uninstall keys (the machine's, both views), and the
// services from the service control manager through Win32_Service, with each
// service's registry Type telling a per-user service's template and instances
// apart. Every
// script only reads. The lab gate runs them with an administrator's login;
// whether a member of Remote Management Users alone reads all of it is not
// yet proven.
package facts

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// platformWindows names the Windows probes. uname answers nothing there, so
// it is chosen by the transport (WinRM) rather than asked.
const platformWindows = "Windows"

// sourceRegistry and sourceSCM are where a Windows package or service fact
// was read.
const (
	sourceRegistry = "registry"
	sourceSCM      = "scm"
)

// windowsJSONOut is the end of every Windows probe: print $out as compact
// JSON with every character outside printable ASCII escaped as JSON escapes
// it. The escape is spelled with [char]92 so no backslash and u ever appear
// together in this file (FAILURE_PATTERNS 834).
const windowsJSONOut = `
$json = ConvertTo-Json -InputObject $out -Compress -Depth 4
[regex]::Replace($json, '[^\x20-\x7e]', { param($m) ([string][char]92) + 'u' + ([int][char]$m.Value).ToString('x4') })
`

// windowsMinScript prints the min subset's facts under their native keys:
// the host name the computer name (COMPUTERNAME), the kernel's name, the
// family and the distribution windows, the kernel release and the version
// the OS version, the edition the caption. The node name is the DNS name,
// with the domain when the host is joined to one. The processor
// architecture is printed as Windows names it (AMD64, ARM64, x86) under
// windowsArchitectureKey, which is not a fact: parseWindowsMin turns it into
// hardware.architecture's machine name. PROCESSOR_ARCHITEW6432 is read first
// because a 32-bit or emulated PowerShell sees its own architecture in
// PROCESSOR_ARCHITECTURE, not the machine's.
const windowsMinScript = `$ErrorActionPreference = 'Stop'
$os = Get-CimInstance Win32_OperatingSystem
$cs = Get-CimInstance Win32_ComputerSystem
$v = [Environment]::OSVersion.Version.ToString()
$node = [string]$cs.DNSHostName
if ($cs.PartOfDomain -and $cs.Domain) { $node = $node + '.' + $cs.Domain }
$arch = [string]$env:PROCESSOR_ARCHITEW6432
if (-not $arch) { $arch = [string]$env:PROCESSOR_ARCHITECTURE }
$out = [ordered]@{
  'host.name' = [string]$env:COMPUTERNAME
  'host.nodename' = $node
  'os.kernel.name' = 'windows'
  'os.kernel.release' = $v
  'os.distribution' = 'windows'
  'os.family' = 'windows'
  'os.version' = $v
  'os.edition' = ([string]$os.Caption).Trim()
  'processor_architecture' = $arch
}` + windowsJSONOut

// windowsArchitectureKey is the min script's processor architecture as
// Windows names it, which parseWindowsMin reads and never emits.
const windowsArchitectureKey = "processor_architecture"

// windowsMachines is the machine name (as uname -m spells it on Linux) of
// each processor architecture Windows names. One it names otherwise leaves
// hardware.architecture out rather than guessing.
var windowsMachines = map[string]string{
	"AMD64": "x86_64",
	"ARM64": "aarch64",
	"X86":   "i686",
}

// windowsHardwareScript prints the hardware subset's facts under their
// native keys: memory in whole megabytes, truncated as every platform's is,
// the processor packages, logical processors and the first package's cores,
// and the seconds since boot by the host's own clock. There is no swap: a
// page file is not swap, and its allocated size grows with a system-managed
// page file. A value CIM does not give is null, which the parser leaves out,
// never 0.
const windowsHardwareScript = `$ErrorActionPreference = 'Stop'
function N($v) { if ($null -eq $v) { $null } else { [int64]$v } }
$os = Get-CimInstance Win32_OperatingSystem
$cs = Get-CimInstance Win32_ComputerSystem
$cpu = @(Get-CimInstance Win32_Processor)
$mem = $null
if ($null -ne $cs.TotalPhysicalMemory) { $mem = [math]::Floor([double]$cs.TotalPhysicalMemory / 1MB) }
$cores = $null
if ($cpu.Count -gt 0) { $cores = $cpu[0].NumberOfCores }
$up = $null
if ($null -ne $os.LastBootUpTime) { $up = [math]::Floor(((Get-Date) - $os.LastBootUpTime).TotalSeconds) }
$out = [ordered]@{
  'hardware.memory_mb' = N $mem
  'hardware.cpu.sockets' = N $cs.NumberOfProcessors
  'hardware.cpu.threads' = N $cs.NumberOfLogicalProcessors
  'hardware.cpu.cores' = N $cores
  'host.uptime_seconds' = N $up
}` + windowsJSONOut

// windowsPackagesScript prints every program the machine's Uninstall keys
// name: the native view, whose architecture is the host's own, and the
// 32-bit view under WOW6432Node. An entry with no DisplayName is left out;
// one Programs and Features hides (a SystemComponent, an update) is kept,
// since it is installed all the same. Per-user installs (HKCU) are another
// account's and are not read.
const windowsPackagesScript = `$ErrorActionPreference = 'Stop'
$native = ([string]$env:PROCESSOR_ARCHITECTURE).ToLowerInvariant()
$roots = @(
  @('HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall', $native),
  @('HKLM:\SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall', 'x86')
)
$out = @(foreach ($r in $roots) {
  if (-not (Test-Path -LiteralPath $r[0])) { continue }
  foreach ($k in @(Get-ChildItem -LiteralPath $r[0] -ErrorAction SilentlyContinue)) {
    $p = Get-ItemProperty -LiteralPath $k.PSPath -ErrorAction SilentlyContinue
    if ($null -eq $p -or -not $p.DisplayName) { continue }
    [ordered]@{ name = [string]$p.DisplayName; version = [string]$p.DisplayVersion; arch = $r[1] }
  }
})` + windowsJSONOut

// windowsServicesScript prints every service the service control manager
// knows: its name, state, start mode and whether an automatic start is
// delayed. A per-user service's instance (Template_<LUID>, made for each
// signed-in user, its registry Type holding SERVICE_USERSERVICE_INSTANCE,
// 0x80) is left out, since its name changes with every sign-in; Win32_Service
// never lists the template (Type SERVICE_USER_SERVICE, 0x40, without 0x80),
// so each template is read from the registry instead, with its start mode and
// no state, and marked per_user. Win32_Service gives an instance's type as
// Unknown, so the registry's Type is what tells one apart, and an instance
// whose key this login cannot read is still told apart by its name: its
// template's and a hexadecimal LUID. A Type, Start or DelayedAutostart that is
// not a DWORD is not taken for one (a template's start mode is then unknown),
// so one odd key never fails the task. The Services key always holds hundreds
// of services, so reading none of their types is a failure, not an answer.
const windowsServicesScript = `$ErrorActionPreference = 'Stop'
$types = @{}
$templates = @()
foreach ($k in @(Get-ChildItem -LiteralPath 'HKLM:\SYSTEM\CurrentControlSet\Services' -ErrorAction SilentlyContinue)) {
  $t = $k.GetValue('Type')
  if ($t -isnot [int]) { continue }
  $types[[string]$k.PSChildName] = $t
  if (($t -band 0x40) -and -not ($t -band 0x80)) { $templates += $k }
}
if ($types.Count -eq 0) { throw 'no service type could be read from the registry' }
$modes = @{ 0 = 'Boot'; 1 = 'System'; 2 = 'Auto'; 3 = 'Manual'; 4 = 'Disabled' }
$named = @{}
foreach ($k in $templates) { $named[[string]$k.PSChildName] = $true }
$seen = @{}
$out = @(foreach ($s in @(Get-CimInstance Win32_Service)) {
  $n = [string]$s.Name
  $t = $types[$n]
  if ($null -ne $t -and ($t -band 0x80)) { continue }
  if ($null -eq $t -and $n -match '^(.+)_[0-9a-fA-F]+$' -and $named.ContainsKey($Matches[1])) { continue }
  $seen[$n] = $true
  [ordered]@{ name = $n; state = [string]$s.State; mode = [string]$s.StartMode; delayed = [bool]$s.DelayedAutoStart }
}
foreach ($k in $templates) {
  if ($seen.ContainsKey([string]$k.PSChildName)) { continue }
  $start = $k.GetValue('Start')
  $mode = 'Unknown'
  if ($start -is [int] -and $modes.ContainsKey($start)) { $mode = $modes[$start] }
  $delayed = $k.GetValue('DelayedAutostart')
  [ordered]@{ name = [string]$k.PSChildName; mode = $mode; delayed = ($delayed -is [int] -and $delayed -eq 1); per_user = $true }
})` + windowsJSONOut

// windowsProbes are the Windows probes, each a PowerShell script.
func windowsProbes() []gatherProbe {
	return append([]gatherProbe{
		{
			subset: gatherSubsetMin,
			keys: []string{gatherFactHostName, gatherFactHostNodename, gatherFactKernelName, gatherFactKernelRelease, gatherFactArchitecture,
				gatherFactDistribution, gatherFactFamily, gatherFactVersion, gatherFactVersionMajor, gatherFactEdition},
			command: windowsMinScript,
			parse:   parseWindowsMin,
		},
		{
			subset: gatherSubsetHardware,
			keys: []string{gatherFactMemoryMB, gatherFactCPUSockets, gatherFactCPUThreads, gatherFactCPUCores,
				gatherFactHostUptime},
			command: windowsHardwareScript,
			parse:   parseWindowsObject,
		},
		{
			subset:  gatherSubsetPackages,
			family:  gatherFamilyPackage,
			command: windowsPackagesScript,
			parse:   func(out string) []gatherFact { return packageFacts(parseWindowsPackages(out)) },
			check:   checkWindowsArray,
		},
		{
			subset:  gatherSubsetServices,
			family:  gatherFamilyService,
			command: windowsServicesScript,
			parse:   parseWindowsServices,
			check:   checkWindowsArray,
		},
	}, windowsItemProbes()...)
}

// parseWindowsMin reads the min script's object, adding the version's
// whole-number major part, hardware.architecture from the architecture
// Windows names (which is itself not kept), and, when the host named no
// computer name, the node name to its first dot as the host name.
func parseWindowsMin(out string) []gatherFact {
	facts := parseWindowsObject(out)
	var kept, extra []gatherFact
	named := false
	for _, f := range facts {
		named = named || f.key == gatherFactHostName
	}
	for _, f := range facts {
		s, _ := f.value.(string)
		switch f.key {
		case windowsArchitectureKey:
			if machine, ok := windowsMachines[strings.ToUpper(s)]; ok {
				extra = append(extra, gatherFact{key: gatherFactArchitecture, value: machine})
			}
			continue
		case gatherFactHostNodename:
			if !named {
				extra = append(extra, gatherFact{key: gatherFactHostName, value: gatherShortName(s)})
			}
		case gatherFactVersion:
			if major, ok := versionMajor(s); ok {
				extra = append(extra, gatherFact{key: gatherFactVersionMajor, value: major})
			}
		}
		kept = append(kept, f)
	}
	return append(kept, extra...)
}

// parseWindowsObject reads one JSON object of fact names to values, in the
// order the host printed them. A whole number is an int; an empty string or
// a null is left out, since a fact the host could not answer for is absent
// rather than empty.
func parseWindowsObject(out string) []gatherFact {
	dec := json.NewDecoder(strings.NewReader(strings.TrimSpace(out)))
	dec.UseNumber()
	tok, err := dec.Token()
	if err != nil || tok != json.Delim('{') {
		return nil
	}
	var facts []gatherFact
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil
		}
		key, _ := keyTok.(string)
		var v any
		if err := dec.Decode(&v); err != nil {
			return nil
		}
		if value, ok := windowsValue(v); ok && key != "" {
			facts = append(facts, gatherFact{key: key, value: value})
		}
	}
	// The object must close, with nothing after it: one cut short after a
	// complete member read as the whole answer, and text printed after it
	// (a profile's Write-Host) as a well-formed one (FAILURE_PATTERNS 925).
	if tok, err := dec.Token(); err != nil || tok != json.Delim('}') {
		return nil
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil
	}
	return facts
}

// windowsValue is v, as a decoder using numbers read it, as a fact's
// value: a string, a whole number as an int, or a boolean. Anything else,
// an empty string or a null is not one.
func windowsValue(v any) (any, bool) {
	switch t := v.(type) {
	case string:
		t = strings.TrimSpace(t)
		return t, t != ""
	case json.Number:
		n, err := t.Int64()
		if err != nil || n < 0 {
			return nil, false
		}
		return int(n), true
	case bool:
		return t, true
	}
	return nil, false
}

// windowsItems reads a JSON array of objects, each into a T. Every family
// script's answer but the files script's has passed checkWindowsArray first,
// so it reads no items only where the host listed none; the files script's
// is matched to its paths by filesFacts, which reads an answer that does not
// decode as every path unreadable.
func windowsItems[T any](out string) []T {
	var items []T
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &items); err != nil {
		return nil
	}
	return items
}

// parseWindowsPackages reads the packages script's array.
func parseWindowsPackages(out string) []installedPackage {
	var pkgs []installedPackage
	for _, p := range windowsItems[struct {
		Name    string `json:"name"`
		Version string `json:"version"`
		Arch    string `json:"arch"`
	}](out) {
		name := strings.TrimSpace(p.Name)
		if name == "" {
			continue
		}
		pkgs = append(pkgs, installedPackage{name: name, version: strings.TrimSpace(p.Version), arch: p.Arch, source: sourceRegistry})
	}
	return pkgs
}

// parseWindowsServices reads the services script's array: state as the
// manager words it, lower-cased (running, stopped, start pending), enabled
// when the service starts with Windows (auto, delayed, boot, system), and
// status the start mode, delayed for an automatic start the manager holds
// back. A per-user service's template has no state, and says per_user.
func parseWindowsServices(out string) []gatherFact {
	var facts []gatherFact
	for _, s := range windowsItems[struct {
		Name    string `json:"name"`
		State   string `json:"state"`
		Mode    string `json:"mode"`
		Delayed bool   `json:"delayed"`
		PerUser bool   `json:"per_user"`
	}](out) {
		if s.Name == "" {
			continue
		}
		mode := strings.ToLower(strings.TrimSpace(s.Mode))
		if mode == "auto" && s.Delayed {
			mode = "delayed"
		}
		enabled := mode == "auto" || mode == "delayed" || mode == "boot" || mode == "system"
		value := map[string]any{"enabled": enabled, "status": mode, "source": sourceSCM}
		if s.PerUser {
			// A template never runs: each signed-in user's instance does.
			value["per_user"] = true
		} else {
			state := strings.ToLower(strings.TrimSpace(s.State))
			if state == "" {
				state = "unknown"
			}
			value["state"] = state
		}
		facts = append(facts, gatherFact{key: itemKey(gatherFamilyService, s.Name), value: value})
	}
	return facts
}

// checkWindowsArray refuses a family script's answer unless it is exactly
// one JSON array with nothing before or after it. Every family script prints
// one (ConvertTo-Json of an @() array, [] when the host has no items), so
// anything else, such as a cmd.exe AutoRun's output before it or an answer
// cut short, failed rather than meaning "no items" (FAILURE_PATTERNS 925).
func checkWindowsArray(out string) error {
	trimmed := strings.TrimSpace(out)
	if !strings.HasPrefix(trimmed, "[") || !json.Valid([]byte(trimmed)) {
		return fmt.Errorf("it did not print one JSON array and nothing else")
	}
	return nil
}
