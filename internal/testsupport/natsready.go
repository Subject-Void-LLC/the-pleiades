// Confirming a started broker's client port answers from the host.
package testsupport

import (
	"fmt"
	"time"
)

// natsGreetingAttempt bounds one attempt at reading a broker's greeting.
const natsGreetingAttempt = 2 * time.Second

// waitForNATSGreeting dials addr until the server there sends the INFO
// line every NATS server opens a client connection with, or within has
// passed.
//
// StartNATS waits for the server's own "Server is ready" log line, which
// is said inside the container. The port the host dials is published
// separately, and on 2026-09-25 it refused every connection after that
// line was logged: for the ten seconds a client's first connection is
// allowed, three times, and in one run for the whole two minutes this
// waits (FAILURE_PATTERNS 353). This absorbs a short gap. More usefully, a
// port that never answers fails here, naming the port, rather than in the
// code under test as a first-connection timeout that reads like a defect.
func waitForNATSGreeting(addr string, within, attempt time.Duration) error {
	deadline := time.Now().Add(within)
	for {
		err := readNATSGreeting(addr, attempt)
		if err == nil {
			return nil
		}
		if !time.Now().Add(200 * time.Millisecond).Before(deadline) {
			return fmt.Errorf("nothing at %s sent a NATS greeting within %v: %w", addr, within, err)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// readNATSGreeting makes one connection to addr, under a deadline of
// timeout, and reads its first line, which must be a NATS greeting.
func readNATSGreeting(addr string, timeout time.Duration) error {
	return readGreeting(addr, "INFO ", "NATS", timeout)
}
