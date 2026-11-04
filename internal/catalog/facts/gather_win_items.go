// Package facts: the files, ports and users probes of a Windows host (Phase
// 41c), each a fixed PowerShell script printing one JSON document as the
// other Windows probes do (windowsJSONOut).
//
// The files script reads through .NET, not the PowerShell provider, but for
// the ACL (Get-Acl, which PowerShell 5.1 and 7 both have, where .NET's
// File.GetAccessControl is gone from 7):
// Get-Item -Force on C:\pagefile.sys, a file that exists, throws the same
// ItemNotFoundException as a path that does not, so the provider cannot tell
// absent from unreachable. [IO.File]::GetAttributes can: only a
// FileNotFoundException or DirectoryNotFoundException means absent, and any
// other failure is unreadable. The paths reach the script as base64 JSON, so
// no character in one is read by PowerShell.
package facts

import (
	"encoding/base64"
	"encoding/json"
	"net/netip"
	"strings"
)

// windowsPortsScript lists every listening TCP socket and every UDP
// endpoint (Windows keeps no peer for one, so every bound endpoint is
// listed), each with its owning process's name where Get-Process names it.
// A host without the TCP/IP cmdlets (before Windows 8 and Server 2012)
// lists none; the cmdlets' "no such object" when nothing listens is no
// error, and any other failure ends the script.
const windowsPortsScript = `$ErrorActionPreference = 'Stop'
$out = @()
if (Get-Command Get-NetTCPConnection -ErrorAction SilentlyContinue) {
  $names = @{}
  foreach ($p in @(Get-Process -ErrorAction SilentlyContinue)) { $names[[int]$p.Id] = [string]$p.ProcessName }
  $tcp = @(Get-NetTCPConnection -State Listen -ErrorAction SilentlyContinue -ErrorVariable te)
  $udp = @(Get-NetUDPEndpoint -ErrorAction SilentlyContinue -ErrorVariable ue)
  foreach ($e in @($te) + @($ue)) { if ($e.CategoryInfo.Category -ne 'ObjectNotFound') { throw $e } }
  $out = @(
    foreach ($c in $tcp) { [ordered]@{ protocol = 'tcp'; address = [string]$c.LocalAddress; port = [int]$c.LocalPort; process = $names[[int]$c.OwningProcess] } }
    foreach ($u in $udp) { [ordered]@{ protocol = 'udp'; address = [string]$u.LocalAddress; port = [int]$u.LocalPort; process = $names[[int]$u.OwningProcess] } }
  )
}` + windowsJSONOut

// windowsUsersScript lists the local accounts: name, SID and whether each
// is enabled. Get-LocalUser has no password or hash to give. A host without
// it (PowerShell before 5.1, or the 32-bit shell) lists none, as a POSIX
// device without the files does.
const windowsUsersScript = `$ErrorActionPreference = 'Stop'
$out = @()
if (Get-Command Get-LocalUser -ErrorAction SilentlyContinue) {
  $out = @(foreach ($u in @(Get-LocalUser)) { [ordered]@{ name = [string]$u.Name; sid = [string]$u.SID.Value; enabled = [bool]$u.Enabled } })
}` + windowsJSONOut

// windowsGroupsScript lists the local groups with their members. A group
// whose members Get-LocalGroupMember cannot list (it throws on an orphaned
// SID or some cloud members) reports none, not an empty list.
const windowsGroupsScript = `$ErrorActionPreference = 'Stop'
$out = @()
if (Get-Command Get-LocalGroup -ErrorAction SilentlyContinue) {
  $out = @(foreach ($g in @(Get-LocalGroup)) {
    $m = $null
    try { $m = @(foreach ($x in @(Get-LocalGroupMember -Group $g -ErrorAction Stop)) { [string]$x.Name }) } catch { }
    [ordered]@{ name = [string]$g.Name; sid = [string]$g.SID.Value; members = $m }
  })
}` + windowsJSONOut

