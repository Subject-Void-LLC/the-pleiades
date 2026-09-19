// Package cli: opening a real session, over a real SSH connection, with
// the prompt the device itself declares.
//
// The rest of this package runs through the openSession seam, which
// proves each method's own decisions. Nothing proved the seam's
// implementation: the connection, the pty-req, the shell request,
// synchronizing on the device's own declared prompt, a line, its echo,
// the reply, and closing the session and the connection under it. That
// prompt is the whole of what these two generic methods know about the
// device, so an opening that ignored it would hang against every device
// whose prompt is not the one netcli happened to assume.
//
// The far end is remoteexectest's scripted device: a prompt and a reply
// per line. A real device's own conventions are pkg/netcli's to cover.
package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	inventorytest "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
)

// sshDevice is a device reachable over SSH that declares its own CLI
// prompt, which is what capability.NetworkCLICapable means.
type sshDevice struct {
	*inventorytest.Stub
	host   string
	port   int
	prompt string
}

func (d *sshDevice) SSHHost() string   { return d.host }
func (d *sshDevice) SSHPort() int      { return d.port }
func (d *sshDevice) CLIPrompt() string { return d.prompt }

// secretContext carries the credential the session authenticates with.
type secretContext struct {
	*stubContext
	secrets map[string]string
}

func (c *secretContext) InjectSecrets() map[string]string { return c.secrets }

// scriptedDevice starts a device answering prompt, and returns a device
// and context pointed at it. declared is what the device claims its
// prompt is, which a test can make disagree with what it prints.
func scriptedDevice(t *testing.T, prompt, declared string, replies map[string]string) (*remoteexectest.Server, *sshDevice, *secretContext) {
	t.Helper()
	srv, err := remoteexectest.Start(remoteexectest.Options{Device: &remoteexectest.Device{
		Prompt:  prompt,
		Replies: replies,
	}})
	if err != nil {
		t.Fatalf("starting the scripted device: %v", err)
	}
	t.Cleanup(srv.Close)
	device := &sshDevice{
		Stub:   &inventorytest.Stub{StubName: "sw1", Caps: []capability.Name{capability.NameSSHTransport}},
		host:   srv.Host,
		port:   srv.Port,
		prompt: declared,
	}
	return srv, device, &secretContext{stubContext: newStubContext(), secrets: srv.Secrets()}
}

// params are what every test here passes: the harness presents a
// generated host key no known_hosts file names.
func params(extra map[string]any) map[string]any {
	p := map[string]any{"insecure_skip_host_key_verify": true}
	for k, v := range extra {
		p[k] = v
	}
	return p
}

// TestOpenSession_SynchronizesOnTheDevicesDeclaredPrompt covers a real
// session opened against a device whose prompt is its own, not a vendor
// convention: the command reaches the device and its reply comes back
// under the stdout stat, with the echoed line and the prompt stripped.
//
// The generic methods send no paging command, which is the difference
// from net.ios.config and the reason runCommand bounds every line: the
// device receives exactly what the task asked for and nothing else.
func TestOpenSession_SynchronizesOnTheDevicesDeclaredPrompt(t *testing.T) {
	srv, device, rc := scriptedDevice(t, "sw1>", "sw1>", map[string]string{
		"show clock": "*12:00:00.000 UTC Fri Sep 19 2026",
	})

	result, err := Command(context.Background(), rc, device, params(map[string]any{"command": "show clock"}))
	if err != nil {
		t.Fatalf("command over a real session: %v", err)
	}
	if !result.Changed {
		t.Error("net.cli.command reports changed for every command it ran; this one did not")
	}
	if got := srv.Commands(); len(got) != 1 || got[0] != "show clock" {
		t.Errorf("the device received %v, want only the task's own line", got)
	}
	out, _ := rc.stats[statStdout].(string)
	if !strings.Contains(out, "UTC Fri Sep 19 2026") {
		t.Errorf("stdout = %q, want the device's reply", out)
	}
	if strings.Contains(out, "show clock") || strings.Contains(out, "sw1>") {
		t.Errorf("stdout = %q, want the echoed line and the prompt stripped", out)
	}
}

// TestOpenSession_SendsEveryConfigLineOverOneSession covers net.cli.config
// against a real session: each line reaches the device, in order, over the
// one session the method opens, since a generic device has no
// configuration mode this method could enter.
func TestOpenSession_SendsEveryConfigLineOverOneSession(t *testing.T) {
	srv, device, rc := scriptedDevice(t, "sw1#", "sw1#", nil)

	if _, err := Config(context.Background(), rc, device, params(map[string]any{
		"config": "hostname sw1\nntp server 10.0.0.1\n",
	})); err != nil {
		t.Fatalf("config over a real session: %v", err)
	}
	got := srv.Commands()
	if len(got) != 2 || got[0] != "hostname sw1" || got[1] != "ntp server 10.0.0.1" {
		t.Errorf("the device received %v, want both lines in order", got)
	}
}

// TestOpenSession_FailsWhenTheDeclaredPromptIsWrong is the failure an
// operator actually hits: cli_prompt is a property somebody typed, and a
// device printing something else never matches it. The session gives up
// on the caller's deadline rather than waiting forever, and says which
// method was waiting.
func TestOpenSession_FailsWhenTheDeclaredPromptIsWrong(t *testing.T) {
	_, device, rc := scriptedDevice(t, "switch-1>", "sw1>", nil)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	start := time.Now()
	_, err := Command(ctx, rc, device, params(map[string]any{"command": "show clock"}))
	if err == nil || !strings.Contains(err.Error(), "net.cli.command") {
		t.Fatalf("err = %v, want a failure named for the method", err)
	}
	if waited := time.Since(start); waited > 10*time.Second {
		t.Errorf("the session waited %s, past the deadline it was given", waited)
	}
}
