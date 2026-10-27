// Package redact_test tests the filter for variables a device brings: secrets
// are dropped at any depth and nothing else changes.
package redact_test

import (
	"reflect"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
)

// TestCleanVars_DropsSecretsAtAnyDepth: a secret-named key and a secret-shaped
// text are dropped from nested maps and lists, and what is left is untouched.
func TestCleanVars_DropsSecretsAtAnyDepth(t *testing.T) {
	in := map[string]any{
		"vlan":          10,
		"dns":           []any{"10.0.0.1", "https://admin:hunter2@repo.example.com/x"},
		"db":            map[string]any{"host": "db1", "password": "plain text"},
		"onlyp":         map[string]any{"token": "x"},
		"snmp_password": "x",
		"deep":          map[string]any{"a": []any{map[string]any{"secret": "s", "ok": true}}},
	}
	want := map[string]any{
		"vlan": 10,
		"dns":  []any{"10.0.0.1"},
		"db":   map[string]any{"host": "db1"},
		"deep": map[string]any{"a": []any{map[string]any{"ok": true}}},
	}
	if got := redact.CleanVars(in); !reflect.DeepEqual(got, want) {
		t.Errorf("CleanVars = %#v, want %#v", got, want)
	}
	if in["db"].(map[string]any)["password"] != "plain text" {
		t.Error("CleanVars changed its input")
	}
}
