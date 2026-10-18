//go:build !darwin && !freebsd && !windows

// How many connections one listener holds before accepting them, where
// that is no limit a test meets.
package remoteexec

import "math"

// listenQueue is how many connections one listener here can hold before
// it accepts them: no limit a test meets, since a kernel that runs out of
// room drops the SYN and the client sends it again (see the darwin
// version for the one that resets instead).
func listenQueue() int { return math.MaxInt }
