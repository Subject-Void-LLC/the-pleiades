// facts.gather's Windows files subset on a real Windows host, through the
// real binary over WinRM (owner decisions 2026-11-19): a world-readable file
// keeps a plain digest, and each restriction the decisions name makes one
// private (a deny of read to anyone, a mandatory integrity label, a junction
// above it, a conditional entry where the host takes one); a named stream
// is listed with one keyed digest, which a change to the stream alone moves;
// and the whole script, past cmd.exe's command line, reaches the host on
// standard input. The file name names no platform (FAILURE PATTERNS 374); it
// skips unless PLEIADES_WINRM_PROJECT and PLEIADES_WINRM_DEVICE name a host.
package main_test

import (
	"os"
	"strings"
	"testing"
)

// labWinFileKey is a Windows path's file family key: Windows paths hold no
// slash or percent sign here, and spaces are encoded.
func labWinFileKey(family, path string) string {
	return family + strings.ReplaceAll(path, " ", "%20")
}

func TestCLI_FactsGatherOnWindowsHoldsACLsLabelsJunctionsAndStreams(t *testing.T) {
	m := labMachine{os.Getenv(envWinRMProject), os.Getenv(envWinRMDevice)}
	if m.dir == "" || m.host == "" {
		t.Skipf("needs a Windows host managed as windows_server: set %s and %s", envWinRMProject, envWinRMDevice)
	}
	if strings.HasPrefix(m.dir, "~/") {
		home, _ := os.UserHomeDir()
		m.dir = home + m.dir[1:]
	}
	const dir = `C:\pleiades-gate-files`
	// A folder under C:\ inherits BUILTIN\Users read from it on a stock
	// install, so its files are world-readable until something restricts one.
	setup := `$d = '` + dir + `'; Remove-Item -LiteralPath $d -Recurse -Force -ErrorAction SilentlyContinue; New-Item -ItemType Directory -Path $d | Out-Null; ` +
		`foreach ($n in 'pub', 'deny', 'low', 'stream') { Set-Content -LiteralPath (Join-Path $d ($n + '.txt')) -Value 'pin1234' }; ` +
		`New-Item -ItemType Directory -Path (Join-Path $d 'real') | Out-Null; Set-Content -LiteralPath (Join-Path $d 'real\x.txt') -Value 'pin1234'; ` +
		`New-Item -ItemType Junction -Path (Join-Path $d 'j') -Target (Join-Path $d 'real') | Out-Null; ` +
		`icacls (Join-Path $d 'deny.txt') /deny '*S-1-5-32-546:(R)' | Out-Null; ` +
		`icacls (Join-Path $d 'low.txt') /setintegritylevel L | Out-Null; ` +
		`Set-Content -LiteralPath (Join-Path $d 'stream.txt') -Stream hidden -Value 'one'; 'ready'`
	t.Cleanup(func() {
		labPowerShell(t, m, `Remove-Item -LiteralPath '`+dir+`' -Recurse -Force -ErrorAction SilentlyContinue; 'removed'`)
	})
	if got := labPowerShell(t, m, setup); got != "ready" {
		t.Fatalf("setting up %s printed %q", dir, got)
	}
	pub, deny, low, stream, junction := dir+`\pub.txt`, dir+`\deny.txt`, dir+`\low.txt`, dir+`\stream.txt`, dir+`\j\x.txt`
	stats := labGather(t, m, pub, deny, low, stream, junction)

	// The baseline: without a restriction, the host's own digest, plain.
	digest := strings.ToLower(labPowerShell(t, m, `(Get-FileHash -LiteralPath '`+pub+`' -Algorithm SHA256).Hash`))
	if f := labItem(t, stats, labWinFileKey("file.", pub)); f["sha256"] != digest || f["world_readable"] != true || f["acl"] != "listed" {
		t.Fatalf("%s = %v, want the host's digest %s, plain: without it no private answer below means anything", pub, f, digest)
	}
	for path, why := range map[string]string{deny: "a deny of read to Guests", low: "a Low mandatory label", junction: "a junction above it"} {
		labHoldsKeyedDigest(t, stats, labWinFileKey("file.", path))
		if t.Failed() {
			t.Logf("%s: %s should have made it private", path, why)
		}
	}

	// The stream: listed by name, its digest keyed, and moved by a change to
	// the stream alone, while the file's own digest stays.
	key := labWinFileKey("file_attached.", stream)
	a := labItem(t, stats, key)
	names, _ := a["names"].([]any)
	if a["kind"] != "streams" || a["state"] != "listed" || len(names) != 1 || names[0] != "hidden" || a["hmac_sha256"] == nil || a["sha256_raw"] != nil {
		t.Fatalf("%s = %v, want the one stream hidden and a keyed digest", key, a)
	}
	before := labItem(t, stats, labWinFileKey("file.", stream))["sha256"]
	labPowerShell(t, m, `Set-Content -LiteralPath '`+stream+`' -Stream hidden -Value 'two'; 'written'`)
	again := labGather(t, m, stream)
	if b := labItem(t, again, key); b["hmac_sha256"] == a["hmac_sha256"] {
		t.Errorf("changing the stream left %s's digest %v", key, b["hmac_sha256"])
	}
	if f := labItem(t, again, labWinFileKey("file.", stream)); f["sha256"] != before {
		t.Errorf("changing the stream moved the file's own digest: %v, was %v", f["sha256"], before)
	}

	// A conditional entry, where the host lets this login set one.
	cond := dir + `\cond.txt`
	set := `Set-Content -LiteralPath '` + cond + `' -Value 'pin1234'; $a = Get-Acl -LiteralPath '` + cond + `'; ` +
		`try { $a.SetSecurityDescriptorSddlForm($a.Sddl + '(XA;;FR;;;WD;(Member_of {SID(BA)}))'); Set-Acl -LiteralPath '` + cond + `' -AclObject $a; 'set' } catch { 'refused' }`
	if labPowerShell(t, m, set) != "set" {
		t.Log("this host refused a conditional entry; that case is unproven here")
		return
	}
	labHoldsKeyedDigest(t, labGather(t, m, cond), labWinFileKey("file.", cond))
}
