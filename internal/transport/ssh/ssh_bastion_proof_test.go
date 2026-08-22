package ssh

import (
	"context"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"go.uber.org/goleak"
	"golang.org/x/crypto/ssh"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/rfc2217"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/serialline"
)

// This file is the bastion proof Phase 72 explicitly deferred: a real,
// two-hop route across two genuinely isolated Docker networks, reaching
// a console server (ser2net, speaking real RFC 2217 against a real
// socat PTY pair) that has no path to the outside world except through
// both bastions in order.
//
// # Topology
//
//	outer network:      [test process] <-> outer-jump-host <-> inner-bastion
//	management network:                                        inner-bastion <-> console-server
//
// outer-jump-host is attached ONLY to "outer" (published to the host,
// for the test process's own first hop, and so its real host key can be
// captured directly). inner-bastion bridges "outer" and "management"
// (also published, purely so its own real host key can be captured by a
// direct bootstrap dial the same way — nothing in the actual hop-chain
// dial under test ever uses that published port; the real dial reaches
// inner-bastion only via outer-jump-host's own direct-tcpip channel,
// exactly like every other hop-chain test in this package). console-
// server is attached ONLY to "management" and has NO port published to
// the host at all: that is the one boundary this file's own control
// assertions exist to prove, not a testing convenience.
//
// # What this proves, and what it honestly does not
//
// A real socat PTY pair proves the RFC 2217 negotiation, subnegotiation
// framing, and data path against a real, independent ser2net
// implementation (not this module's own fake server) — genuine
// interoperability evidence pkg/rfc2217's own unit tests cannot provide
// on their own. It does NOT prove a physical line's baud rate actually
// changed: querying the PTY device ser2net itself manages
// (`stty -F /tmp/ttyA`) after a successful SetLine call, empirically,
// during this file's own development, showed no observable termios
// change at all — ser2net's SET_BAUDRATE acknowledgment is real
// protocol behavior, not proof of a physical effect a PTY cannot have.
// SendBreak DOES get a real acknowledgment from ser2net over a PTY;
// AssertDTR does NOT (it times out with no acknowledgment at all, a
// real, verified PTY limitation, not a bug in this package) — this is
// tested deliberately below, not silently skipped.

// newBastionProofNetwork creates a fresh, isolated Docker network and
// returns its name.
func newBastionProofNetwork(t *testing.T) string {
	t.Helper()
	nw, err := network.New(context.Background())
	if err != nil {
		t.Fatalf("creating network: %v", err)
	}
	t.Cleanup(func() { _ = nw.Remove(context.Background()) })
	return nw.Name
}

// startBastionProofOuterJumpHost starts a real openssh-server container
// attached only to outerNet.
func startBastionProofOuterJumpHost(t *testing.T, outerNet string) testcontainers.Container {
	t.Helper()
	req := testcontainers.ContainerRequest{
		Image:        testsupport.SSHDImage,
		ExposedPorts: []string{"2222/tcp"},
		Env: map[string]string{
			"PUID":            "1000",
			"PGID":            "1000",
			"PASSWORD_ACCESS": "true",
			"USER_NAME":       containerSSHUser,
			"USER_PASSWORD":   containerSSHPassword,
		},
		Files: []testcontainers.ContainerFile{{
			Reader:            strings.NewReader("AllowTcpForwarding yes\n"),
			ContainerFilePath: "/config/sshd/sshd_config.d/allow-tcp-forwarding.conf",
			FileMode:          0o644,
		}},
		WaitingFor:     wait.ForLog("done.").WithStartupTimeout(testsupport.SSHDStartupTimeout),
		Networks:       []string{outerNet},
		NetworkAliases: map[string][]string{outerNet: {"outerjump"}},
	}
	c, err := testcontainers.GenericContainer(context.Background(), testcontainers.GenericContainerRequest{ContainerRequest: req, Started: true})
	if err != nil {
		t.Fatalf("starting outer jump host: %v", err)
	}
	t.Cleanup(func() { _ = c.Terminate(context.Background()) })
	return c
}

// startBastionProofInnerBastion starts a real openssh-server container
// bridging outerNet and mgmtNet: reachable from outer-jump-host on
// outerNet, and itself reaching console-server on mgmtNet.
func startBastionProofInnerBastion(t *testing.T, outerNet, mgmtNet string) testcontainers.Container {
	t.Helper()
	req := testcontainers.ContainerRequest{
		Image:        testsupport.SSHDImage,
		ExposedPorts: []string{"2222/tcp"},
		Env: map[string]string{
			"PUID":            "1000",
			"PGID":            "1000",
			"PASSWORD_ACCESS": "true",
			"USER_NAME":       containerSSHUser,
			"USER_PASSWORD":   containerSSHPassword,
		},
		Files: []testcontainers.ContainerFile{{
			Reader:            strings.NewReader("AllowTcpForwarding yes\n"),
			ContainerFilePath: "/config/sshd/sshd_config.d/allow-tcp-forwarding.conf",
			FileMode:          0o644,
		}},
		WaitingFor: wait.ForLog("done.").WithStartupTimeout(testsupport.SSHDStartupTimeout),
		Networks:   []string{outerNet, mgmtNet},
		NetworkAliases: map[string][]string{
			outerNet: {"innerbastion"},
			mgmtNet:  {"innerbastion"},
		},
	}
	c, err := testcontainers.GenericContainer(context.Background(), testcontainers.GenericContainerRequest{ContainerRequest: req, Started: true})
	if err != nil {
		t.Fatalf("starting inner bastion: %v", err)
	}
	t.Cleanup(func() { _ = c.Terminate(context.Background()) })
	return c
}

