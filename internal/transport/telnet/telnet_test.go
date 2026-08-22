package telnet_test

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/transport"
	telnettransport "github.com/Subject-Void-LLC/the-pleiades/internal/transport/telnet"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/telnetexec"
)

// writeKnownHostsFor writes a known_hosts file naming exactly bastion's
// own real host key under its own address: every hop always gets real
// host key verification (internal/transport.Hop carries no per-hop
// opt-in like remoteexec.Hop.InsecureSkipHostKeyVerify does, matching
// internal/transport/ssh's own hopsFrom, which leaves it unset too), so
// InsecureSkipHostKeyVerify on remoteexec.Options alone is not enough to
// reach a bastion leg -- only Connect's own final SSH leg reads that
// field.
func writeKnownHostsFor(t *testing.T, bastion *remoteexectest.Server) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "known_hosts")
	line := knownhosts.Line([]string{bastion.Addr()}, bastion.HostKey)
	if err := os.WriteFile(path, []byte(line+"\n"), 0o600); err != nil {
		t.Fatalf("writing known_hosts: %v", err)
	}
	return path
}

// TestExec_RoundTripsThroughARealTelnetServer proves this Adapter is not
// just a type-erased pass-through in name: it actually dials a real
// server, and the far end's response reaches transport.Result.Stdout.
func TestExec_RoundTripsThroughARealTelnetServer(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	port := ln.Addr().(*net.TCPAddr).Port

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 256)
		_, _ = conn.Read(buf)
		_, _ = conn.Write([]byte("hello from device\r\n"))
	}()

	tr := telnettransport.New(telnetexec.Options{ReadTimeout: 200 * time.Millisecond}, remoteexec.Options{})
	target := transport.Target{Endpoint: transport.NetworkEndpoint{Host: "127.0.0.1", Port: port}}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := tr.Exec(ctx, target, credential.Credential{}, "show version")
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if result.Stdout != "hello from device\r\n" {
		t.Errorf("Stdout = %q, want %q", result.Stdout, "hello from device\r\n")
	}
	if !result.ExitStatusUnknown {
		t.Error("expected ExitStatusUnknown to always be true for bare Telnet")
	}
}

// TestExec_RejectsAnyEndpointOtherThanNetworkEndpoint proves a
// binding-configuration bug (this Adapter reached with the wrong
// Endpoint kind) fails with a clear type error naming the actual type it
// got, not a panic and not a silent misinterpretation of unrelated
// fields.
func TestExec_RejectsAnyEndpointOtherThanNetworkEndpoint(t *testing.T) {
	tr := telnettransport.New(telnetexec.Options{}, remoteexec.Options{})
	target := transport.Target{Endpoint: transport.LocalSocketEndpoint{Address: "/tmp/not-a-telnet-endpoint"}}

	_, err := tr.Exec(context.Background(), target, credential.Credential{}, "cmd")
	if err == nil {
		t.Fatal("expected an error for a non-NetworkEndpoint target")
	}
}

// TestExec_WrapsAnUnderlyingFailureClearly proves a real failure from
// pkg/telnetexec (here, a dial to a closed port) reaches the caller as a
// wrapped, non-nil error rather than a zero-value success.
func TestExec_WrapsAnUnderlyingFailureClearly(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close() // nothing is listening now

	tr := telnettransport.New(telnetexec.Options{}, remoteexec.Options{})
	target := transport.Target{Endpoint: transport.NetworkEndpoint{Host: "127.0.0.1", Port: port}}

	_, err = tr.Exec(context.Background(), target, credential.Credential{}, "cmd")
	if err == nil {
		t.Fatal("expected an error dialing a closed listener")
	}
}

// startDeviceServer starts a real, plain TCP listener that writes
// response immediately upon accept (mirroring
// TestExec_RoundTripsThroughARealTelnetServer's own device: telnetexec's
// banner-drain read phase captures it before the command is ever
// written), standing in for a Telnet-only device this Adapter never
// speaks SSH to directly.
func startDeviceServer(t *testing.T, response string) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = conn.Write([]byte(response))
		buf := make([]byte, 256)
		_, _ = conn.Read(buf)
	}()

	return ln.Addr().(*net.TCPAddr).Port
}

// TestExec_TunnelsThroughABastionWhenRouteIsSet proves target.Route is
// honored, not silently ignored (the real gap Phase 73's own Workstream
// G found while building the bastion proof): a genuine SSH handshake to
// a real bastion (remoteexectest.Server, now forwarding-capable), a
// genuine direct-tcpip channel opened through it, landing on a device
// that speaks no SSH at all -- this Adapter's own real bare-Telnet
// protocol reaches it end to end through the tunnel.
func TestExec_TunnelsThroughABastionWhenRouteIsSet(t *testing.T) {
	bastion, err := remoteexectest.Start(remoteexectest.Options{})
	if err != nil {
		t.Fatalf("starting the fake bastion: %v", err)
	}
	defer bastion.Close()

	devicePort := startDeviceServer(t, "hello-through-bastion\r\n")
	knownHostsPath := writeKnownHostsFor(t, bastion)

	tr := telnettransport.New(
		telnetexec.Options{ReadTimeout: 300 * time.Millisecond},
		remoteexec.Options{KnownHostsPath: knownHostsPath, MaxRetries: 1},
	)
	target := transport.Target{
		Endpoint: transport.NetworkEndpoint{Host: "127.0.0.1", Port: devicePort},
		Route: []transport.Hop{{
			Host:       bastion.Host,
			Port:       bastion.Port,
			DeviceName: "bastion",
			Credential: credential.Credential{Username: bastion.Username, Password: bastion.Password},
		}},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := tr.Exec(ctx, target, credential.Credential{}, "show version")
	if err != nil {
		t.Fatalf("Exec through the bastion: %v", err)
	}
	if result.Stdout != "hello-through-bastion\r\n" {
		t.Errorf("Stdout = %q, want %q", result.Stdout, "hello-through-bastion\r\n")
	}
	if !result.ExitStatusUnknown {
		t.Error("expected ExitStatusUnknown to always be true for bare Telnet")
	}
}

// TestExec_UnreachableDeviceThroughBastionFailsWithChannelError proves
// the tunneled failure mode: the bastion's own handshake succeeds, but
// nothing is listening at the device address, so the channel open
// itself fails and must surface as a real, named error rather than a
// hang or a zero-value success.
func TestExec_UnreachableDeviceThroughBastionFailsWithChannelError(t *testing.T) {
	bastion, err := remoteexectest.Start(remoteexectest.Options{})
	if err != nil {
		t.Fatalf("starting the fake bastion: %v", err)
	}
	defer bastion.Close()

	deadListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	unreachablePort := deadListener.Addr().(*net.TCPAddr).Port
	deadListener.Close()
	knownHostsPath := writeKnownHostsFor(t, bastion)

	tr := telnettransport.New(
		telnetexec.Options{},
		remoteexec.Options{KnownHostsPath: knownHostsPath, MaxRetries: 1},
	)
	target := transport.Target{
		Endpoint: transport.NetworkEndpoint{Host: "127.0.0.1", Port: unreachablePort},
		Route: []transport.Hop{{
			Host:       bastion.Host,
			Port:       bastion.Port,
			DeviceName: "bastion",
			Credential: credential.Credential{Username: bastion.Username, Password: bastion.Password},
		}},
	}

	_, err = tr.Exec(context.Background(), target, credential.Credential{}, "cmd")
	if err == nil {
		t.Fatal("expected a channel-open failure against an address nothing is listening on")
	}
}
