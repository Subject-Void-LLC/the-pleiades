package ssh

import (
	"context"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	toxiproxyclient "github.com/Shopify/toxiproxy/v2/client"
	"go.uber.org/goleak"
	"golang.org/x/crypto/ssh"

	"github.com/testcontainers/testcontainers-go"
	tctoxiproxy "github.com/testcontainers/testcontainers-go/modules/toxiproxy"
	"github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/internal/transport"
)

// TestSSHHopChain_SeveredBastionMidTunneledCommandSurfacesANamedError is
// this phase's Chaos Testing proof for the hop chain specifically
// (AGENTS.md's Bulletproof Testing Matrix: Toxiproxy for network
// boundaries in a Release Gate, to simulate TCP severing). It fronts a
// real, independent sshd with a real Toxiproxy proxy, dials through it
// as a bastion, tunnels a SECOND, genuinely independent handshake back
// to the same sshd's own internal loopback (the identical trick
// TestSSHContainer_HopChain_TunnelsThroughItself uses to prove the happy
// path with only one container), starts a long-running command over the
// TUNNELED connection, then severs the PROXY -- the bastion leg -- while
// that command is still in flight.
//
// Severing the bastion leg necessarily severs the tunneled channel too:
// the tunnel's bytes are multiplexed inside the same underlying TCP
// connection the proxy fronts, so there is no way to sever one without
// the other. What this test proves is what that severance produces:
// a real, bounded error naming the failure, never a hang and never a
// silent zero-value Result that would look like the command ran and
// produced nothing.
func TestSSHHopChain_SeveredBastionMidTunneledCommandSurfacesANamedError(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ctx := context.Background()

	nw, err := network.New(ctx)
	if err != nil {
		t.Fatalf("failed to create network: %v", err)
	}
	t.Cleanup(func() { nw.Remove(context.Background()) })

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
		// AllowTcpForwarding is off by default in this image (verified
		// directly against a throwaway container; see
		// ssh_container_test.go's own requireSSHContainer for the full
		// reasoning). This is the identical config-snippet mount that
		// unlocks it.
		Files: []testcontainers.ContainerFile{{
			Reader:            strings.NewReader("AllowTcpForwarding yes\n"),
			ContainerFilePath: "/config/sshd/sshd_config.d/allow-tcp-forwarding.conf",
			FileMode:          0o644,
		}},
		WaitingFor: wait.ForLog("done.").WithStartupTimeout(testsupport.SSHDStartupTimeout),
		Networks:   []string{nw.Name},
		NetworkAliases: map[string][]string{
			nw.Name: {"sshbastion"},
		},
	}
	sshContainer, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("failed to start sshd container: %v", err)
	}
	t.Cleanup(func() { sshContainer.Terminate(context.Background()) })

	sshHost, err := sshContainer.Host(ctx)
	if err != nil {
		t.Fatalf("failed to get container host: %v", err)
	}
	sshMapped, err := sshContainer.MappedPort(ctx, "2222/tcp")
	if err != nil {
		t.Fatalf("failed to get mapped port: %v", err)
	}
	realAddr := net.JoinHostPort(sshHost, sshMapped.Port())

	// The proxy's upstream is "sshbastion:2222" (the container's network
	// alias and its own internal sshd port), reachable from the
	// toxiproxy container because both share nw.
	toxiproxyContainer, err := tctoxiproxy.Run(ctx,
		"ghcr.io/shopify/toxiproxy:2.12.0",
		tctoxiproxy.WithProxy("sshbastion", "sshbastion:2222"),
		network.WithNetwork([]string{"toxiproxy"}, nw),
	)
	if err != nil {
		t.Fatalf("failed to start toxiproxy container: %v", err)
	}
	t.Cleanup(func() { toxiproxyContainer.Terminate(context.Background()) })

	proxiedHost, proxiedPort, err := toxiproxyContainer.ProxiedEndpoint(8666)
	if err != nil {
		t.Fatalf("failed to get proxied endpoint: %v", err)
	}
	proxiedAddr := net.JoinHostPort(proxiedHost, proxiedPort)

	toxiURI, err := toxiproxyContainer.URI(ctx)
	if err != nil {
		t.Fatalf("failed to get toxiproxy control URI: %v", err)
	}
	toxiClient := toxiproxyclient.NewClient(toxiURI)
	proxies, err := toxiClient.Proxies()
	if err != nil {
		t.Fatalf("failed to list proxies: %v", err)
	}
	proxy, ok := proxies["sshbastion"]
	if !ok {
		t.Fatal("toxiproxy has no \"sshbastion\" proxy registered")
	}

	// Bootstrap dial straight to the container's own real, mapped port
	// (bypassing the proxy, which is only under test for the client's
	// OWN connection below) to capture its real host key: the same
	// recording-callback pattern TestSSHContainer_HostKeyVerification
	// already uses.
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
	bootstrapClient, err := ssh.Dial("tcp", realAddr, recordingConfig)
	if err != nil {
		t.Fatalf("bootstrap dial to capture the real host key failed: %v", err)
	}
	bootstrapClient.Close()
	if capturedKey == nil {
		t.Fatal("expected to capture a real host key from the container")
	}

	// Both addresses the two independent handshakes present the SAME
	// real key under: the PROXIED address for the bastion leg (the
	// client dials the proxy, never the container directly), and the
	// internal loopback for the tunneled leg.
	knownHostsPath := writeMultiKnownHosts(t, map[string]ssh.PublicKey{
		proxiedAddr:      capturedKey,
		"127.0.0.1:2222": capturedKey,
	})

	tr := New(Options{KnownHostsPath: knownHostsPath, DialTimeout: 5 * time.Second, MaxRetries: 1})

	bastionHost, bastionPortStr, err := net.SplitHostPort(proxiedAddr)
	if err != nil {
		t.Fatalf("split proxied addr: %v", err)
	}
	bastionPort, err := strconv.Atoi(bastionPortStr)
	if err != nil {
		t.Fatalf("parse proxied port: %v", err)
	}

	target := transport.Target{
		Endpoint: transport.NetworkEndpoint{Host: "127.0.0.1", Port: 2222},
		Route: []transport.Hop{{
			Host:       bastionHost,
			Port:       bastionPort,
			DeviceName: "sshbastion",
			Credential: containerCred(),
		}},
	}

	// 1. Baseline: the hop chain works through the healthy proxy.
	if _, err := tr.Exec(ctx, target, containerCred(), "echo baseline-ok"); err != nil {
		t.Fatalf("baseline hop-chain Exec through the healthy proxy failed: %v", err)
	}

	// The goleak baseline is taken HERE, after every container, network
	// and proxy is up and the baseline connection has already completed
	// a full dial-and-close cycle, rather than reusing the package's
	// shared sharedContainerGoleak snapshot (populated by
	// requireSSHContainer, which this test never calls). Depending on
	// that shared snapshot would make this test's own leak check depend
	// on test EXECUTION ORDER within the binary: run this test alone
	// (go test -run TestSSHHopChain) and sharedContainerGoleak is still
	// nil, so goleak.VerifyNone checks against no baseline at all and
	// flags testcontainers-go's own background HTTP transport goroutines
	// (its Docker-daemon and toxiproxy-control-API clients keep idle
	// connections' read loops running) as if this test's own SSH code
	// leaked them. A local snapshot, taken after setup finishes, is what
	// makes this test's leak check self-contained and correct regardless
	// of what else has or has not run in the same binary.
	leakOpts := goleak.IgnoreCurrent()
	defer func() { goleak.VerifyNone(t, leakOpts) }()

	// 2. Sever the bastion leg WHILE a command is in flight over the
	// tunneled connection. sleep 10 gives the severance a real window to
	// land mid-command rather than racing a command that already
	// finished.
	severed := make(chan struct{})
	go func() {
		defer close(severed)
		time.Sleep(300 * time.Millisecond)
		if err := proxy.Disable(); err != nil {
			t.Errorf("failed to disable proxy: %v", err)
		}
	}()

	start := time.Now()
	result, err := tr.Exec(ctx, target, containerCred(), "sleep 10 && echo should-never-print")
	elapsed := time.Since(start)
	<-severed

	if err == nil {
		t.Fatalf("expected the severed bastion to surface a real error, got a clean result: %+v", result)
	}
	if result.Stdout != "" || result.Stderr != "" || result.ExitCode != 0 {
		t.Errorf("result = %+v, want the zero value: an error return means Result is meaningless (transport.Transport's own documented contract)", result)
	}
	if elapsed > 15*time.Second {
		t.Errorf("severed command took %v to fail, want it bounded well under the 10s sleep plus a short margin", elapsed)
	}
	t.Logf("severed hop-chain command failed after %v: %v", elapsed, err)

	// 3. Heal the network and prove a FRESH call succeeds. This is
	// deliberately a new Exec, not a retry of the interrupted one: a
	// command already sent to the remote side is never retried
	// (internal/transport/ssh's own doc comment), so recovery here means
	// the mechanism (breaker, dial) is healthy again, not that the
	// severed command's own side effect is somehow recovered.
	if err := proxy.Enable(); err != nil {
		t.Fatalf("failed to re-enable proxy: %v", err)
	}

	deadline := time.Now().Add(20 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		if _, err := tr.Exec(ctx, target, containerCred(), "echo recovered-ok"); err == nil {
			lastErr = nil
			break
		} else {
			lastErr = err
			time.Sleep(500 * time.Millisecond)
		}
	}
	if lastErr != nil {
		t.Fatalf("hop-chain Exec never recovered after the network healed: %v", lastErr)
	}
}
