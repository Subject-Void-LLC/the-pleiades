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
	// Each script is two facts: the service and its state (service_state.).
	if err != nil || len(before) != 2*scripts || scripts == 0 {
		t.Fatalf("%d service facts; rc-service lists %d (%v)", len(before), scripts, err)
	}
	if got := before["service.sshd"]; !reflect.DeepEqual(got, map[string]any{"enabled": true, "status": "default", "source": "openrc"}) || before["service_state.sshd"] != "unknown" {
		t.Errorf("before boot, service.sshd = %v, state %v", got, before["service_state.sshd"])
	}

	// Booted, with one default service stopped and one started by hand.
	run(t, c, "mkdir -p /run/openrc && touch /run/openrc/softlevel && openrc default >/dev/null 2>&1; "+
		"rc-service crond stop >/dev/null 2>&1; rc-service local start >/dev/null 2>&1; true")
	after := read()
	want := map[string]map[string]any{
		"sshd":   {"enabled": true, "status": "default", "source": "openrc"},
		"crond":  {"enabled": true, "status": "default", "source": "openrc"},
		"syslog": {"enabled": true, "status": "boot", "source": "openrc"},
		"local":  {"enabled": false, "status": "disabled", "source": "openrc"},
	}
	states := map[string]string{"sshd": "running", "crond": "stopped", "syslog": "running", "local": "running"}
	for name, w := range want {
		if got := after["service."+name]; !reflect.DeepEqual(got, w) || after["service_state."+name] != states[name] {
			t.Errorf("service.%s = %v, state %v, want %v, %s", name, got, after["service_state."+name], w, states[name])
		}
	}
	// Each state held to rc-status's own answer for that script.
	for _, name := range []string{"sshd", "crond", "syslog", "local"} {
		status, _ := runAs(t, c, "gather", "rc-service "+name+" status 2>&1")
		running := strings.Contains(status, "started")
		if got := after["service_state."+name] == "running"; got != running {
			t.Errorf("%s: fact says running %v, rc-service says %q", name, got, strings.TrimSpace(status))
		}
	}
}
