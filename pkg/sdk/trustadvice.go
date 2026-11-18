// Package sdk: the command that trusts a device's host key, named on the
// refusal of one.
package sdk

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
)

// EphemeralProperty is the device property that says a device is made
// again (a lab node) and so has new host keys on every deploy, which lets
// trust-host --first-connect run without a reason.
const EphemeralProperty = "ephemeral"

// IsEphemeral reports whether device is flagged ephemeral: the property is
// true, as a bool or as the word an operator typed.
func IsEphemeral(device inventory.InventoryItem) bool {
	if device == nil {
		return false
	}
	switch v := device.Properties().Raw()[EphemeralProperty].(type) {
	case bool:
		return v
	case string:
		return strings.EqualFold(v, "true")
	}
	return false
}

// WithTrustAdvice returns err with the trust-host command that would accept
// the key it refused, when err is a host key refusal for device's SSH
// listener or its NETCONF one dialed at port (remoteexec.HostKeyError). The
// key stays refused: the advice says to check the presented fingerprint
// against the device itself first, since a machine in the middle presents a
// changed key too.
//
// A changed key is advised with --replace, which drops the old one. An unknown
// key on a NETCONF listener is left to the NETCONF callers, which already say
// how to trust it; a revoked key, an error that is not a host key refusal, and
// a port trust-host cannot trust come back unchanged.
func WithTrustAdvice(err error, device inventory.InventoryItem, port int) error {
	var hostErr *remoteexec.HostKeyError
	if device == nil || !errors.As(err, &hostErr) || hostErr.Revoked {
		return err
	}
	sshPort := 0
	if dev, ok := device.(capability.SSHTransportCapable); ok {
		sshPort = dev.SSHPort()
	}
	netconf := false
	if nc, ok := device.(capability.NetconfCapable); ok && port != sshPort {
		listener := nc.NetconfPort()
		if listener == 0 {
			listener = 830
		}
		netconf = listener == port
	}
	switch {
	case port != sshPort && !netconf:
		return err
	case netconf && !hostErr.Changed():
		return err
	}

	command := []string{"pleiades trust-host", device.Name(), "--first-connect"}
	if netconf {
		command = append(command, "--netconf")
	}
	if hostErr.Changed() {
		command = append(command, "--replace")
	}
	// A device not flagged ephemeral needs a reason, which trust-host records.
	if !IsEphemeral(device) {
		command = append(command, `--reason "<why this key is trusted>"`)
	}
	fingerprint := remoteexec.Fingerprint(hostErr.Presented)
	if hostErr.Changed() {
		return fmt.Errorf("%w; if the device made a new key (a node built again, or one that makes a new key at every boot, as a Catalyst 8000V on IOS XE 17.15 does on port 22), check %s against the device itself, then replace the old key with: %s",
			err, fingerprint, strings.Join(command, " "))
	}
	return fmt.Errorf("%w; check %s against the device itself, then trust it with: %s", err, fingerprint, strings.Join(command, " "))
}
