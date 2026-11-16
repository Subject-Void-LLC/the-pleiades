// Package testsupport: Nokia SR Linux in a container, the real gNMI target
// pkg/gnmi's live tests and the CLI's gNMI release gate run against, with
// gNOI added to its gRPC server's services, which its factory configuration
// leaves out.
package testsupport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/testcontainers/testcontainers-go"
	tcexec "github.com/testcontainers/testcontainers-go/exec"
	"github.com/testcontainers/testcontainers-go/wait"
)

// SRLinuxImage is Nokia SR Linux 26.7.2, published on GitHub Container
// Registry, pinned by its tag's multi-arch index digest.
const SRLinuxImage = "ghcr.io/nokia/srlinux@sha256:0096fe3ebcafabb7253492e2060425fe027a168e0e066766d1e85efbb0b48be8"

// SR Linux's factory login, a throwaway container's, never a device's.
const (
	SRLinuxUser     = "admin"
	SRLinuxPassword = "NokiaSrl1!"
)

// srlinuxGNMIPort is the port SR Linux's factory gRPC server (mgmt) serves
// gNMI on, over TLS with its own self-signed certificate.
const srlinuxGNMIPort = "57400/tcp"

// SRLinux is one started SR Linux container.
type SRLinux struct {
	// Container is the container, for its owner to terminate.
	Container testcontainers.Container
	// Target is its gNMI server's host:port, reached from this machine.
	Target string
}

// RunSRLinux starts SR Linux, waits for its management server, and adds
// gNOI to its gRPC server's services. Its gNMI server can take a few more
// seconds to answer: a caller waits for it the way a runbook does. A start
// that fails terminates its own container; once started, the caller
// terminates it, even when an error is returned.
func RunSRLinux(ctx context.Context) (*SRLinux, error) {
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        SRLinuxImage,
			Cmd:          []string{"sudo", "bash", "/opt/srlinux/bin/sr_linux"},
			ExposedPorts: []string{srlinuxGNMIPort},
			// SR Linux runs its applications and data plane in the
			// container's own network namespaces, which needs a privileged
			// container (the owner allowed it, 2026-11-15, for SR Linux and
			// Stratum alone).
			HostConfigModifier: func(hc *container.HostConfig) { hc.Privileged = true },
			ConfigModifier: func(cfg *container.Config) {
				cfg.Tty = true
				cfg.Hostname = "srl"
			},
			WaitingFor: wait.ForLog("Application mgmt_server is running").WithStartupTimeout(6 * time.Minute),
		},
		Started: true,
	})
	if err != nil {
		// A failed start still returns its container.
		_ = testcontainers.TerminateContainer(c)
		return &SRLinux{}, fmt.Errorf("starting %s: %w", SRLinuxImage, err)
	}
	s := &SRLinux{Container: c}
	host, err := c.Host(ctx)
	if err != nil {
		return s, err
	}
	mapped, err := c.MappedPort(ctx, srlinuxGNMIPort)
	if err != nil {
		return s, err
	}
	s.Target = net.JoinHostPort(host, strconv.Itoa(int(mapped.Num())))
	// The command line can lag the management server by a moment.
	var last error
	for try := 0; try < 30; try++ {
		if _, last = s.CLI(ctx, "set / system grpc-server mgmt services [ gnmi gnsi gnoi ]", true); last == nil {
			return s, nil
		}
		select {
		case <-ctx.Done():
			return s, errors.Join(ctx.Err(), last)
		case <-time.After(2 * time.Second):
		}
	}
	return s, fmt.Errorf("adding gNOI to SR Linux's gRPC server: %w", last)
}

// StartSRLinux is RunSRLinux for one test, skipped in -short mode and
// terminated when t ends.
func StartSRLinux(t testing.TB) *SRLinux {
	t.Helper()
	if testing.Short() {
		t.Skip("starts a privileged SR Linux container")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	s, err := RunSRLinux(ctx)
	if s.Container != nil {
		t.Cleanup(func() { _ = testcontainers.TerminateContainer(s.Container) })
	}
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// srlinuxRejects finds the count of refused logins in the gRPC server's
// statistics, the server-wide one, which sr_cli writes first.
var srlinuxRejects = regexp.MustCompile(`access-rejects\s+(\d+)`)

// LoginRejects is how many logins SR Linux's mgmt gRPC server has refused,
// read from its own state over sr_cli, never through gNMI: zero before the
// first, when SR Linux writes no statistics at all.
func (s *SRLinux) LoginRejects(ctx context.Context) (int, error) {
	out, err := s.CLI(ctx, "info from state /system grpc-server mgmt statistics", false)
	if err != nil {
		return 0, err
	}
	return loginRejects(out)
}

// loginRejects reads the server-wide count of refused logins from sr_cli's
// statistics, zero when it wrote none.
func loginRejects(statistics string) (int, error) {
	m := srlinuxRejects.FindStringSubmatch(statistics)
	if m == nil {
		return 0, nil
	}
	return strconv.Atoi(m[1])
}

// CLI runs one command on SR Linux's own command line (sr_cli) as root,
// independent of gNMI, and returns its output: with commit, the command is
// a configuration change committed at once (sr_cli -ec), and otherwise a
// read (sr_cli -d).
func (s *SRLinux) CLI(ctx context.Context, command string, commit bool) (string, error) {
	mode := "-d"
	if commit {
		mode = "-ec"
	}
	code, out, err := s.Container.Exec(ctx, []string{"sr_cli", mode, command}, tcexec.WithUser("root"), tcexec.Multiplexed())
	if err != nil {
		return "", err
	}
	raw, _ := io.ReadAll(out)
	if code != 0 {
		return string(raw), fmt.Errorf("sr_cli %q exited %d: %s", command, code, raw)
	}
	return string(raw), nil
}
