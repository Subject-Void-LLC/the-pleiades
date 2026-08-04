package inventory_test

import (
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/inventory"
	"github.com/SubjectVoidLLC/the-pleiades/internal/inventory/record"
)

func BenchmarkFactoryHydration(b *testing.B) {
	factory := inventory.NewItemFactory()

	rec := record.Record{
		Name: "core-router-1",
		Type: "cisco_router",
		Properties: map[string]interface{}{
			"host":            "10.0.0.1",
			"port":            22,
			"ios_version":     "17.3.2",
			"netconf_enabled": true,
		},
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := factory.Build(rec)
		if err != nil {
			b.Fatalf("failed to build: %v", err)
		}
	}
}
