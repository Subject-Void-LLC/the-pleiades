// Tests of holdableParam, the strict reader's rule for one value a journaled
// rollback: step carries: a key and exactly one value of a kind the
// projection admits.
package journal

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
)

// TestHoldableParam_OneValueOfAKindTheJournalHolds holds each kind, valid and
// not, and a value with none or two kinds set.
func TestHoldableParam_OneValueOfAKindTheJournalHolds(t *testing.T) {
	text := func(s string) *string { return &s }
	number := func(s string) *json.Number { n := json.Number(s); return &n }
	yes := true
	for name, tc := range map[string]struct {
		p    engine.InverseParam
		want bool
	}{
		"a text":                    {engine.InverseParam{Key: "name", Text: text("web-1")}, true},
		"a text with no key":        {engine.InverseParam{Text: text("web-1")}, false},
		"a text a terminal acts on": {engine.InverseParam{Key: "name", Text: text("web\x1b[2J")}, false},
		"a text past its bound":     {engine.InverseParam{Key: "name", Text: text(strings.Repeat("a", 1<<20))}, false},
		"a list":                    {engine.InverseParam{Key: "names", Texts: []string{"a", "b"}}, true},
		"a list with a bad item":    {engine.InverseParam{Key: "names", Texts: []string{"a", "b\nc"}}, false},
		"a number":                  {engine.InverseParam{Key: "port", Number: number("22")}, true},
		"a number that is none":     {engine.InverseParam{Key: "port", Number: number("twenty")}, false},
		"a bool":                    {engine.InverseParam{Key: "force", Bool: &yes}, true},
		"no value":                  {engine.InverseParam{Key: "name"}, false},
		"two values":                {engine.InverseParam{Key: "name", Text: text("a"), Bool: &yes}, false},
	} {
		if got := holdableParam(tc.p); got != tc.want {
			t.Errorf("%s: holdableParam = %v, want %v", name, got, tc.want)
		}
	}
}
