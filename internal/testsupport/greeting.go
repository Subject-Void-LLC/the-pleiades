// Readiness through the port a test will use: a server's first line, read
// from the host through the container's mapped port.
package testsupport

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"github.com/testcontainers/testcontainers-go/wait"
)

// greetingPoll is how long a greeting wait pauses between attempts.
const greetingPoll = 200 * time.Millisecond

// ForGreeting waits until a connection made from the host, through port's
// mapped host port, reads a first line starting with prefix: "SSH-" for an
// SSH server, "INFO " for NATS. protocol names it in a refusal.
//
// A log line says a server is up inside its container. It does not say
// Docker forwards the mapped port yet, and a test whose first call does
// not retry fails in that gap: NATS clients did (FAILURE_PATTERNS 353), and
// so did the Vault gate (408). A greeting read through the mapped port is
// the path the test will take. Use it after the log line in a wait.ForAll,
// so a server that exits at once still fails at the speed the log wait
// reports it.
//
// The step keeps ContainerStartupTimeout and reports it through Timeout,
// so a wait.ForAll leaves it alone rather than narrowing it to sixty
// seconds (readiness.go's second section). It stops at once when the
// container is no longer running.
func ForGreeting(port, prefix, protocol string) wait.Strategy {
	return &greeting{port: port, prefix: prefix, protocol: protocol, timeout: ContainerStartupTimeout}
}

// SSHGreeting waits for an SSH server's banner through port's mapped host
// port. See ForGreeting.
func SSHGreeting(port string) wait.Strategy {
	return ForGreeting(port, "SSH-", "SSH")
}

// greeting is ForGreeting's strategy.
type greeting struct {
	port, prefix, protocol string
	timeout                time.Duration
}

// Timeout reports the bound this step keeps.
func (g *greeting) Timeout() *time.Duration { return &g.timeout }

// WaitUntilReady dials until the greeting arrives, the container stops, or
// the step's bound passes.
func (g *greeting) WaitUntilReady(ctx context.Context, target wait.StrategyTarget) error {
	ctx, cancel := context.WithTimeout(ctx, g.timeout)
	defer cancel()
	for {
		err := g.attempt(ctx, target)
		if err == nil {
			return nil
		}
		if state, stateErr := target.State(ctx); stateErr == nil && !state.Running {
			return fmt.Errorf("the container stopped (exit %d) before port %s sent the %s greeting: %w", state.ExitCode, g.port, g.protocol, err)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("port %s sent no %s greeting within %v: %w", g.port, g.protocol, g.timeout, err)
		case <-time.After(greetingPoll):
		}
	}
}

// attempt makes one connection through the mapped port and reads its
// first line.
func (g *greeting) attempt(ctx context.Context, target wait.StrategyTarget) error {
	host, err := target.Host(ctx)
	if err != nil {
		return err
	}
	mapped, err := target.MappedPort(ctx, g.port)
	if err != nil {
		return err
	}
	return readGreeting(net.JoinHostPort(host, mapped.Port()), g.prefix, g.protocol, natsGreetingAttempt)
}

// readGreeting makes one connection to addr, under a deadline of timeout,
// and reads its first line, which must start with prefix.
func readGreeting(addr, prefix, protocol string, timeout time.Duration) error {
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return err
	}
	line, err := bufio.NewReader(io.LimitReader(conn, 64<<10)).ReadString('\n')
	if err != nil {
		return err
	}
	if !strings.HasPrefix(line, prefix) {
		return fmt.Errorf("the server there opened with %q, not the %s greeting", strings.TrimSpace(line[:min(len(line), 40)]), protocol)
	}
	return nil
}
