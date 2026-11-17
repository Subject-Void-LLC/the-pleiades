// Gates for SSH refusals through the real binary against scripted IOS devices
// over real SSH: an old switch that offers only SHA-1 key exchange and an
// ssh-rsa host key (an IOSvL2 15.2, measured in the 2026-11-17 CML window) is
// trusted and reached only once its record allows each, warned about on every
// run, and refused again when a flag is taken away; and a device whose host
// key changed (a Catalyst 8000V on 17.15 makes a new port 22 key at every
// boot) is refused naming both keys and the command that accepts the new one,
// and a wait for it stops at that refusal at once.
package main_test

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
)

// scriptedIOS starts a scripted IOS device answering show version, offering
// only legacy algorithms when legacy is set.
func scriptedIOS(t *testing.T, prompt string, legacy bool) *remoteexectest.Server {
	t.Helper()
	srv, err := remoteexectest.Start(remoteexectest.Options{Legacy: legacy, Device: &remoteexectest.Device{
		Prompt:  prompt,
		Replies: map[string]string{"show version": "Cisco IOS Software, vios_l2 Software (vios_l2-ADVENTERPRISEK9-M), Version 15.2(20200924:215240)"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	return srv
}

// emptyHome is a home whose known_hosts exists and trusts nothing, or the
// given lines.
func emptyHome(t *testing.T, lines string) string {
	t.Helper()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".ssh", "known_hosts"), []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	return home
}

// iosProject is a project holding one IOS device of type at srv, with srv's
// credential stored, and setting the extra properties given.
func iosProject(t *testing.T, srv *remoteexectest.Server, name, deviceType string, extra ...string) string {
	t.Helper()
	dir := t.TempDir()
	add := []string{"add-host", name, "--type", deviceType, "--set", "host=" + srv.Host, "--set", "port=" + strconv.Itoa(srv.Port)}
	for _, kv := range extra {
		add = append(add, "--set", kv)
	}
	for _, args := range [][]string{{"init"}, add} {
		if out, err := runPleiades(t, dir, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	if out, err := runPleiadesWithStdin(t, dir, srv.Password+"\n", "add-credential", name, "--username", srv.Username, "--password-stdin"); err != nil {
		t.Fatalf("add-credential: %v\n%s", err, out)
	}
	return dir
}

// TestCLI_ALegacySSHDeviceIsReachedOnlyThroughItsFlags is the legacy gate.
func TestCLI_ALegacySSHDeviceIsReachedOnlyThroughItsFlags(t *testing.T) {
	srv := scriptedIOS(t, "sw-l2#", true)
	dir := iosProject(t, srv, "sw-l2", "cisco_switch", "ephemeral=true")
	home := emptyHome(t, "")
	set := func(args ...string) {
		t.Helper()
		if out, err := runPleiades(t, dir, append([]string{"set-host", "sw-l2"}, args...)...); err != nil {
			t.Fatalf("set-host %v: %v\n%s", args, err, out)
		}
	}

	// Nothing allowed: trust-host is refused naming the key exchange, then,
	// with that allowed, naming the host key.
	for _, step := range []struct {
		allow, want string
	}{
		{"", remoteexec.LegacyKeyExchangeProperty},
		{remoteexec.LegacyKeyExchangeProperty, remoteexec.LegacyHostKeysProperty},
	} {
		if step.allow != "" {
			set("--set", step.allow+"=true")
		}
		out, err := runPleiadesWithHome(t, dir, home, "trust-host", "sw-l2", "--first-connect")
		if err == nil || !strings.Contains(out, "set "+step.want+": true") {
			t.Fatalf("trust-host allowing %q: %v, want a refusal naming %s\n%s", step.allow, err, step.want, out)
		}
	}
	set("--set", remoteexec.LegacyHostKeysProperty+"=true")
	out, err := runPleiadesWithHome(t, dir, home, "trust-host", "sw-l2", "--first-connect")
	if err != nil || !strings.Contains(out, "ssh-rsa "+remoteexec.Fingerprint(srv.HostKey)) || !strings.Contains(out, "allows legacy SSH key exchange") {
		t.Fatalf("trust-host with both allowed: %v\n%s", err, out)
	}

	// A run reaches it, and says what it used.
	rep, raw, code := runPleiadesJSONWithHome(t, dir, home, "adhoc", "sw-l2", "net.ios.command", "command=show version")
	if code != 0 || len(rep.Tasks) != 1 {
		t.Fatalf("adhoc show version: exit %d\n%s", code, raw)
	}
	warnings := strings.Join(rep.Tasks[0].Warnings, "\n")
	for _, want := range []string{"used legacy SSH key exchange diffie-hellman-group14-sha1", "used legacy SSH host key algorithm ssh-rsa"} {
		if !strings.Contains(warnings, want) {
			t.Errorf("the run's warnings %q do not say %q", warnings, want)
		}
	}
	if !strings.Contains(raw, "vios_l2") {
		t.Errorf("show version's answer is not in the report:\n%s", raw)
	}

	// A flag taken away refuses it again, before any credential is sent.
	logins, refusals := srv.Logins(), srv.Refusals()
	set("--unset", remoteexec.LegacyKeyExchangeProperty)
	_, raw, code = runPleiadesJSONWithHome(t, dir, home, "adhoc", "sw-l2", "net.ios.command", "command=show version")
	if code == 0 || !strings.Contains(raw, remoteexec.LegacyKeyExchangeProperty) {
		t.Fatalf("adhoc with the key exchange no longer allowed: exit %d\n%s", code, raw)
	}
	if srv.Logins() != logins || srv.Refusals() != refusals {
		t.Errorf("a refused negotiation sent a credential: logins %d to %d, refusals %d to %d", logins, srv.Logins(), refusals, srv.Refusals())
	}

	// A flag that is not a boolean is refused where it is written.
	if out, err := runPleiades(t, dir, "set-host", "sw-l2", "--set", remoteexec.LegacyCiphersProperty+"=yes"); err == nil || !strings.Contains(out, "must be true or false") {
		t.Errorf("a flag set to yes: %v\n%s", err, out)
	}
	// And one on a device no SSH connection is made to allows nothing.
	if out, err := runPleiades(t, dir, "add-host", "api", "--type", "generic_http", "--set", "base_url=https://192.0.2.7", "--set", remoteexec.LegacyHostKeysProperty+"=true"); err == nil || !strings.Contains(out, "allows nothing") {
		t.Errorf("a flag on an HTTP device: %v\n%s", err, out)
	}
}

// TestCLI_AChangedHostKeyNamesBothKeysAndTheCommandThatAcceptsIt is the
// changed-key gate: a wait stops at the refusal on its first try, the refusal
// names the port, the new key and the trusted one, and the trust-host command
// that replaces it, no credential is sent, and that command accepts it.
func TestCLI_AChangedHostKeyNamesBothKeysAndTheCommandThatAcceptsIt(t *testing.T) {
	srv := scriptedIOS(t, "c8k-b#", false)
	old := scriptedIOS(t, "old#", false).HostKey
	address := net.JoinHostPort(srv.Host, strconv.Itoa(srv.Port))
	home := emptyHome(t, knownhosts.Line([]string{address}, old)+"\n")
	dir := iosProject(t, srv, "c8k-b", "cisco_router")
	rb := filepath.Join("runbooks", "wait.yaml")
	body := "# What this does: waits for c8k-b's SSH login, for the changed-key gate.\n# Blast radius: c8k-b. Changes: nothing. Undo: none needed.\nid: wait\nhosts: c8k-b\ntasks:\n  - name: wait for the login\n    wait.connection:\n      timeout: 600\n      sleep: 1\n      connect_timeout: 5\n"
	if err := os.WriteFile(filepath.Join(dir, rb), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	rep, raw, code := runPleiadesJSONWithHome(t, dir, home, "run", rb)
	if code == 0 || len(rep.Tasks) != 1 {
		t.Fatalf("a wait on a changed key: exit %d\n%s", code, raw)
	}
	msg := rep.Tasks[0].Error
	for _, want := range []string{
		"stopped at try 1",
		"this side refused what it presented before any credential was sent",
		"port " + strconv.Itoa(srv.Port) + " changed",
		"presented ssh-ed25519 " + remoteexec.Fingerprint(srv.HostKey),
		"trusts ssh-ed25519 " + remoteexec.Fingerprint(old),
		"pleiades trust-host c8k-b --first-connect --replace --reason",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, msg)
		}
	}
	if srv.Logins() != 0 || srv.Refusals() != 0 {
		t.Errorf("a credential reached a host whose key changed: %d logins, %d refusals", srv.Logins(), srv.Refusals())
	}

	// The command it names accepts the new key, and the wait then passes.
	if out, err := runPleiadesWithHome(t, dir, home, "trust-host", "c8k-b", "--first-connect", "--replace", "--reason", "made a new key at boot, checked on its console"); err != nil {
		t.Fatalf("trust-host --replace: %v\n%s", err, out)
	}
	if _, raw, code := runPleiadesJSONWithHome(t, dir, home, "run", rb); code != 0 {
		t.Fatalf("the wait after the new key was trusted: exit %d\n%s", code, raw)
	}
}