// windowsFilesScript is the files script (a file is hashed only when its ACL
// lets Everyone, Authenticated Users or BUILTIN\Users read it and denies none
// of them, as posixFileProbe hashes only a world-readable file; any other is
// digest withheld, with no size; and a file another process holds open
// for writing, such as a log, is still read, sharing it as that process
// allows, rather than reported unreadable until the writer lets go) for the base64 JSON list of paths
// b64: per path, in order, its state and, when present, type, size,
// attributes, owner, digest and link target.
func windowsFilesScript(b64 string) string {
	return `$ErrorActionPreference = 'Stop'
$paths = ConvertFrom-Json ([Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('` + b64 + `')))
$out = @(foreach ($p in @($paths)) {
  $r = [ordered]@{ state = 'unreadable' }
  try { $attr = [IO.File]::GetAttributes($p) } catch {
    $e = $_.Exception
    while ($null -ne $e.InnerException) { $e = $e.InnerException }
    if ($e -is [IO.FileNotFoundException] -or $e -is [IO.DirectoryNotFoundException]) { $r.state = 'absent' }
    $r
    continue
  }
  $r.state = 'present'
  $r.attributes = [string]$attr
  $dir = ($attr -band [IO.FileAttributes]::Directory) -ne 0
  if (($attr -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
    $r.type = 'link'
    try { $r.link_target = [string](@((Get-Item -LiteralPath $p -Force -ErrorAction Stop).Target) -join ';') } catch { }
  } elseif ($dir) { $r.type = 'directory' } else {
    $r.type = 'file'
    $world = $false
    try {
      $allow = $false; $deny = $false
      foreach ($a in @((Get-Acl -LiteralPath $p).GetAccessRules($true, $true, [Security.Principal.SecurityIdentifier]))) {
        if (@('S-1-1-0', 'S-1-5-11', 'S-1-5-32-545') -notcontains $a.IdentityReference.Value) { continue }
        if (($a.FileSystemRights -band [Security.AccessControl.FileSystemRights]::ReadData) -eq 0) { continue }
        if ([string]$a.AccessControlType -eq 'Deny') { $deny = $true } else { $allow = $true }
      }
      $world = $allow -and -not $deny
    } catch { }
    $r.world_readable = $world
    if (-not $world) { $r.digest = 'withheld' } else {
      try { $r.size = [int64](New-Object IO.FileInfo($p)).Length } catch { }
      try {
        $s = [IO.File]::Open($p, 'Open', 'Read', 'ReadWrite, Delete')
        try { $h = [Security.Cryptography.SHA256]::Create().ComputeHash($s) } finally { $s.Dispose() }
        $r.sha256 = -join ($h | ForEach-Object { $_.ToString('x2') })
        $r.readable = $true
      } catch { $r.readable = $false }
    }
  }
  try { $r.owner = [string](Get-Acl -LiteralPath $p).Owner } catch { }
  $r
})` + windowsJSONOut
}

// windowsFileProbe is the files probe of a Windows host.
func windowsFileProbe() gatherProbe {
	return gatherProbe{
		subset: gatherSubsetFiles,
		family: gatherFamilyFile,
		commandFor: func(paths []string) string {
			raw, _ := json.Marshal(paths)
			return windowsFilesScript(base64.StdEncoding.EncodeToString(raw))
		},
		parse: parseWindowsFiles,
	}
}

// windowsItemProbes are the Windows host's files, ports and users probes.
func windowsItemProbes() []gatherProbe {
	return []gatherProbe{
		windowsFileProbe(),
		{subset: gatherSubsetPorts, family: gatherFamilyPort, command: windowsPortsScript, parse: parseWindowsPorts},
		{subset: gatherSubsetUsers, family: gatherFamilyUser, command: windowsUsersScript, parse: parseWindowsUsers},
		{subset: gatherSubsetUsers, family: gatherFamilyGroup, command: windowsGroupsScript, parse: parseWindowsGroups},
	}
}