// startBastionProofConsoleServer builds and starts the real ser2net +
// socat console-server emulator (testdata/consoleserver), attached ONLY
// to mgmtNet, with NO port published to the host at all: readiness is
// checked from INSIDE the container (SkipExternalCheck), not via a host
// mapping this container deliberately never gets.
func startBastionProofConsoleServer(t *testing.T, mgmtNet string) testcontainers.Container {
	t.Helper()
	req := testcontainers.ContainerRequest{
		FromDockerfile: testcontainers.FromDockerfile{
			Context:    "testdata/consoleserver",
			Dockerfile: "Dockerfile",
		},
		WaitingFor:     wait.ForListeningPort("7000/tcp").SkipExternalCheck().WithStartupTimeout(2 * time.Minute),
		Networks:       []string{mgmtNet},
		NetworkAliases: map[string][]string{mgmtNet: {"consoleserver"}},
	}
	c, err := testcontainers.GenericContainer(context.Background(), testcontainers.GenericContainerRequest{ContainerRequest: req, Started: true})
	if err != nil {
		t.Fatalf("starting console server: %v", err)
	}
	t.Cleanup(func() { _ = c.Terminate(context.Background()) })
	return c
}

// bootstrapHostKey captures c's real host key via a direct bootstrap
// dial to its own mapped port, the same recording-callback pattern
// TestSSHContainer_HostKeyVerification and ssh_hop_chaos_test.go's own
// bootstrap dial already use. This is a test-only convenience: nothing
// in the real hop-chain dial under test ever reaches this mapped port.
func bootstrapHostKey(t *testing.T, c testcontainers.Container) ssh.PublicKey {
	t.Helper()
	ctx := context.Background()
	host, err := c.Host(ctx)
	if err != nil {
		t.Fatalf("container host: %v", err)
	}
	mapped, err := c.MappedPort(ctx, "2222/tcp")
	if err != nil {
		t.Fatalf("mapped port: %v", err)
	}
	addr := net.JoinHostPort(host, mapped.Port())

	var capturedKey ssh.PublicKey
	recordingConfig := &ssh.ClientConfig{
		User: containerSSHUser,
		Auth: []ssh.AuthMethod{ssh.Password(containerSSHPassword)},
		HostKeyCallback: func(hostname string, remote net.Addr, key ssh.PublicKey) error {
			capturedKey = key
			return nil
		},
		Timeout: 5 * time.Second,
	}
	client, err := ssh.Dial("tcp", addr, recordingConfig)
	if err != nil {
		t.Fatalf("bootstrap dial to %s: %v", addr, err)
	}
	client.Close()
	if capturedKey == nil {
		t.Fatalf("expected to capture a real host key from %s", addr)
	}
	return capturedKey
}

