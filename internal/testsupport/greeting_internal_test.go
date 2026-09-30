// Tests for ForGreeting: ready once the greeting arrives through the
// mapped port, patient with a port that drops connections, and quick to
// give up on a container that stopped.
package testsupport

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/testcontainers/testcontainers-go/wait"
)

// fakeTarget is a container whose mapped port is addr and whose state is
// running or stopped with exit.
type fakeTarget struct {
	wait.StrategyTarget
	addr    string
	running bool
	exit    int
}

func (f fakeTarget) Host(context.Context) (string, error) {
	host, _, err := net.SplitHostPort(f.addr)
	return host, err
}

func (f fakeTarget) MappedPort(context.Context, string) (network.Port, error) {
	_, port, err := net.SplitHostPort(f.addr)
	if err != nil {
		return network.Port{}, err
	}
	return network.ParsePort(port + "/tcp")
}

func (f fakeTarget) State(context.Context) (*container.State, error) {
	return &container.State{Running: f.running, ExitCode: f.exit}, nil
}

// TestForGreeting_KeepsTheAgreedBound proves the step reports
// ContainerStartupTimeout, so a wait.ForAll does not narrow it.
func TestForGreeting_KeepsTheAgreedBound(t *testing.T) {
	st, ok := SSHGreeting("2222/tcp").(wait.StrategyTimeout)
	if !ok || st.Timeout() == nil || *st.Timeout() != ContainerStartupTimeout {
		t.Fatal("SSHGreeting does not report ContainerStartupTimeout as its bound")
	}
}

// TestForGreeting covers each answer a mapped port can give.
func TestForGreeting(t *testing.T) {
	const banner = "SSH-2.0-OpenSSH_9.6\r\n"
	tests := []struct {
		name    string
		target  fakeTarget
		wantErr string
	}{
		{"the banner at once", fakeTarget{addr: greetingServer(t, 0, banner), running: true}, ""},
		{"after the port drops three connections", fakeTarget{addr: greetingServer(t, 3, banner), running: true}, ""},
		{"another protocol", fakeTarget{addr: greetingServer(t, 0, "INFO {}\r\n"), running: true}, "not the SSH greeting"},
		{"a container that stopped", fakeTarget{addr: greetingServer(t, 1000, banner), running: false, exit: 1}, "stopped (exit 1)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := &greeting{port: "2222/tcp", prefix: "SSH-", protocol: "SSH", timeout: 3 * time.Second}
			start := time.Now()
			err := g.WaitUntilReady(context.Background(), tt.target)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("WaitUntilReady: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want it to say %q", err, tt.wantErr)
			}
			if tt.target.running == false && time.Since(start) > time.Second {
				t.Errorf("a stopped container took %v to be reported, want at once", time.Since(start))
			}
		})
	}
}
