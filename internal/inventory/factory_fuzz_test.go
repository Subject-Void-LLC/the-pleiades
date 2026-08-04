package inventory_test

import (
	"encoding/json"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/inventory"
	"github.com/SubjectVoidLLC/the-pleiades/internal/inventory/record"
	"github.com/SubjectVoidLLC/the-pleiades/pkg/capability"
)

// FuzzFactoryParser ensures that malformed or malicious property payloads
// cannot cause the factory to panic during hydration. Building a Record
// directly (rather than an *ent.Device) means this fuzz target no longer
// depends on the ORM at all to exercise the factory.
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

		deviceType, _ := props["type"].(string)
		rec := record.Record{
			Name:       "fuzz-device",
			Type:       deviceType,
			Properties: props,
		}

		// The factory might return an error if the type is missing or invalid,
		// but it should NEVER panic.
		item, _ := factory.Build(rec)

		// If it succeeded, try calling interface methods to ensure type safety bounds don't panic
		if item != nil {
			_ = item.ID()
			_ = item.HasCapability("any")

			if cisco, ok := item.(capability.CiscoIOSCapable); ok {
				_ = cisco.IOSVersion()
			}
			if ssh, ok := item.(capability.SSHTransportCapable); ok {
				_ = ssh.SSHHost()
				_ = ssh.SSHPort()
			}
		}
	})
}