// TestBastionProof is the real thing: a genuine two-hop route (client ->
// outer-jump-host -> inner-bastion), each leg its own independent SSH
// handshake with its own verified host key, landing on a console server
// one further real TCP hop away that speaks no SSH at all —
// pkg/rfc2217's own protocol negotiates against a real, independent
// ser2net implementation over the resulting tunneled net.Conn
// (pkg/remoteexec.Runner.DialThroughHops). One topology is started once
// and shared across every subtest below, the same resource-conscious
// pattern ssh_container_test.go's own shared fixture already
// establishes for its own (cheaper, single-container) tests.
func TestBastionProof(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping bastion-proof integration test in short mode")
	}
	ctx := context.Background()

	outerNet := newBastionProofNetwork(t)
	mgmtNet := newBastionProofNetwork(t)

	outerJump := startBastionProofOuterJumpHost(t, outerNet)
	innerBastion := startBastionProofInnerBastion(t, outerNet, mgmtNet)
	consoleServer := startBastionProofConsoleServer(t, mgmtNet)

	outerJumpKey := bootstrapHostKey(t, outerJump)
	innerBastionKey := bootstrapHostKey(t, innerBastion)

	outerJumpHost, err := outerJump.Host(ctx)
	if err != nil {
		t.Fatalf("outer jump host: %v", err)
	}
	outerJumpMapped, err := outerJump.MappedPort(ctx, "2222/tcp")
	if err != nil {
		t.Fatalf("outer jump mapped port: %v", err)
	}
	outerJumpAddr := net.JoinHostPort(outerJumpHost, outerJumpMapped.Port())

	innerBastionHost, err := innerBastion.Host(ctx)
	if err != nil {
		t.Fatalf("inner bastion host: %v", err)
	}
	innerBastionMapped, err := innerBastion.MappedPort(ctx, "2222/tcp")
	if err != nil {
		t.Fatalf("inner bastion mapped port: %v", err)
	}
	innerBastionMappedAddr := net.JoinHostPort(innerBastionHost, innerBastionMapped.Port())

	// known_hosts carries BOTH the mapped-port addresses (bootstrap only)
	// AND "innerbastion:2222" — the alias-based address the real
	// hop-chain dial actually verifies against, since that dial happens
	// entirely inside the Docker network and never touches a mapped
	// port.
	knownHostsPath := writeMultiKnownHosts(t, map[string]ssh.PublicKey{
		outerJumpAddr:          outerJumpKey,
		innerBastionMappedAddr: innerBastionKey,
		"innerbastion:2222":    innerBastionKey,
	})

	runner := remoteexec.New(remoteexec.Options{KnownHostsPath: knownHostsPath, DialTimeout: 10 * time.Second, MaxRetries: 2})

	outerJumpHostPart, outerJumpPortPart, err := net.SplitHostPort(outerJumpAddr)
	if err != nil {
		t.Fatalf("split outer jump addr: %v", err)
	}
	outerJumpPortNum, err := strconv.Atoi(outerJumpPortPart)
	if err != nil {
		t.Fatalf("parse outer jump port: %v", err)
	}

	outerHop := remoteexec.Hop{Target: remoteexec.Target{Host: outerJumpHostPart, Port: outerJumpPortNum}, Auth: remoteexec.PasswordAuth(containerSSHUser, containerSSHPassword)}
	innerHop := remoteexec.Hop{Target: remoteexec.Target{Host: "innerbastion", Port: 2222}, Auth: remoteexec.PasswordAuth(containerSSHUser, containerSSHPassword)}
	fullRoute := []remoteexec.Hop{outerHop, innerHop}
	consoleTarget := remoteexec.Target{Host: "consoleserver", Port: 7000}

	leakOpts := goleak.IgnoreCurrent()

	t.Run("RFC2217ThroughBothBastionsReachesTheConsoleServer", func(t *testing.T) {
		conn, err := runner.DialThroughHops(ctx, fullRoute, consoleTarget)
		if err != nil {
			t.Fatalf("DialThroughHops through two real, isolated-network bastions: %v", err)
		}
		defer conn.Close()

		rfcCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		client, err := rfc2217.NewOverConn(rfcCtx, conn, rfc2217.Options{ReadTimeout: 5 * time.Second})
		if err != nil {
			t.Fatalf("negotiating RFC 2217 with the real console server, through both bastions: %v", err)
		}
		defer client.Close()

		line := serialline.Config{BaudRate: 19200, DataBits: 8, Parity: serialline.ParityNone, StopBits: serialline.StopBitsOne}
		if err := client.SetLine(rfcCtx, line); err != nil {
			t.Errorf("SetLine against the real ser2net, through both bastions: %v", err)
		}
		if err := client.SendBreak(rfcCtx, 100*time.Millisecond); err != nil {
			t.Errorf("SendBreak against the real ser2net, through both bastions: %v", err)
		}

		// DTR: a real, verified PTY limitation, not a bug -- see this
		// file's own doc comment. Tested here (reusing the already-open
		// tunneled session) rather than skipped, so the limitation is a
		// deliberate, running assertion instead of a silent gap.
		start := time.Now()
		dtrErr := client.AssertDTR(rfcCtx, true)
		elapsed := time.Since(start)
		if dtrErr == nil {
			t.Error("expected AssertDTR to time out against a PTY-backed line with no real modem control lines")
		}
		if elapsed > 8*time.Second {
			t.Errorf("AssertDTR took %v to fail, want it bounded by ReadTimeout", elapsed)
		}
		t.Logf("AssertDTR failed as expected after %v: %v", elapsed, dtrErr)
	})

	t.Run("DirectDialToConsoleServerFails", func(t *testing.T) {
		consoleIP, err := consoleServer.ContainerIP(ctx)
		if err != nil {
			t.Fatalf("console server container IP: %v", err)
		}
		conn, err := net.DialTimeout("tcp", net.JoinHostPort(consoleIP, "7000"), 3*time.Second)
		if err == nil {
			_ = conn.Close()
			t.Fatal("expected a direct dial to the console server (no hop chain at all) to fail: it is attached only to the management network, with no route from outside it")
		}
		t.Logf("direct dial failed as expected: %v", err)
	})

	t.Run("DialThroughOuterJumpHostAloneFails", func(t *testing.T) {
		conn, err := runner.DialThroughHops(ctx, []remoteexec.Hop{outerHop}, consoleTarget)
		if err == nil {
			_ = conn.Close()
			t.Fatal("expected dialing through the outer jump host ALONE (skipping the inner bastion) to fail: the outer jump host is attached only to the outer network and has no route to the management network at all")
		}
		t.Logf("outer-jump-host-alone dial failed as expected: %v", err)
	})

	goleak.VerifyNone(t, leakOpts)
}
