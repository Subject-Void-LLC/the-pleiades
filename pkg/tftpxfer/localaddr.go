// This file holds how the client picks the local address its socket binds,
// so that an IPv4 server's replies always reach it.
//
// github.com/pin/tftp/v3 opens its client socket with net.ListenUDP("udp")
// on the wildcard address, which Go makes a dual-stack IPv6 socket. On macOS
// (XNU) the kernel can hand that socket an ephemeral port another socket
// already holds on 127.0.0.1 or another IPv4 address, with no error, and the
// IPv4 socket bound to the specific address then receives every reply sent
// to that port. Measured on macOS 27: of 2,000 dual-stack sockets opened
// beside 2,000 IPv4 loopback ones, 253 shared a port, and every datagram sent
// to 127.0.0.1 on a shared port reached the IPv4 socket. Linux refuses the
// shared port, so it never happens there. The transfer then times out after
// every retry, which is how the package's tests failed on macOS runners.
//
// Binding the client to the IPv4 address it will send from makes the socket
// an IPv4 one, whose port the kernel checks against every other IPv4 socket.
// An IPv6 server keeps the library's own wildcard socket: the kernel checks
// an IPv6 port against every other IPv6 socket, so the shared port cannot
// happen for IPv6 replies.
package tftpxfer

import (
	"fmt"
	"net"
)

// resolveServer turns host and port into the one UDP address every datagram
// of a transfer goes to. Resolving once here, rather than letting the library
// resolve the name again, means the local address chosen for the socket and
// the server address the library sends to can never be different families.
func resolveServer(host string, port int) (*net.UDPAddr, error) {
	addr := net.JoinHostPort(host, fmt.Sprint(port))
	raddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, fmt.Errorf("tftpxfer: resolving %s: %w", addr, err)
	}
	return raddr, nil
}

// localIPFor returns the local IPv4 address the kernel would send to an IPv4
// server from, for the client socket to bind, or "" for an IPv6 server,
// whose wildcard socket is safe as it is. A connected UDP socket sends
// nothing: connecting it only asks the kernel which route, and so which
// source address, it would use.
func localIPFor(raddr *net.UDPAddr) (string, error) {
	if raddr.IP.To4() == nil {
		// An IPv6 server: its replies are IPv6 datagrams, and an IPv6
		// socket's port is checked against every other IPv6 socket.
		return "", nil
	}
	probe, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: raddr.IP.To4(), Port: raddr.Port})
	if err != nil {
		return "", fmt.Errorf("tftpxfer: choosing a local address for %s: %w", raddr, err)
	}
	defer func() { _ = probe.Close() }()
	return probe.LocalAddr().(*net.UDPAddr).IP.String(), nil
}
