// The addresses whose known_hosts lines trust a device: its SSH address and,
// for a device with a NETCONF listener, that listener's. remove-host
// --forget-host-keys forgets both, since trust-host can trust either.
package main

import (
	"net"
	"strconv"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// defaultNetconfListenerPort is RFC 6242's NETCONF over SSH port, which a
// device's NetconfPort reports when it names none.
const defaultNetconfListenerPort = 830

// netconfListenerAddress returns the host:port a device's NETCONF listener
// answers at, and false for a device with none. A NETCONF server is an SSH
// server of its own, whose keys known_hosts records under "[host]:port".
//
// The check is structural, as pkg/netconf's sessions and trust-host's own
// NETCONF address are: a cisco_router implements NetconfPort whether or not
// netconf_enabled is set, and a generic_netconf's NetconfCapable is granted
// only by onboarding, which itself needs the listener's key trusted first.
// Asking HasCapability would leave behind exactly the keys trusted before
// onboarding, so any device that can name a listener counts.
func netconfListenerAddress(device inventory.InventoryItem) (string, bool) {
	nc, ok := device.(capability.NetconfCapable)
	if !ok {
		return "", false
	}
	ssh, ok := device.(capability.SSHTransportCapable)
	if !ok || ssh.SSHHost() == "" {
		return "", false
	}
	port := nc.NetconfPort()
	if port == 0 {
		port = defaultNetconfListenerPort
	}
	return net.JoinHostPort(ssh.SSHHost(), strconv.Itoa(port)), true
}

// trustedAddresses returns every address whose known_hosts lines trust the
// device, SSH's first, each once: a generic_netconf's SSH port is its NETCONF
// one unless set apart. It fails as sshAddress does for a device not reached
// over SSH at an address.
func trustedAddresses(device inventory.InventoryItem) ([]string, error) {
	address, err := sshAddress(device)
	if err != nil {
		return nil, err
	}
	addresses := []string{address}
	if listener, ok := netconfListenerAddress(device); ok && listener != address {
		addresses = append(addresses, listener)
	}
	return addresses, nil
}
