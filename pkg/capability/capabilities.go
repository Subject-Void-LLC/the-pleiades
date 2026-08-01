// Package capability defines the contracts for device execution.
package capability

import "context"

// CiscoIOSCapable defines the contract for devices running Cisco IOS.
// If an InventoryItem returns true for HasCapability("CiscoIOSCapable"),
// the Trigger Engine assumes it can safely cast the item to this interface.
type CiscoIOSCapable interface {
	// RunCommand executes a raw CLI command on the device and returns the output.
	RunCommand(ctx context.Context, cmd string) (string, error)

	// BackupConfig streams the running configuration to the specified destination.
	BackupConfig(ctx context.Context, destinationURI string) error
}

// LinuxCapable defines the contract for devices running a Linux OS.
type LinuxCapable interface {
	// ExecuteBash runs a bash script on the server.
	ExecuteBash(ctx context.Context, script string) (string, error)

	// RestartService restarts a systemd service.
	RestartService(ctx context.Context, serviceName string) error
}
