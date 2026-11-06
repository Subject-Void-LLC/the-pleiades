// Package templates: tests that a template's saved value is described in a
// launch field's help, method settings by name and extra variables by count.
package templates

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
)

// TestSavedValue_NamesMethodSettingsAndCountsVariables proves a launch form
// says which method settings a template holds, which are choice words, while
// extra variables, which may hold anything, are only counted.
func TestSavedValue_NamesMethodSettingsAndCountsVariables(t *testing.T) {
	defaults := launch.Fields{
		launch.MethodSettingsField: map[string]any{"z.y.mode": "warn", "a.b.mode": "refuse"},
		"extra_vars":               map[string]any{"password": "hunter2"},
	}
	settings := launch.FieldSpec{Name: launch.MethodSettingsField, Type: launch.TypeMap}
	if got := savedValue(settings, defaults); got != "a.b.mode=refuse, z.y.mode=warn" {
		t.Errorf("method settings = %q", got)
	}
	vars := launch.FieldSpec{Name: "extra_vars", Type: launch.TypeMap}
	if got := savedValue(vars, defaults); got != "1 variables" {
		t.Errorf("extra variables = %q, want only a count", got)
	}
}