// parseWindowsFiles reads the files script's array, one answer per path in
// order; Gather names each (filesFacts). A value it does not know is left
// out rather than guessed.
func parseWindowsFiles(out string) []gatherFact {
	items := windowsItems[struct {
		State      string `json:"state"`
		Type       string `json:"type"`
		Size       *int64 `json:"size"`
		Attributes string `json:"attributes"`
		Owner      string `json:"owner"`
		SHA256     string `json:"sha256"`
		Readable   *bool  `json:"readable"`
		World      *bool  `json:"world_readable"`
		Digest     string `json:"digest"`
		LinkTarget string `json:"link_target"`
	}](out)
	facts := make([]gatherFact, 0, len(items))
	for _, it := range items {
		value := map[string]any{"state": fileStateUnreadable}
		if it.State == fileStateAbsent {
			value["state"] = fileStateAbsent
		}
		if it.State == fileStatePresent && (it.Type == "file" || it.Type == "directory" || it.Type == "link") {
			value["state"], value["type"] = fileStatePresent, it.Type
			for k, v := range map[string]string{"attributes": it.Attributes, "owner": it.Owner, "link_target": it.LinkTarget} {
				if v != "" {
					value[k] = v
				}
			}
			if it.Size != nil && *it.Size >= 0 {
				value["size"] = *it.Size
			}
			if it.Readable != nil {
				value["readable"] = *it.Readable
			}
			if it.World != nil {
				value["world_readable"] = *it.World
			}
			if b, ok := windowsDigest(it.SHA256); ok && it.Digest != fileDigestWithheld {
				value["sha256"] = b
			}
			// A withheld file's size is a secret's length, and is not kept.
			if it.Digest == fileDigestWithheld {
				value["digest"] = fileDigestWithheld
				delete(value, "size")
			}
		}
		facts = append(facts, gatherFact{value: value})
	}
	return facts
}

// windowsDigest is s as a lower-case SHA-256 hex digest, when it is one.
func windowsDigest(s string) (string, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if len(s) != 64 || strings.Trim(s, "0123456789abcdef") != "" {
		return "", false
	}
	return s, true
}

// parseWindowsPorts reads the ports script's array. An address is written as
// netip writes it (a scope such as %4 kept); a port outside 1 to 65535 is no
// listener.
func parseWindowsPorts(out string) []gatherFact {
	var facts []gatherFact
	for _, p := range windowsItems[struct {
		Protocol string `json:"protocol"`
		Address  string `json:"address"`
		Port     int    `json:"port"`
		Process  string `json:"process"`
	}](out) {
		if (p.Protocol != "tcp" && p.Protocol != "udp") || p.Port <= 0 || p.Port > 65535 || p.Address == "" {
			continue
		}
		address := p.Address
		if a, err := netip.ParseAddr(address); err == nil {
			address = a.String()
		}
		value := map[string]any{"protocol": p.Protocol, "address": address, "port": p.Port}
		if name := strings.TrimSpace(p.Process); name != "" {
			value["process"] = name
		}
		facts = append(facts, gatherFact{key: portKey(p.Protocol, address, p.Port), value: value})
	}
	return facts
}

// sourceSAM is where a Windows account fact was read: the local security
// account database.
const sourceSAM = "sam"

// parseWindowsUsers reads the users script's array, one fact per account.
func parseWindowsUsers(out string) []gatherFact {
	var facts []gatherFact
	for _, u := range windowsItems[struct {
		Name    string `json:"name"`
		SID     string `json:"sid"`
		Enabled bool   `json:"enabled"`
	}](out) {
		if u.Name == "" {
			continue
		}
		facts = append(facts, gatherFact{key: itemKey(gatherFamilyUser, u.Name), value: map[string]any{
			"sid": u.SID, "enabled": u.Enabled, "source": sourceSAM,
		}})
	}
	return facts
}

// parseWindowsGroups reads the groups script's array, one fact per group. A
// group whose members could not be listed has no members key, which is not
// the same as an empty list.
func parseWindowsGroups(out string) []gatherFact {
	var facts []gatherFact
	for _, g := range windowsItems[struct {
		Name    string    `json:"name"`
		SID     string    `json:"sid"`
		Members *[]string `json:"members"`
	}](out) {
		if g.Name == "" {
			continue
		}
		value := map[string]any{"sid": g.SID, "source": sourceSAM}
		if g.Members != nil {
			value["members"] = append([]string{}, *g.Members...)
		}
		facts = append(facts, gatherFact{key: itemKey(gatherFamilyGroup, g.Name), value: value})
	}
	return facts
}
