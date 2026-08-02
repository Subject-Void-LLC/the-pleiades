package inventory_test

import (
	"encoding/json"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/ent"
	"github.com/SubjectVoidLLC/the-pleiades/internal/inventory"
)

// FuzzFactoryParser ensures that malformed or malicious JSON properties
// cannot cause the factory to panic during hydration.
func FuzzFactoryParser(f *testing.F) {
	f.Add(`{"type": "cisco_router", "port": "not-an-int"}`)
	f.Add(`{"type": "linux_server", "host": 12345}`)
	f.Add(`{"type": "unknown_type"}`)
	f.Add(`{"not_a_type": "true"}`)

	factory := inventory.NewItemFactory()

	f.Fuzz(func(t *testing.T, payload string) {
		var props map[string]interface{}
		if err := json.Unmarshal([]byte(payload), &props); err != nil {
			// Invalid json, skip
			return
		}

		dev := &ent.Device{
			Name:       "fuzz-device",
			Properties: props,
		}

		// The factory might return an error if the type is missing or invalid,
		// but it should NEVER panic.
		item, _ := factory.Build(dev)

		// If it succeeded, try calling interface methods to ensure type safety bounds don't panic
		if item != nil {
			_ = item.ID()
			_ = item.HasCapability("any")

			if cisco, ok := item.(inventory.CiscoIOSCapable); ok {
				_ = cisco.IOSVersion()
			}
			if ssh, ok := item.(inventory.SSHTransportCapable); ok {
				_ = ssh.SSHHost()
				_ = ssh.SSHPort()
			}
		}
	})
}
