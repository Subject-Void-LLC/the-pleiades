// Package yang: the harness net.yang.schema's tests share: a run's context
// that writes into a real project directory under the project's own rules
// (relative, inside, never .pleiades), a source scripted with a device's
// library and sources, and devices of each protocol.
package yang

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/yanglib"
)

// probeSource is a module whose words a configuration masker takes for
// secrets: a leaf named password, a list's key statement, the words secret
// and key before a value, numbers and an address. Every one is schema.
const probeSource = `module pleiades-probe {
  yang-version 1.1;
  namespace "urn:pleiades:probe";
  prefix pp;
  revision 2026-11-16 {
    description "A probe with a password leaf, a secret and 10.1.2.3.";
  }
  container users {
    list user {
      key "name";
      leaf name { type string; }
      leaf password { type string; description "the secret key 100, enable secret 5 $1$abcd$efghijklmnopqrstuv"; }
      leaf key { type uint32 { range "1..4294967295"; } }
    }
  }
}
`

// testContext is a run's context writing into dir as the command line's
// does: a relative path inside dir, never through .pleiades, written whole.
type testContext struct {
	dir     string
	secrets map[string]string
	known   []string
	stats   map[string]any
	// failStat names a stat whose recording fails.
	failStat string
	// writes counts real writes.
	writes int
}

// newContext is a context for a project at a fresh directory.
func newContext(t *testing.T) *testContext {
	t.Helper()
	return &testContext{dir: t.TempDir(), stats: map[string]any{}}
}

func (c *testContext) InjectSecrets() map[string]string { return c.secrets }

func (c *testContext) SetStat(key string, value any) error {
	if key == c.failStat {
		return errors.New("recording the stat failed")
	}
	c.stats[key] = value
	return nil
}

func (c *testContext) EmitFact(key string, value any) error { return c.SetStat(key, value) }

// KnownSecretValues implements sdk.KnownSecrets.
func (c *testContext) KnownSecretValues() []string { return c.known }

// WriteProjectFile implements sdk.ProjectFileWriter under the rules the
// command line's writer holds a path to that matter here.
func (c *testContext) WriteProjectFile(name string, data []byte, perm fs.FileMode, write bool) (bool, error) {
	clean := path.Clean(name)
	if path.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, ".pleiades") {
		return false, errors.New("the path leaves the project")
	}
	full := filepath.Join(c.dir, filepath.FromSlash(clean))
	if old, err := os.ReadFile(full); err == nil && bytes.Equal(old, data) {
		return false, nil
	}
	if !write {
		return true, nil
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return false, err
	}
	c.writes++
	return true, os.WriteFile(full, data, perm)
}

// read is the file name names in the project, and whether it is there.
func (c *testContext) read(t *testing.T, name string) (string, bool) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(c.dir, filepath.FromSlash(name)))
	if errors.Is(err, fs.ErrNotExist) {
		return "", false
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(raw), true
}

// records are the modules stat's entries.
func (c *testContext) records(t *testing.T) []map[string]any {
	t.Helper()
	list, _ := c.stats["modules"].([]any)
	out := make([]map[string]any, 0, len(list))
	for _, item := range list {
		out = append(out, item.(map[string]any))
	}
	return out
}

// noProject is a context with no project, as a Runner's is: embedding the
// interface promotes its three methods and no writer.
type noProject struct {
	sdk.RunbookContext
}

// fakeSource is a source scripted with a library and its sources.
type fakeSource struct {
	listing listing
	// sources answer each entry's fetch by its key.
	sources map[string][]byte
	// listErr and fetchErr fail the list or every fetch.
	listErr, fetchErr error
	said              []string
	fetched           []string
	closed            bool
}

func (f *fakeSource) list(context.Context) (listing, error) { return f.listing, f.listErr }

func (f *fakeSource) fetch(_ context.Context, e yanglib.Schema, _ int) ([]byte, error) {
	f.fetched = append(f.fetched, e.Key())
	if f.fetchErr != nil {
		return nil, f.fetchErr
	}
	src, ok := f.sources[e.Key()]
	if !ok {
		return nil, errors.New("the scripted device has no such source")
	}
	return src, nil
}

func (f *fakeSource) warnings() []string { return f.said }
func (f *fakeSource) close()             { f.closed = true }

// probeLibrary is a library listing the probe, a module with a submodule,
// and an import-only module, each with its source.
func probeLibrary() *fakeSource {
	return &fakeSource{
		listing: listing{completeness: yanglib.Complete, entries: []yanglib.Schema{
			{Name: "ietf-inet-types", Revision: "2013-07-15", ImportOnly: true},
			{Name: "pleiades-parent", Revision: "2024-01-01", Submodules: []yanglib.Schema{{Name: "pleiades-child", Revision: "2024-01-02"}}},
			{Name: "pleiades-probe", Revision: "2026-11-16"},
		}},
		sources: map[string][]byte{
			"ietf-inet-types@2013-07-15": []byte("module ietf-inet-types { revision 2013-07-15; }\n"),
			"pleiades-parent@2024-01-01": []byte("module pleiades-parent { include pleiades-child; revision 2024-01-01; }\n"),
			"pleiades-child@2024-01-02":  []byte("submodule pleiades-child { belongs-to pleiades-parent { prefix p; } revision 2024-01-02; }\n"),
			"pleiades-probe@2026-11-16":  []byte(probeSource),
		},
	}
}

// withSource has every alternative open src, and counts the opens.
func withSource(t *testing.T, src *fakeSource) *[]string {
	t.Helper()
	saved := openers
	var opened []string
	openers = map[string]func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (source, error){}
	for _, name := range []string{viaNETCONF, viaRESTCONF} {
		openers[name] = func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (source, error) {
			opened = append(opened, name)
			return src, nil
		}
	}
	t.Cleanup(func() { openers = saved })
	return &opened
}

// device is a stub named name holding caps.
func device(name string, caps ...capability.Name) inventory.InventoryItem {
	return &inventorytest.Stub{StubID: inventory.DeviceID(name), StubName: name, Caps: caps}
}

// netconfOnly, restconfOnly, both and gnmiOnly are devices of each kind.
var (
	netconfOnly  = []capability.Name{capability.NameNetconf, capability.NameSSHTransport}
	restconfOnly = []capability.Name{capability.NameRestconf}
	bothKinds    = []capability.Name{capability.NameNetconf, capability.NameSSHTransport, capability.NameRestconf}
	gnmiOnly     = []capability.Name{capability.NameGNMI}
)
