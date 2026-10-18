//go:build windows

// How many connections one listener holds on Windows before accepting them.
package remoteexec

// listenQueue is how many connections one listener here can be sure of
// holding before it accepts them. Go asks Windows for SOMAXCONN, which
// Windows reads as a reasonable maximum of its own choosing, and like XNU
// it refuses a connection past it rather than dropping the SYN for the
// client to send again. Measured on Windows Server 2025 (the lab's
// win-check, one CPU): 99 of 300 concurrent hop sessions were refused, so
// it held about 200; 128 is the macOS figure and leaves room under that.
func listenQueue() int { return 128 }
