// Tests for the method settings field's accessor, and for a field's own
// Check, which the runbook kind's method_settings is the first user of.
package launch_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
)

// TestMethodSettings reads only text values, and nothing when unset.
func TestMethodSettings(t *testing.T) {
	for _, tc := range []struct {
		name   string
		fields launch.Fields
		want   map[string]string
	}{
		{"no fields", nil, nil},
		{"field unset", launch.Fields{"forks": 5}, nil},
		{"empty", launch.Fields{launch.MethodSettingsField: map[string]any{}}, nil},
		{"a value that is not text is left out", launch.Fields{launch.MethodSettingsField: map[string]any{"a.b": "c", "d.e": true}}, map[string]string{"a.b": "c"}},
	} {
		if got := launch.MethodSettings(tc.fields); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: MethodSettings = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestFieldCheck_RefusesWhatTheKindRefuses proves a field's Check runs after
// its type check, wherever a value is normalized, and its refusal is an
// ErrInvalidField naming the field.
func TestFieldCheck_RefusesWhatTheKindRefuses(t *testing.T) {
	t.Cleanup(launch.SnapshotForTest())
	checked := 0
	err := launch.Register(launch.Descriptor{
		Kind: "test-checked-field", Adapter: "native",
		Fields: []launch.FieldSpec{{Name: "names", Type: launch.TypeMap, Check: func(v any) error {
			checked++
			if _, bad := v.(map[string]any)["bad"]; bad {
				return errors.New("bad is not a name")
			}
			return nil
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	d, _ := launch.Lookup("test-checked-field")
	if _, err := d.Normalize("names", map[string]any{"good": "x"}); err != nil {
		t.Errorf("a value its Check passes: %v", err)
	}
	_, err = d.Normalize("names", map[string]any{"bad": "x"})
	if !errors.Is(err, launch.ErrInvalidField) || !strings.Contains(err.Error(), `"names": bad is not a name`) {
		t.Errorf("a value its Check refuses: %v, want ErrInvalidField naming the field and why", err)
	}
	// A value of the wrong type is refused by the type, before Check.
	before := checked
	if _, err := d.Normalize("names", "not a map"); !errors.Is(err, launch.ErrInvalidField) || checked != before {
		t.Errorf("a value of the wrong type: %v, Check called %d times, want refused by type alone", err, checked-before)
	}
}

// TestStrictMethodSettings refuses a value that is not text, naming its key,
// where MethodSettings drops it.
func TestStrictMethodSettings(t *testing.T) {
	if _, err := launch.StrictMethodSettings(launch.Fields{launch.MethodSettingsField: map[string]any{"a.b": 1}}); err == nil || !strings.Contains(err.Error(), `"a.b"`) {
		t.Errorf("a number: %v, want it refused naming the key", err)
	}
	got, err := launch.StrictMethodSettings(launch.Fields{launch.MethodSettingsField: map[string]any{"a.b": "c"}})
	if err != nil || got["a.b"] != "c" {
		t.Errorf("text: %v, %v", got, err)
	}
}
