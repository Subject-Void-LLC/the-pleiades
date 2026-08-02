package inventory_test

import (
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/ent"
	"github.com/SubjectVoidLLC/the-pleiades/internal/inventory"
)

func BenchmarkFactoryHydration(b *testing.B) {
	factory := inventory.NewItemFactory()

	dev := &ent.Device{
		Name: "core-router-1",
		Properties: map[string]interface{}{
			"type":            "cisco_router",
			"host":            "10.0.0.1",
			"port":            22,
			"ios_version":     "17.3.2",
			"netconf_enabled": true,
		},
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := factory.Build(dev)
		if err != nil {
			b.Fatalf("failed to build: %v", err)
		}
	}
}
