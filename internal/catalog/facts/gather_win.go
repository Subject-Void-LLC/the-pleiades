// Package facts: what facts.gather reads from a Windows host, over WinRM
// (Phase 41c). Each probe is a fixed PowerShell script that prints one JSON
// document, escaped to ASCII on the host so the console's code page cannot
// change a name on its way here; the parsing is in Go, as every other
// platform's is. The min and hardware facts come from CIM, the packages from
// the registry's Uninstall keys (the machine's, both views), and the
// services from the service control manager through Win32_Service. Nothing
// needs elevation: every one of them is readable by a member of Remote
// Management Users.
package facts

import (
	"encoding/json"
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

// windowsMinScript prints the min subset's facts as Ansible names them on
// Windows: the host name the computer name (COMPUTERNAME), the system Win32NT, the kernel and the distribution version the
// OS version, the distribution the edition's caption, the architecture
// OSArchitecture's 64-bit. The node name is the DNS name, with the domain
// when the host is joined to one.
const windowsMinScript = `$ErrorActionPreference = 'Stop'
$os = Get-CimInstance Win32_OperatingSystem
$cs = Get-CimInstance Win32_ComputerSystem
$v = [Environment]::OSVersion.Version.ToString()
$node = [string]$cs.DNSHostName
if ($cs.PartOfDomain -and $cs.Domain) { $node = $node + '.' + $cs.Domain }
$out = [ordered]@{
  ansible_hostname = [string]$env:COMPUTERNAME
  ansible_nodename = $node
  ansible_system = 'Win32NT'
  ansible_kernel = $v
  ansible_architecture = [string]$os.OSArchitecture
  ansible_distribution = ([string]$os.Caption).Trim()
  ansible_distribution_version = $v
}` + windowsJSONOut

// windowsHardwareScript prints the hardware subset's facts: memory and the
// page files' allocated size in whole megabytes, the processor packages,
// logical processors and the first package's cores, and the seconds since
// boot by the host's own clock.
const windowsHardwareScript = `$ErrorActionPreference = 'Stop'
$os = Get-CimInstance Win32_OperatingSystem
$cs = Get-CimInstance Win32_ComputerSystem
$cpu = @(Get-CimInstance Win32_Processor)
$swap = 0
foreach ($p in @(Get-CimInstance Win32_PageFileUsage)) { $swap += [int64]$p.AllocatedBaseSize }
$out = [ordered]@{
  ansible_memtotal_mb = [int64][math]::Floor([double]$cs.TotalPhysicalMemory / 1MB)
  ansible_swaptotal_mb = [int64]$swap
  ansible_processor_count = [int64]$cs.NumberOfProcessors
  ansible_processor_vcpus = [int64]$cs.NumberOfLogicalProcessors
  ansible_processor_cores = [int64]$cpu[0].NumberOfCores
  ansible_uptime_seconds = [int64][math]::Floor(((Get-Date) - $os.LastBootUpTime).TotalSeconds)
}` + windowsJSONOut

// windowsPackagesScript prints every program the machine's Uninstall keys
// name: the native view, whose architecture is the host's own, and the
// 32-bit view under WOW6432Node. An entry with no DisplayName is not a
// program Windows lists and is left out, as Programs and Features leaves it
// out; per-user installs (HKCU) are another account's and are not read.
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
// delayed.
const windowsServicesScript = `$ErrorActionPreference = 'Stop'
$out = @(foreach ($s in @(Get-CimInstance Win32_Service)) {
  [ordered]@{ name = [string]$s.Name; state = [string]$s.State; mode = [string]$s.StartMode; delayed = [bool]$s.DelayedAutoStart }
})` + windowsJSONOut

// windowsProbes are the Windows probes, each a PowerShell script.
func windowsProbes() []gatherProbe {
	return []gatherProbe{
		{
			subset: gatherSubsetMin,
			keys: []string{gatherFactHostname, gatherFactNodename, gatherFactSystem, gatherFactKernel, gatherFactArchitecture,
				gatherFactDistribution, gatherFactDistributionVersion, gatherFactDistributionMajor},
			command: windowsMinScript,
			parse:   parseWindowsMin,
		},
		{
			subset: gatherSubsetHardware,
			keys: []string{gatherFactMemTotalMB, gatherFactSwapTotalMB, gatherFactProcessorCount, gatherFactProcessorVCPUs,
				gatherFactProcessorCores, gatherFactUptimeSeconds},
			command: windowsHardwareScript,
			parse:   parseWindowsObject,
		},
		{
			subset:  gatherSubsetPackages,
			family:  gatherFamilyPackage,
			command: windowsPackagesScript,
			parse:   func(out string) []gatherFact { return packageFacts(parseWindowsPackages(out)) },
		},
		{
			subset:  gatherSubsetServices,
			family:  gatherFamilyService,
			command: windowsServicesScript,
			parse:   parseWindowsServices,
		},
	}
}

// parseWindowsMin reads the min script's object, adding the distribution's
// major version (the OS version to its first dot) and, when the host named
// no computer name, the node name to its first dot as the host name.
func parseWindowsMin(out string) []gatherFact {
	facts := parseWindowsObject(out)
	var extra []gatherFact
	named := false
	for _, f := range facts {
		named = named || f.key == gatherFactHostname
	}
	for _, f := range facts {
		s, _ := f.value.(string)
		switch f.key {
		case gatherFactNodename:
			if !named {
				extra = append(extra, gatherFact{key: gatherFactHostname, value: gatherShortName(s)})
			}
		case gatherFactDistributionVersion:
			major, _, _ := strings.Cut(s, ".")
			extra = append(extra, gatherFact{key: gatherFactDistributionMajor, value: major})
		}
	}
	return append(facts, extra...)
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

// windowsItems reads a JSON array of objects, each into a T. PowerShell prints an empty array as [] and a missing one as nothing,
// which both read as no items.
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
// back.
func parseWindowsServices(out string) []gatherFact {
	var facts []gatherFact
	for _, s := range windowsItems[struct {
		Name    string `json:"name"`
		State   string `json:"state"`
		Mode    string `json:"mode"`
		Delayed bool   `json:"delayed"`
	}](out) {
		if s.Name == "" {
			continue
		}
		mode := strings.ToLower(strings.TrimSpace(s.Mode))
		if mode == "auto" && s.Delayed {
			mode = "delayed"
		}
		state := strings.ToLower(strings.TrimSpace(s.State))
		if state == "" {
			state = "unknown"
		}
		enabled := mode == "auto" || mode == "delayed" || mode == "boot" || mode == "system"
		facts = append(facts, gatherFact{key: itemKey(gatherFamilyService, s.Name), value: map[string]any{
			"state": state, "enabled": enabled, "status": mode, "source": sourceSCM,
		}})
	}
	return facts
}
