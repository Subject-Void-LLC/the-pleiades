//go:build darwin

// How many connections one listener holds on macOS before accepting them.
package remoteexec

import "golang.org/x/sys/unix"

// listenQueue is how many connections one listener here can be sure of
// holding before it accepts them: the backlog Go asks for,
// kern.ipc.somaxconn (128 by default). XNU resets the connections past
// what its queue holds, where Linux drops a SYN it has no room for and
// the client sends it again. Measured on a MacBook Air (M4): 128
// concurrent hop sessions passed ten runs of ten alone and three of three
// with the whole package at -count=3; 192 (half again the backlog, where
// XNU's queue is said to end) failed two of ten alone and three of three
// with the package; 300 failed every run.
func listenQueue() int {
	n, err := unix.SysctlUint32("kern.ipc.somaxconn")
	if err != nil || n == 0 {
		n = 128
	}
	return int(n)
}
