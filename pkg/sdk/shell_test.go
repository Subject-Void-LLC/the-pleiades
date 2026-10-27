// Tests of OpenShell: the carrier it picks for a device, against a real
// in-process SSH server with a scripted CLI and a scripted Telnet device
// on a real TCP listener, and what it refuses.
package sdk_test

import (
	"bufio"
	"context"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport/need"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcli"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// cliDevice is a device reachable over SSH, Telnet or both, as its
// declared capabilities say.
type cliDevice struct {
	*inventorytest.Stub
	host       string
	ssh, tport int
}

func (d *cliDevice) SSHHost() string    { return d.host }
func (d *cliDevice) SSHPort() int       { return d.ssh }
func (d *cliDevice) TelnetHost() string { return d.host }
func (d *cliDevice) TelnetPort() int    { return d.tport }

func newCLIDevice(host string, sshPort, telnetPort int, caps ...capability.Name) *cliDevice {
	return &cliDevice{Stub: &inventorytest.Stub{StubID: "r1", StubName: "r1", Caps: caps, StubState: inventory.StateActive}, host: host, ssh: sshPort, tport: telnetPort}
}

// telnetLogin serves one Telnet login for cisco/cisco, landing at "R1>".
func telnetLogin(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		r := bufio.NewReader(conn)
		_, _ = conn.Write([]byte("Username: "))
		u, _ := r.ReadString('\n')
		_, _ = conn.Write([]byte("Password: "))
		p, _ := r.ReadString('\n')
		if strings.TrimSpace(u) != "cisco" || strings.TrimSpace(p) != "cisco" {
			_, _ = conn.Write([]byte("\r\n% Login invalid\r\n\r\nUsername: "))
			return
		}
		_, _ = conn.Write([]byte("\r\nR1>"))
		time.Sleep(2 * time.Second)
	}()
	return ln.Addr().(*net.TCPAddr).Port
}

var r1Prompt = regexp.MustCompile(`(?m)^R1[>#]\s*$`)

