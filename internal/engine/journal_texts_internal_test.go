// Tests for a list of identifiers as a recorded undo value (InverseParam's
// Texts): kept when every item is text the journal holds, refused
// otherwise, and handed back in the shape a runbook gives a list.
package engine

import (
	"reflect"
	"strings"
	"testing"
)

func TestRecordedValue_KeepsAListOfIdentifiers(t *testing.T) {
	for name, value := range map[string]any{
		"as a method records it": []string{"libjq1", "libonig5"},
		"after YAML or JSON":     []any{"libjq1", "libonig5"},
	} {
		p, ok := recordedValue("dependencies", value, true)
		if !ok || !reflect.DeepEqual(p.Texts, []string{"libjq1", "libonig5"}) || p.Text != nil {
			t.Errorf("%s: recorded %+v, %v", name, p, ok)
		}
		if v, ok := p.Value(); !ok || !reflect.DeepEqual(v, []any{"libjq1", "libonig5"}) {
			t.Errorf("%s: Value() = %v, %v, want the list as []any", name, v, ok)
		}
	}
}

func TestRecordedValue_RefusesAListItCannotHoldExactly(t *testing.T) {
	many := make([]string, maxRecordedItems+1)
	for i := range many {
		many[i] = "pkg"
	}
	for name, value := range map[string]any{
		"empty":            []string{},
		"too long":         many,
		"a non-string":     []any{"libjq1", 3},
		"a line break":     []string{"libjq1\nlibfake"},
		"an oversized one": []string{strings.Repeat("x", maxRecordedText+1)},
		"a list of lists":  []any{[]any{"a"}},
	} {
		if p, ok := recordedValue("dependencies", value, true); ok {
			t.Errorf("%s: recorded %+v", name, p)
		}
	}
}

func TestRecordedValue_RefusesAListWhereTheUndoTakesText(t *testing.T) {
	if p, ok := recordedValue("name", []string{"libjq1"}, false); ok {
		t.Errorf("a list for a parameter not declared a list was recorded: %+v", p)
	}
	if ListParam("no.such.method", "x") {
		t.Error("an unregistered method's parameter reads as a list")
	}
}
