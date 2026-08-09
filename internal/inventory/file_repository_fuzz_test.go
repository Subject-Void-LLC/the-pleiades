package inventory_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
)

// FuzzFileRepositorySaveAndGetByName fuzzes Save and GetByName against
// randomized host names and property mutations, proving neither can panic
// regardless of what a caller passes as a host name or a property
// key/value pair. It mirrors FuzzFactoryParser (factory_fuzz_test.go) and
// FuzzParseHosts (yaml_plugin_fuzz_test.go) in spirit: this package's
// existing fuzz targets attack the factory and the YAML parser in
// isolation, while this one attacks the file-backed Repository's full
// read-mutate-write-reload path.
func FuzzFileRepositorySaveAndGetByName(f *testing.F) {
	f.Add("web-1", "kernel", "6.6.1", true)
	f.Add("", "", "", false)
	f.Add("host with spaces", "a-key", "", true)
	f.Add("host-\x00-null", "key\nwith\nnewlines", "value", false)
	f.Add("host-with-unicode-é中", "é-key", "中-value", true)

	f.Fuzz(func(t *testing.T, name, key, value string, overwrite bool) {
		dir := t.TempDir()
		path := filepath.Join(dir, "hosts.yaml")
		hosts := []inventory.HostSpec{
			{
				ID:   "fuzz-id",
				Name: name,
				Type: "linux_server",
			},
		}
		if err := inventory.WriteHosts(path, hosts); err != nil {
			// A fuzzed name that cannot round-trip through the YAML
			// encoder is an input problem for ParseHosts/EncodeHosts
			// (already fuzzed by FuzzParseHosts in yaml_plugin_fuzz_test.go),
			// not something this target needs to re-prove; skip it.
			t.Skip("cannot seed fixture:", err)
		}

		repo := inventory.NewFileRepository(path, inventory.NewItemFactory())
		ctx := context.Background()

		item, err := repo.GetByName(ctx, name)
		if err != nil {
			// A legitimate not-found (for instance if YAML re-encoding
			// altered the fuzzed name) is not a panic; nothing left to do.
			return
		}

		if key == "" {
			// AddInfo/RemoveInfo on an empty key is a caller error the
			// domain layer (record.Base) already rejects predictably.
			// Save itself must still never panic on an unmutated item.
			_ = repo.Save(ctx, item)
			return
		}

		// AddInfo may legitimately error (overwrite=false on an existing
		// key); what matters here is that neither it nor the subsequent
		// Save and reload ever panics.
		_ = item.AddInfo(key, value, overwrite)
		_ = repo.Save(ctx, item)
		_, _ = repo.GetByName(ctx, name)
	})
}
