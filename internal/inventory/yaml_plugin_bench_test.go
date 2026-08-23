package inventory_test

import (
	"fmt"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
)

// BenchmarkParseHosts measures parse time for a representative
// thousand-host static inventory file, the Crawl-tier scale this format is
// meant for (a hand-authored file, not a 100,000-device Walk-tier group).
func BenchmarkParseHosts(b *testing.B) {
	hosts := make([]inventory.HostSpec, 1000)
	for i := range hosts {
		hosts[i] = inventory.HostSpec{
			ID:   fmt.Sprintf("id-%d", i),
			Name: fmt.Sprintf("host-%d", i),
			Type: "linux_server",
			Tags: []string{"bench"},
			Properties: map[string]interface{}{
				"host": fmt.Sprintf("10.0.%d.%d", i/256, i%256),
			},
		}
	}
	data, err := inventory.EncodeHosts(nil, hosts)
	if err != nil {
		b.Fatalf("failed to encode fixture: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := inventory.ParseHosts(data); err != nil {
			b.Fatalf("failed to parse: %v", err)
		}
	}
}
