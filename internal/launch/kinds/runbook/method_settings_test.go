// Package runbook_test: tests that the runbook kind's method_settings field
// is held to the Controller's catalog and merges setting by setting.
package runbook_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api/wellknown"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launch/kinds/runbook"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// installControllerCatalog installs the module catalog a Controller embeds,
// as cmd/controller does at start: this package links no Collection, as a
// Controller links none, so every method a setting names is answered from it.
func installControllerCatalog(t *testing.T) {
	t.Helper()
	t.Cleanup(collection.CatalogForTest())
	if err := collection.InstallCatalog(wellknown.ModuleCatalog); err != nil {
		t.Fatalf("installing the module catalog: %v", err)
	}
}

// TestMethodSettings_TheControllerHoldsEachToItsMethod proves the runbook
// kind's method_settings field refuses, wherever a value is normalized, a key
// no method declares a run setting and a value outside its choices, reading
// the Controller's own catalog.
func TestMethodSettings_TheControllerHoldsEachToItsMethod(t *testing.T) {
	installControllerCatalog(t)
	d, ok := launch.Lookup(runbook.Kind)
	if !ok {
		t.Fatal("the runbook kind is not registered")
	}
	if _, err := d.Normalize(launch.MethodSettingsField, map[string]any{"file.remove.check_forbidden": "warn"}); err != nil {
		t.Fatalf("a valid setting was refused: %v", err)
	}
	// The field's Check alone, as a kind's own value would reach it: one that
	// is not a map at all is refused too.
	spec, _ := d.Field(launch.MethodSettingsField)
	if err := spec.Check("file.remove.check_forbidden=warn"); err == nil {
		t.Error("a method_settings value that is not a map was accepted")
	}
	for name, value := range map[string]map[string]any{
		"must be one of refuse, warn":     {"file.remove.check_forbidden": "maybe"},
		"declares no run setting":         {"file.remove.recurse": "true"},
		"not a method this build knows":   {"file.nothing.check_forbidden": "warn"},
		"must be text":                    {"file.remove.check_forbidden": true},
		"is not <method>.<parameter>":     {"check_forbidden": "warn"},
		"declares no parameter a run may": {"exec.command.cmd": "uptime"},
	} {
		_, err := d.Normalize(launch.MethodSettingsField, value)
		if !errors.Is(err, launch.ErrInvalidField) || !strings.Contains(err.Error(), name) {
			t.Errorf("%v: Normalize = %v, want ErrInvalidField saying %q", value, err, name)
		}
	}
}

// TestMethodSettings_MergeKeyByKeyAcrossLayers proves the most specific layer
// wins for each setting while the others' settings are kept, and a launch
// value the template does not open is reported rather than applied.
func TestMethodSettings_MergeKeyByKeyAcrossLayers(t *testing.T) {
	installControllerCatalog(t)
	tmpl := launch.Template{
		KindName: runbook.Kind, Definition: "site",
		Defaults: launch.Fields{launch.MethodSettingsField: map[string]any{"file.remove.check_forbidden": "refuse"}},
		Prompts:  []string{launch.MethodSettingsField},
	}
	resolved, ignored, err := tmpl.Resolve(context.Background(), launch.Config{
		Overrides: launch.Fields{launch.MethodSettingsField: map[string]any{"file.remove.check_forbidden": "warn"}},
	})
	if err != nil || len(ignored) != 0 {
		t.Fatalf("Resolve = %v, ignored %v", err, ignored)
	}
	if got := launch.MethodSettings(resolved.Fields); got["file.remove.check_forbidden"] != "warn" {
		t.Errorf("the launch's setting = %v, want warn over the template's refuse", got)
	}

	tmpl.Prompts = nil
	resolved, ignored, err = tmpl.Resolve(context.Background(), launch.Config{
		Overrides: launch.Fields{launch.MethodSettingsField: map[string]any{"file.remove.check_forbidden": "warn"}},
	})
	if err != nil || len(ignored) != 1 || ignored[0].Reason != launch.ReasonLocked {
		t.Fatalf("a locked field: Resolve = %v, ignored %v, want it reported locked", err, ignored)
	}
	if got := launch.MethodSettings(resolved.Fields); got["file.remove.check_forbidden"] != "refuse" {
		t.Errorf("a locked field = %v, want the template's own", got)
	}

	// A launch refused for a bad value is reported, and the template's
	// settings stand.
	tmpl.Prompts = []string{launch.MethodSettingsField}
	resolved, ignored, err = tmpl.Resolve(context.Background(), launch.Config{
		Overrides: launch.Fields{launch.MethodSettingsField: map[string]any{"file.remove.check_forbidden": "maybe"}},
	})
	if err != nil || len(ignored) != 1 || !strings.Contains(ignored[0].Reason, "must be one of") {
		t.Fatalf("a bad launch value: Resolve = %v, ignored %v, want it reported", err, ignored)
	}
	if got := launch.MethodSettings(resolved.Fields); got["file.remove.check_forbidden"] != "refuse" {
		t.Errorf("after a bad launch value = %v, want the template's own", got)
	}
}
