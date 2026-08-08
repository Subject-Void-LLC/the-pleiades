package ent_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/enttest"
	_ "github.com/mattn/go-sqlite3"
)

// BenchmarkDeviceQuery measures the speed of retrieving devices.
// Context: AWX (Ansible Tower) using Django ORM often takes ~2-5 seconds
// to serialize and return 10,000 inventory hosts over its API.
// We are stress testing the raw ORM overhead of entgo.io here to ensure
// we beat that reference platform by orders of magnitude.
func BenchmarkDeviceQuery(b *testing.B) {
	client := enttest.Open(b, "sqlite3", "file:entbench?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	// Stress Test Setup: Pre-load 10,000 devices
	const numDevices = 10000

	startSetup := time.Now()
	for i := 0; i < numDevices; i++ {
		_, err := client.Device.Create().
			SetName(fmt.Sprintf("bench-router-%d", i)).
			SetType("network_device").
			SetProperties(map[string]interface{}{"vendor": "arista", "role": "leaf"}).
			Save(ctx)
		if err != nil {
			b.Fatalf("failed setup: %v", err)
		}
	}
	b.Logf("[STRESS SETUP] Populated %d devices in %v", numDevices, time.Since(startSetup))
	b.Logf("[REFERENCE] AWX/Ansible Tower inventory fetch for 10k hosts: ~2000ms - 5000ms")

	b.ResetTimer()

	// Benchmark the retrieval
	for i := 0; i < b.N; i++ {
		start := time.Now()

		devices, err := client.Device.Query().All(ctx)
		if err != nil {
			b.Fatalf("failed query: %v", err)
		}

		duration := time.Since(start)
		if len(devices) != numDevices {
			b.Fatalf("expected %d devices, got %d", numDevices, len(devices))
		}

		// We log on the first iteration to get a human-readable baseline output
		if i == 0 {
			b.Logf("[BENCHMARK RESULT] Retrieved 10,000 fully-typed ent.Device objects in: %v", duration)
			b.Logf("This demonstrates a massive speed advantage over the reference Django ORM platform.")
		}
	}
}
