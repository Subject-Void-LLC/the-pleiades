// The services subset's OpenRC probe against a real OpenRC: an Alpine
// container with OpenRC installed, the probe's own command run as a login
// that is not root, once before OpenRC has booted and once after, and its
// output read by the real parser (owner, 2026-11-03: containers where one
// can stand in for a lab machine).
package facts

import (
	"context"
	"io"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/testcontainers/testcontainers-go"
	tcexec "github.com/testcontainers/testcontainers-go/exec"
)

// openrcImage is Alpine, whose init system is OpenRC.
const openrcImage = "alpine:3.20.10"

// containerRunAs runs command through sh in c as user and returns its
// standard output, whatever it exited with (a guarded probe exits non-zero
// where its tool is absent).
func containerRunAs(t *testing.T, c testcontainers.Container, user, command string) string {
	t.Helper()
	_, r, err := c.Exec(context.Background(), []string{"sh", "-c", command}, tcexec.Multiplexed(), tcexec.WithUser(user))
	if err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func TestGatherServices_ReadsARealOpenRC(t *testing.T) {
	if testing.Short() {
		t.Skip("starts an Alpine container")
	}
	c, err := testcontainers.GenericContainer(context.Background(), testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{Image: openrcImage, Cmd: []string{"sleep", "infinity"},
			// An init that reaps: under sleep as PID 1 a daemon OpenRC starts
			// from an exec becomes a zombie its stop cannot end.
			HostConfigModifier: func(hc *container.HostConfig) { init := true; hc.Init = &init }},
		Started: true,
	})
	if err != nil {
		_ = testcontainers.TerminateContainer(c)
		t.Fatalf("starting %s: %v", openrcImage, err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(c) })
	containerRun(t, c, "apk add -q openrc openssh busybox-openrc && rc-update add sshd default && rc-update add crond default && "+
		"rc-update add syslog boot && adduser -D gather")

	var probe gatherProbe
	for _, p := range linuxServiceProbes() {
		if strings.Contains(p.command, "rc-status") {
			probe = p
		}
	}
	read := func() map[string]any { return valuesOf(probe.parse(containerRunAs(t, c, "gather", probe.command))) }

	// Not booted: every script listed, its runlevels read, its state unknown.
	before := read()
	scripts, err := strconv.Atoi(strings.TrimSpace(containerRun(t, c, "rc-service --list | wc -l")))
	if err != nil || len(before) != scripts || scripts == 0 {
		t.Fatalf("%d service facts; rc-service lists %d (%v)", len(before), scripts, err)
	}
	if got := before["service.sshd"]; !reflect.DeepEqual(got, map[string]any{"state": "unknown", "enabled": true, "status": "default", "source": "openrc"}) {
		t.Errorf("before boot, service.sshd = %v", got)
	}

	// Booted, with one default service stopped and one started by hand.
	containerRun(t, c, "mkdir -p /run/openrc && touch /run/openrc/softlevel && openrc default >/dev/null 2>&1; "+
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
		status := containerRunAs(t, c, "gather", "rc-service "+name+" status 2>&1")
		running := strings.Contains(status, "started")
		if got := after["service."+name].(map[string]any)["state"] == "running"; got != running {
			t.Errorf("%s: fact says running %v, rc-service says %q", name, got, strings.TrimSpace(status))
		}
	}
}

func TestParseOpenRC_ReadsItsThreeSections(t *testing.T) {
	out := "sshd\ncrond\nlocal\nkillprocs\nsshd\n" + serviceSectionMark + "\n                sshd |      default                           \n" +
		"            killprocs |                     shutdown          \n               crond |      default    boot     \n" + serviceSectionMark +
		"\n[default]\nsshd =  started \ncrond =  crashed \n[Dynamic manual]\nlocal =  started \n"
	got := valuesOf(parseOpenRC(out))
	want := map[string]any{
		"service.sshd":      map[string]any{"state": "running", "enabled": true, "status": "default", "source": "openrc"},
		"service.crond":     map[string]any{"state": "crashed", "enabled": true, "status": "default,boot", "source": "openrc"},
		"service.local":     map[string]any{"state": "running", "enabled": false, "status": "disabled", "source": "openrc"},
		"service.killprocs": map[string]any{"state": "stopped", "enabled": false, "status": "shutdown", "source": "openrc"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("openrc = %v\nwant %v", got, want)
	}
	if parseOpenRC("one section") != nil {
		t.Error("output missing its sections was read")
	}
}
