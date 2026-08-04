package inventory_test

import (
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/inventory"
)

// FuzzParseHosts ensures malformed, adversarial, or deeply nested YAML
// inventory files can never panic the parser or the factory it feeds.
func FuzzParseHosts(f *testing.F) {
	factory := inventory.NewItemFactory()

	f.Add([]byte("hosts:\n  - name: a\n    type: linux_server\n"))
	f.Add([]byte("hosts: []"))
	f.Add([]byte(""))
	f.Add([]byte("hosts:\n  - name: a\n    type: cisco_router\n    properties:\n      port: not-an-int\n"))
	f.Add([]byte("hosts: *anchor_that_does_not_exist"))

	f.Fuzz(func(t *testing.T, payload []byte) {
		hosts, err := inventory.ParseHosts(payload)
		if err != nil {
			return
		}
		// Parsing succeeded; hydration must still never panic, only error.
		_, _ = inventory.HydrateHosts(factory, hosts)
	})
}