// TestOpenShell_PicksTheCarrierTheDeviceIsReachedBy: a device reachable
// over SSH gets an SSH shell, even when it is also reachable over Telnet;
// a Telnet-only device gets a logged-in Telnet session, with the task's
// opt-in.
func TestOpenShell_PicksTheCarrierTheDeviceIsReachedBy(t *testing.T) {
	need.POSIXShell(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	srv, err := remoteexectest.Start(remoteexectest.Options{Device: &remoteexectest.Device{Prompt: "R1#"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	both := newCLIDevice(srv.Host, srv.Port, 1, capability.NameSSHTransport, capability.NameTelnet)
	shell, err := sdk.OpenShell(ctx, newSecretContext(srv.Secrets()), both, map[string]any{sdk.ParamInsecureSkipHostKeyVerify: true}, "test.cli")
	if err != nil {
		t.Fatalf("over SSH: %v", err)
	}
	if out, err := shell.ReadUntil(ctx, r1Prompt, 1<<16); err != nil || !strings.Contains(out, "R1#") {
		t.Errorf("the SSH prompt = %q, %v", out, err)
	}
	if err := shell.Close(); err != nil {
		t.Errorf("closing the SSH shell: %v", err)
	}

	telnetOnly := newCLIDevice("127.0.0.1", 0, telnetLogin(t), capability.NameTelnet)
	secrets := map[string]string{wire.SecretUsername: "cisco", wire.SecretPassword: "cisco"}
	shell, err = sdk.OpenShell(ctx, newSecretContext(secrets), telnetOnly, map[string]any{sdk.ParamInsecureTelnet: true}, "test.cli")
	if err != nil {
		t.Fatalf("over Telnet: %v", err)
	}
	defer shell.Close()
	if out, err := shell.ReadUntil(ctx, r1Prompt, 1<<16); err != nil || !strings.Contains(out, "R1>") {
		t.Errorf("the Telnet prompt = %q, %v", out, err)
	}
}

// TestOpenShell_Refusals: no device, a device reachable by neither
// carrier, Telnet without the opt-in, and a Telnet login with no
// credential, which names the command that stores one.
func TestOpenShell_Refusals(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	optIn := map[string]any{sdk.ParamInsecureTelnet: true}
	for name, tc := range map[string]struct {
		device  inventory.InventoryItem
		params  map[string]any
		secrets map[string]string
		want    string
	}{
		"no device":       {nil, optIn, nil, "no target device"},
		"neither carrier": {newCLIDevice("127.0.0.1", 22, 23, capability.NameNetworkAddressable), optIn, nil, "neither SSH nor Telnet"},
		"no opt-in":       {newCLIDevice("127.0.0.1", 0, 23, capability.NameTelnet), nil, nil, "insecure_telnet: true among the method's parameters"},
		"no credential":   {newCLIDevice("127.0.0.1", 0, telnetLogin(t), capability.NameTelnet), optIn, map[string]string{}, "pleiades add-credential"},
	} {
		if _, err := sdk.OpenShell(ctx, newSecretContext(tc.secrets), tc.device, tc.params, "test.cli"); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want it to say %q", name, err, tc.want)
		}
	}
}

// TestOpenShell_FailuresAfterTheChoice: an SSH device whose port refuses,
// an SSH server that declines a shell, and a Telnet device whose port
// refuses each fail naming the task; a shell closed twice reports the
// second close.
func TestOpenShell_FailuresAfterTheChoice(t *testing.T) {
	need.POSIXShell(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	skip := map[string]any{sdk.ParamInsecureSkipHostKeyVerify: true}
	srv := startServer(t)
	refused := newCLIDevice("127.0.0.1", 1, 0, capability.NameSSHTransport)
	if _, err := sdk.OpenShell(ctx, newSecretContext(srv.Secrets()), refused, skip, "test.cli"); err == nil || !strings.Contains(err.Error(), "test.cli") {
		t.Errorf("an SSH port that refuses = %v", err)
	}
	// A task that asked for Telnet of a device whose record says SSH is
	// told where the carrier is chosen.
	telnetAsked := map[string]any{sdk.ParamInsecureSkipHostKeyVerify: true, sdk.ParamInsecureTelnet: true}
	if _, err := sdk.OpenShell(ctx, newSecretContext(srv.Secrets()), refused, telnetAsked, "test.cli"); err == nil || !strings.Contains(err.Error(), "--set transport=telnet") {
		t.Errorf("an SSH device given insecure_telnet = %v", err)
	}
	// A device that answered and was refused (its host key unknown) gets no
	// such advice: Telnet is no fix for a key that may be a machine in the
	// middle.
	knownHosts := filepath.Join(t.TempDir(), "known_hosts")
	if err := os.WriteFile(knownHosts, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(remoteexec.KnownHostsEnv, knownHosts)
	answered := newCLIDevice(srv.Host, srv.Port, 0, capability.NameSSHTransport)
	if _, err := sdk.OpenShell(ctx, newSecretContext(srv.Secrets()), answered, map[string]any{sdk.ParamInsecureTelnet: true}, "test.cli"); err == nil || strings.Contains(err.Error(), "transport=telnet") {
		t.Errorf("a refused host key given insecure_telnet = %v", err)
	}
	noShell := newCLIDevice(srv.Host, srv.Port, 0, capability.NameSSHTransport)
	if _, err := sdk.OpenShell(ctx, newSecretContext(srv.Secrets()), noShell, skip, "test.cli"); err == nil || !strings.Contains(err.Error(), "test.cli") {
		t.Errorf("a server that declines a shell = %v", err)
	}
	telnetRefused := newCLIDevice("127.0.0.1", 0, 1, capability.NameTelnet)
	if _, err := sdk.OpenShell(ctx, newSecretContext(nil), telnetRefused, map[string]any{sdk.ParamInsecureTelnet: true}, "test.cli"); err == nil || !strings.Contains(err.Error(), "dialing") {
		t.Errorf("a Telnet port that refuses = %v", err)
	}

	cli, err := remoteexectest.Start(remoteexectest.Options{Device: &remoteexectest.Device{Prompt: "R1#"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cli.Close)
	shell, err := sdk.OpenShell(ctx, newSecretContext(cli.Secrets()), newCLIDevice(cli.Host, cli.Port, 0, capability.NameSSHTransport), skip, "test.cli")
	if err != nil {
		t.Fatal(err)
	}
	if err := shell.Close(); err != nil {
		t.Fatalf("the first Close: %v", err)
	}
	if err := shell.Close(); err == nil {
		t.Error("a second Close reported nothing")
	}
}

// TestOpenCLI: a session in IOS's dialect over a real SSH server, the
// carrier's failure named for the task, and a dialect that cannot bound a
// read refused with the shell closed again.
func TestOpenCLI(t *testing.T) {
	need.POSIXShell(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	srv, err := remoteexectest.Start(remoteexectest.Options{Device: &remoteexectest.Device{Prompt: "R1#"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	skip := map[string]any{sdk.ParamInsecureSkipHostKeyVerify: true}
	device := newCLIDevice(srv.Host, srv.Port, 0, capability.NameSSHTransport)

	session, err := sdk.OpenCLI(ctx, newSecretContext(srv.Secrets()), device, skip, "test.cli", netcli.IOS)
	if err != nil {
		t.Fatalf("OpenCLI: %v", err)
	}
	if err := session.Close(); err != nil {
		t.Errorf("closing the session: %v", err)
	}

	if _, err := sdk.OpenCLI(ctx, newSecretContext(nil), nil, skip, "test.cli", netcli.IOS); err == nil || !strings.Contains(err.Error(), "no target device") {
		t.Errorf("no device: %v", err)
	}
	if _, err := sdk.OpenCLI(ctx, newSecretContext(srv.Secrets()), device, skip, "test.cli", netcli.Dialect{}); err == nil || !strings.Contains(err.Error(), "test.cli") || !strings.Contains(err.Error(), "no prompt pattern") {
		t.Errorf("a dialect with no prompt: %v", err)
	}
}
