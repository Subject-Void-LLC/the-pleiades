// Phase 41's check-mode observation rule against the catalog: a checked
// task may read what an earlier read-only method emitted during the check
// only when that method's check really reads the device, which engine's
// ObservesInCheck takes to mean its Check is its own Invoke.
package archtest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	_ "github.com/Subject-Void-LLC/the-pleiades/internal/catalog" // every built-in method
	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// TestReadOnlyFactEmittersObserveOnlyWhenTheirCheckIsTheirRun: for every
// built-in declaring it only reads and emits facts, the engine treats its
// check's facts as observations exactly when its Check is its Invoke, and
// never otherwise; the ones that are not are listed, so a reader sees which
// gatherers leave a check incomplete.
func TestReadOnlyFactEmittersObserveOnlyWhenTheirCheckIsTheirRun(t *testing.T) {
	var observing, excluded []string
	for _, name := range builtinNames(t) {
		d, _ := collection.Lookup(name)
		if !d.Manifest.Reversibility.ReadOnly || len(d.Manifest.Facts) == 0 {
			continue
		}
		same := d.Check != nil && d.Invoke != nil && reflect.ValueOf(d.Check).Pointer() == reflect.ValueOf(d.Invoke).Pointer()
		if got := engine.ObservesInCheck(name); got != same {
			t.Errorf("%s: ObservesInCheck = %v, but its Check is its Invoke: %v", name, got, same)
		}
		if same {
			observing = append(observing, name)
		} else {
			excluded = append(excluded, name)
		}
	}
	if len(observing) == 0 {
		t.Fatal("no read-only built-in emitting facts checks by its own run, so this rule proved nothing (facts.gather should)")
	}
	t.Logf("read-only fact emitters whose check is an observation: %v", observing)
	t.Logf("read-only fact emitters whose check is not their run, so their readers in a check are unchecked: %v", excluded)
}

// builtinNames is every built-in method the generated module catalog names
// (docs/reference/schemas/module-catalog.json, which tools/gendocs builds
// from the registry), sorted.
func builtinNames(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "docs", "reference", "schemas", "module-catalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	var catalog map[string]json.RawMessage
	if err := json.Unmarshal(raw, &catalog); err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(catalog))
	for name := range catalog {
		if _, registered := collection.Lookup(name); registered {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}
