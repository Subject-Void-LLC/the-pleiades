//go:build unix

// Binding a loopback port without listening on it, through the socket calls.
package refusing

import (
	"errors"
	"fmt"
	"syscall"
)

// errNoUnlistenedBind is never returned here: every Unix can bind a port
// without listening on it. It is declared on every platform so
// Addr reads the same everywhere.
var errNoUnlistenedBind = errors.New("this platform cannot bind a port without listening on it")

// bindUnlistened binds a TCP socket to 127.0.0.1 on a port the kernel
// picks and never listens on it, returning the port and the call that
// closes the socket. The net package cannot do this: its only way to bind
// a TCP port is Listen.
func bindUnlistened() (int, func() error, error) {
	fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_STREAM, 0)
	if err != nil {
		return 0, nil, fmt.Errorf("socket: %w", err)
	}
	release := func() error { return syscall.Close(fd) }
	if err := syscall.Bind(fd, &syscall.SockaddrInet4{Addr: [4]byte{127, 0, 0, 1}}); err != nil {
		_ = release()
		return 0, nil, fmt.Errorf("bind: %w", err)
	}
	sa, err := syscall.Getsockname(fd)
	if err != nil {
		_ = release()
		return 0, nil, fmt.Errorf("getsockname: %w", err)
	}
	in4, ok := sa.(*syscall.SockaddrInet4)
	if !ok {
		_ = release()
		return 0, nil, fmt.Errorf("getsockname answered %T, not an IPv4 address", sa)
	}
	return in4.Port, release, nil
}
