// A published port Docker Desktop lost to a socket of this machine's own.
//
// Docker Desktop's engine runs in a network namespace of its own and picks a
// container's host port from 32768 up without seeing the sockets of the
// namespace the tests run in. Its forwarder then binds that port in the
// tests' namespace, and when one of the tests' own sockets already holds it
// (an outgoing connection's local port, or an in-process server on :0, both
// drawn from the kernel's ephemeral range) the bind fails with "address
// already in use". Docker Desktop logs that as a warning ("failed to expose
// port") and never retries, while docker inspect still reports the port as
// published, so the container's port refuses every connection for the life
// of the container (FAILURE_PATTERNS 470, 471 and 481, diagnosed 2026-11-06:
// reproduced eight times in eight, and gone with the ephemeral range moved
// below 32768). A native dockerd binds in the tests' own namespace and picks
// another port, so this never happens on CI.
//
// The tell is on this side: a refused dial to host port P while a socket of
// this user has P as its local port. A readiness wait that sees it fails at
// once with the cause and the fix, rather than after its two minutes.
package testsupport

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
)

// ErrPublishedPortLost marks a readiness failure whose published host port
// is held by a socket of this machine's own, so Docker Desktop's forwarder
// could not bind it and will not retry.
var ErrPublishedPortLost = errors.New("the published host port is held by a socket of this machine's own")

// portRangeFix is the command that moves this machine's ephemeral ports
// below the ones Docker Desktop publishes; tools/doctor prints the same one.
const portRangeFix = "echo 'net.ipv4.ip_local_port_range = 20000 32767' | sudo tee /etc/sysctl.d/60-pleiades-docker-ports.conf && sudo sysctl -p /etc/sysctl.d/60-pleiades-docker-ports.conf"

// hostProcNetTCP is where this process's network namespace lists its TCP
// sockets. A variable so a test can point it elsewhere.
var hostProcNetTCP = []string{"/proc/self/net/tcp", "/proc/self/net/tcp6"}

// tcpStates names the /proc/net/tcp state codes a holder can be in.
var tcpStates = map[string]string{
	"01": "ESTABLISHED", "02": "SYN_SENT", "03": "SYN_RECV", "04": "FIN_WAIT1", "05": "FIN_WAIT2",
	"06": "TIME_WAIT", "07": "CLOSE", "08": "CLOSE_WAIT", "09": "LAST_ACK", "0A": "LISTEN", "0B": "CLOSING",
}

// ownSocketsOn returns the states of the sockets in this process's network
// namespace whose local port is port and whose owner is this process's
// user, one entry per socket, sorted. It returns nil where /proc/net/tcp
// cannot be read (any platform but Linux), which is never a collision.
func ownSocketsOn(port int) []string {
	var text strings.Builder
	for _, path := range hostProcNetTCP {
		raw, err := os.ReadFile(path) // #nosec G304 -- fixed /proc paths
		if err != nil {
			continue
		}
		text.Write(raw)
		text.WriteByte('\n')
	}
	return socketsOwnedOn(text.String(), port, os.Getuid())
}

// socketsOwnedOn reads /proc/net/tcp-format text for the sockets whose local
// port is port and whose owner is uid. For root it leaves listeners out,
// since a forwarder that did bind the port (docker-proxy on a native engine)
// runs as root too, and a root test's own listener is the rare case not
// worth that confusion. A TIME_WAIT socket belongs to no user (uid 0), so it
// is never counted as a non-root user's, which is right: Docker Desktop's
// forwarder binds with SO_REUSEADDR, which TIME_WAIT does not block.
func socketsOwnedOn(text string, port, uid int) []string {
	suffix := fmt.Sprintf(":%04X", port)
	var states []string
	for _, line := range strings.Split(text, "\n") {
		f := strings.Fields(line)
		if len(f) < 8 || !strings.HasSuffix(f[1], suffix) {
			continue
		}
		owner, err := strconv.Atoi(f[7])
		if err != nil || owner != uid {
			continue
		}
		if uid == 0 && f[3] == "0A" {
			continue
		}
		state := tcpStates[f[3]]
		if state == "" {
			state = "state " + f[3]
		}
		states = append(states, state)
	}
	sort.Strings(states)
	return states
}

// lostPortError is the readiness failure for a published port a socket of
// this machine's own holds.
func lostPortError(containerPort string, hostPort int, holders []string) error {
	return fmt.Errorf("%w: port %s is published at host port %d, which refuses connections while %d socket(s) of this user hold it here (%s), "+
		"so Docker Desktop's forwarder could not bind it and will not retry; move this machine's ephemeral ports below Docker's: %s",
		ErrPublishedPortLost, containerPort, hostPort, len(holders), strings.Join(holders, ", "), portRangeFix)
}

// hostSideEvidence says, for a wait that gave up, whether a socket of this
// user holds the host port it was dialing.
func hostSideEvidence(hostPort int) string {
	holders := ownSocketsOn(hostPort)
	if len(holders) == 0 {
		return fmt.Sprintf("no socket of this user holds host port %d here", hostPort)
	}
	return fmt.Sprintf("%d socket(s) of this user hold host port %d here (%s): Docker Desktop's forwarder cannot bind it", len(holders), hostPort, strings.Join(holders, ", "))
}
