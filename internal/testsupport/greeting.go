// Readiness through the port a test will use: a server's first line, read
// from the host through the container's mapped port.
package testsupport

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"

	tcexec "github.com/testcontainers/testcontainers-go/exec"
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
			return fmt.Errorf("port %s sent no %s greeting within %v: %w (%s)", g.port, g.protocol, g.timeout, err, g.evidence(target))
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

// evidenceTimeout bounds the look a timed-out wait takes at the container.
const evidenceTimeout = 5 * time.Second

// evidence says what a wait that timed out could not: where Docker says it
// publishes the port, which host port the wait was dialing by then, and
// whether anything inside the container listens on the port at all. A
// refused mapped port has three different causes (no forwarding on the
// host, the wrong host port, no server inside), and the dial alone cannot
// tell them apart; this was missing when the openssh fixture's port was
// refused for two minutes while its log said sshd was listening.
func (g *greeting) evidence(target wait.StrategyTarget) string {
	ctx, cancel := context.WithTimeout(context.Background(), evidenceTimeout)
	defer cancel()
	var parts []string
	if insp, err := target.Inspect(ctx); err != nil {
		parts = append(parts, "inspect failed: "+err.Error())
	} else if insp.NetworkSettings == nil {
		parts = append(parts, "Docker reports no network settings")
	} else {
		var bound []string
		for p, bindings := range insp.NetworkSettings.Ports {
			if p.String() != g.port {
				continue
			}
			for _, b := range bindings {
				bound = append(bound, net.JoinHostPort(b.HostIP.String(), b.HostPort))
			}
		}
		parts = append(parts, fmt.Sprintf("Docker publishes it at [%s]", strings.Join(bound, " ")))
	}
	if mapped, err := target.MappedPort(ctx, g.port); err == nil {
		parts = append(parts, "the mapped port is now "+mapped.Port())
	}
	parts = append(parts, insideListener(ctx, target, g.port))
	return strings.Join(parts, "; ")
}

// insideListener reports whether a socket inside the container listens on
// port, from /proc/net/tcp and tcp6, which every Linux container has and
// which needs no tool beyond cat.
func insideListener(ctx context.Context, target wait.StrategyTarget, port string) string {
	number, _, _ := strings.Cut(port, "/")
	want, err := strconv.Atoi(number)
	if err != nil {
		return "port " + port + " is not a number"
	}
	code, out, err := target.Exec(ctx, []string{"cat", "/proc/net/tcp", "/proc/net/tcp6"}, tcexec.Multiplexed())
	if err != nil || code != 0 {
		return fmt.Sprintf("could not read the container's sockets (exit %d, %v)", code, err)
	}
	raw, err := io.ReadAll(io.LimitReader(out, 1<<20))
	if err != nil {
		return "could not read the container's sockets: " + err.Error()
	}
	if listensOn(string(raw), want) {
		return fmt.Sprintf("something inside listens on %d", want)
	}
	return fmt.Sprintf("nothing inside listens on %d", want)
}

// listensOn reports whether /proc/net/tcp-format text has a socket in the
// LISTEN state (0A) on port.
func listensOn(procNetTCP string, port int) bool {
	suffix := fmt.Sprintf(":%04X", port)
	for _, line := range strings.Split(procNetTCP, "\n") {
		fields := strings.Fields(line)
		if len(fields) > 3 && strings.HasSuffix(fields[1], suffix) && fields[3] == "0A" {
			return true
		}
	}
	return false
}
