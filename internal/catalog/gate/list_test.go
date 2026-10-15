// Tests that gate.list validates and classifies a manifest's checks and
// runs none of them.
package gate_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/gate"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// TestList_ClassifiesWithoutRunning reports each check's classification,
// what the selection would run and why not, and what the device cannot
// run, and runs nothing.
func TestList_ClassifiesWithoutRunning(t *testing.T) {
	h := newHarness(t)
	marker := filepath.Join(h.project, "ran")
	manifest := h.manifest(t, `version: 1
kinds: [{name: browser, description: needs a headless browser}]
defaults: {timeout: 2m}
checks:
  - {name: unit, kind: pure, shell: "touch `+marker+`", dir: src, requires: {commands: [go], env: [GOFLAGS]}}
  - {name: db, kind: db, heavy: true, serial: true, timeout: 20m, shell: "touch `+marker+`", image: 'postgres@sha256:`+strings.Repeat("ab", 32)+`', requires: {tcp: ["db:5432"]}}
  - {name: ui, kind: browser, shell: "touch `+marker+`"}
`)
	res, err := gate.List(context.Background(), h.rc, h.device, params(manifest, map[string]any{"kinds": []any{"pure", "db"}}))
	if err != nil || res.Changed {
		t.Fatalf("List = %+v, %v", res, err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("gate.list ran a check")
	}
	got := resultsByName(t, h.rc)
	unit, db, ui := got["unit"], got["db"], got["ui"]
	if unit["selected"] != true || unit["runs"] != "dir "+filepath.Join(h.project, "src") || unit["timeout_s"] != int64(120) || unit["requires"] != "commands go; env GOFLAGS" || unit["runnable"] != true {
		t.Errorf("unit = %v", unit)
	}
	if db["heavy"] != true || db["serial"] != true || db["timeout_s"] != int64(1200) || !strings.HasPrefix(db["runs"].(string), "image postgres@sha256:") {
		t.Errorf("db = %v", db)
	}
	if db["runnable"] != false || !strings.Contains(db["reason"].(string), "no engine was onboarded") {
		t.Errorf("an image check on a device with no engine = %v", db)
	}
	if ui["selected"] != false || !strings.Contains(ui["reason"].(string), "its kind, browser") {
		t.Errorf("ui = %v", ui)
	}
	if h.rc.stats["selected"] != 2 {
		t.Errorf("selected = %v", h.rc.stats["selected"])
	}
	kinds, _ := h.rc.stats["kinds"].([]any)
	var names []string
	for _, k := range kinds {
		names = append(names, k.(map[string]any)["name"].(string))
	}
	if strings.Join(names, ",") != "browser,db,model,network,pure" {
		t.Errorf("kinds = %v", names)
	}
}

// TestList_AnEngineDeviceCanRunAnImageCheck: the same image check on a
// device with an engine onboarded is runnable.
func TestList_AnEngineDeviceCanRunAnImageCheck(t *testing.T) {
	h := newHarness(t, capability.NameContainerEngine)
	manifest := h.manifest(t, "version: 1\nchecks:\n  - {name: db, kind: db, timeout: 1m, shell: \"true\", image: 'postgres@sha256:"+strings.Repeat("ab", 32)+"'}\n")
	if _, err := gate.List(context.Background(), h.rc, h.device, params(manifest, nil)); err != nil {
		t.Fatal(err)
	}
	if db := resultsByName(t, h.rc)["db"]; db["runnable"] != true || db["reason"] != "" {
		t.Errorf("db = %v", db)
	}
}

// TestList_RefusesAnInvalidManifest under the rules gate.run applies.
func TestList_RefusesAnInvalidManifest(t *testing.T) {
	h := newHarness(t)
	manifest := h.manifest(t, "version: 1\nchecks:\n  - {name: a, kind: pure, shell: \"true\"}\n")
	if _, err := gate.List(context.Background(), h.rc, h.device, params(manifest, nil)); err == nil || !strings.Contains(err.Error(), "no timeout") {
		t.Fatalf("List = %v", err)
	}
}

// TestList_Registered is read-only, and its check is the method itself.
func TestList_Registered(t *testing.T) {
	d, ok := collection.Lookup("gate.list")
	if !ok {
		t.Fatal("gate.list is not registered")
	}
	if !d.Manifest.Reversibility.ReadOnly || !d.Manifest.SupportsCheck || d.CheckCall != nil {
		t.Errorf("manifest = %+v", d.Manifest)
	}
}
