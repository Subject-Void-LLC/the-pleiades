// Package dockerports holds what this repository knows about the host ports
// Docker Desktop publishes, for the two places that act on it: the test
// harness's readiness waits (internal/testsupport), which name the fix when
// a container's port was lost, and make doctor (tools/doctor), which checks
// for the overlap that loses one. One statement, so the command a failing
// wait prints and the one doctor prints cannot drift apart.
//
// Docker Desktop's engine runs in a network namespace of its own and picks a
// container's host port from DesktopLowest up (measured 2026-11-06 on
// Docker Desktop 29.8.0 under WSL2: 33274 to 65381 over about a hundred
// allocations) without seeing the sockets of the namespace the tests run in.
// Its forwarder then binds the port there, and when a socket the tests
// already hold has it, the bind fails and is never retried (FAILURE_PATTERNS
// 470). Moving this machine's ephemeral ports below DesktopLowest keeps the
// tests' own sockets out of the ports Docker Desktop can choose.
package dockerports

// DesktopLowest is the lowest host port Docker Desktop publishes a
// container's port on.
const DesktopLowest = 32768

// RangeFix is the command that moves this machine's ephemeral ports below
// DesktopLowest, persistently, through systemd-sysctl.
const RangeFix = "echo 'net.ipv4.ip_local_port_range = 20000 32767' | sudo tee /etc/sysctl.d/60-pleiades-docker-ports.conf && sudo sysctl -p /etc/sysctl.d/60-pleiades-docker-ports.conf"
