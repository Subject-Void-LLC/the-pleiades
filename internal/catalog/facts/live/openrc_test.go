// The services subset's OpenRC probe against a real OpenRC: an Alpine
// container with OpenRC installed, the probe's own command run as a login
// that is not root, once before OpenRC has booted and once after, and its
// output read by the real parser (owner, 2026-11-03: containers where one
// can stand in for a lab machine).
package live

import (
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/facts"
)

// openrcImage is Alpine, whose init system is OpenRC.
const openrcImage = "alpine:3.20.10"

func TestGatherServices_ReadsARealOpenRC(t *testing.T) {
	c := start(t, openrcImage)
	run(t, c, "apk add -q openrc openssh busybox-openrc && rc-update add sshd default && rc-update add crond default && "+
		"rc-update add syslog boot && adduser -D gather")

	probes := facts.Probes("Linux", "services", "rc-status")
	if len(probes) != 1 {
		t.Fatalf("%d OpenRC probes", len(probes))
	}
	read := func() map[string]any {
		out, _ := runAs(t, c, "gather", probes[0].Command)
		return probes[0].Read(out)
	}

	// Not booted: every script listed, its runlevels read, its state unknown.
	before := read()
	scripts, err := strconv.Atoi(strings.TrimSpace(run(t, c, "rc-service --list | wc -l")))
	if err != nil || len(before) != scripts || scripts == 0 {
		t.Fatalf("%d service facts; rc-service lists %d (%v)", len(before), scripts, err)
	}
	if got := before["service.sshd"]; !reflect.DeepEqual(got, map[string]any{"state": "unknown", "enabled": true, "status": "default", "source": "openrc"}) {
		t.Errorf("before boot, service.sshd = %v", got)
	}

	// Booted, with one default service stopped and one started by hand.
	run(t, c, "mkdir -p /run/openrc && touch /run/openrc/softlevel && openrc default >/dev/null 2>&1; "+
		"rc-service crond stop >/dev/null 2>&1; rc-service local start >/dev/null 2>&1; true")
	after := read()
	want := map[string]map[string]any{
		"sshd":   {"state": "running", "enabled": true, "status": "default", "source": "openrc"},
		"crond":  {"state": "stopped", "enabled": true, "status": "default", "source": "openrc"},
		"syslog": {"state": "running", "enabled": true, "status": "boot", "source": "openrc"},
		"local":  {"state": "running", "enabled": false, "status": "disabled", "source": "openrc"},
	}
	for name, w := range want {
		if got := after["service."+name]; !reflect.DeepEqual(got, w) {
			t.Errorf("service.%s = %v, want %v", name, got, w)
		}
	}
	// Each state held to rc-status's own answer for that script.
	for _, name := range []string{"sshd", "crond", "syslog", "local"} {
		status, _ := runAs(t, c, "gather", "rc-service "+name+" status 2>&1")
		running := strings.Contains(status, "started")
		if got := after["service."+name].(map[string]any)["state"] == "running"; got != running {
			t.Errorf("%s: fact says running %v, rc-service says %q", name, got, strings.TrimSpace(status))
		}
	}
}
