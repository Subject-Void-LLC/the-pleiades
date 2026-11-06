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
// below 32768). No refused published port has been recorded on CI, whose
// native dockerd binds the port where the tests run.
//
// The tell is on this side: a refused dial to this machine's loopback on
// port P while a socket of this user has P as its local port. A readiness
// wait that sees it fails at once with the cause and the fix, rather than
// after its two minutes.
//
// What this cannot see, it does not claim. /proc/net/tcp lists listening and
// connected sockets only, so a socket bound without listening (refusing.Addr
// makes one) holds the port without appearing; and another user's sockets
// are left out on purpose. Run as root, nothing is read at all: a forwarder
// that did bind the port, and the sockets it accepts, are root's too.
package testsupport

import (
	"errors"
	"fmt"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/dockerports"
)

// ErrPublishedPortLost marks a readiness failure whose published host port
// is held by a socket of this machine's own, so Docker Desktop's forwarder
// could not bind it and will not retry.
var ErrPublishedPortLost = errors.New("the published host port is held by a socket of this machine's own")

// hostProcNetTCP is where this process's network namespace lists its TCP
// sockets. A variable so a test can point it elsewhere.
var hostProcNetTCP = []string{"/proc/self/net/tcp", "/proc/self/net/tcp6"}

// tcpStates names the /proc/net/tcp state codes a holder can be in.
var tcpStates = map[string]string{
	"01": "ESTABLISHED", "02": "SYN_SENT", "03": "SYN_RECV", "04": "FIN_WAIT1", "05": "FIN_WAIT2",
	"07": "CLOSE", "08": "CLOSE_WAIT", "09": "LAST_ACK", "0A": "LISTEN", "0B": "CLOSING",
}

// ownSocketsOn returns the states of the sockets in this process's network
// namespace whose local port is port and whose owner is this process's
// user, one entry per socket, sorted. It returns nil where /proc/net/tcp
// cannot be read (any platform but Linux) and for root, which is never a
// diagnosis.
func ownSocketsOn(port int) []string {
	uid := os.Getuid()
	if uid == 0 {
		return nil
	}
	var text strings.Builder
	for _, path := range hostProcNetTCP {
		raw, err := os.ReadFile(path) // #nosec G304 -- fixed /proc paths
		if err != nil {
			continue
		}
		text.Write(raw)
		text.WriteByte('\n')
	}
	return socketsOwnedOn(text.String(), port, uid)
}

// socketsOwnedOn reads /proc/net/tcp-format text for the sockets whose local
// port is port and whose owner is uid, leaving TIME_WAIT out: Docker
// Desktop's forwarder binds with SO_REUSEADDR, which a TIME_WAIT socket
// does not block.
func socketsOwnedOn(text string, port, uid int) []string {
	suffix := fmt.Sprintf(":%04X", port)
	var states []string
	for _, line := range strings.Split(text, "\n") {
		f := strings.Fields(line)
		if len(f) < 8 || !strings.HasSuffix(f[1], suffix) || f[3] == "06" {
			continue
		}
		owner, err := strconv.Atoi(f[7])
		if err != nil || owner != uid {
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

// loopbackHost reports whether host names this machine's loopback, the only
// place a published port can be lost to a socket of this namespace's. A
// remote engine's port is on another machine, where a local socket on the
// same number is a coincidence.
func loopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// lostHostPort returns the readiness failure for what (a container port, or
// a server's name) published at host:port, when host is this machine's
// loopback and a socket of this user holds port; nil otherwise.
func lostHostPort(what, host string, port int) error {
	if !loopbackHost(host) {
		return nil
	}
	holders := ownSocketsOn(port)
	if len(holders) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %s is published at host port %d, which refuses connections while %d socket(s) of this user hold it here (%s), "+
		"so Docker Desktop's forwarder could not bind it and will not retry; move this machine's ephemeral ports below Docker's: %s",
		ErrPublishedPortLost, what, port, len(holders), strings.Join(holders, ", "), dockerports.RangeFix)
}

// hostSideEvidence says, for a wait that gave up, what /proc/net/tcp shows
// of this user's sockets on the host port it was dialing.
func hostSideEvidence(hostPort int) string {
	holders := ownSocketsOn(hostPort)
	if len(holders) == 0 {
		return fmt.Sprintf("/proc/net/tcp lists no listening or connected socket of this user on host port %d (a socket bound without listening, or another user's, would not show)", hostPort)
	}
	return fmt.Sprintf("%d socket(s) of this user hold host port %d here (%s): Docker Desktop's forwarder cannot bind it", len(holders), hostPort, strings.Join(holders, ", "))
}
