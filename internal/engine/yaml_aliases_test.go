// Tests that a runbook's YAML anchors, aliases and merge keys are read as
// the values they stand for, and that what cannot be expanded is refused by
// what it is: aliases that expand without bound, a merge of something that
// is no mapping, and a top-level key that only holds an anchor.
package engine_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
)

func TestBuild_ExpandsAliasesAndMergeKeys(t *testing.T) {
	eval, _ := engine.NewCELEvaluator()
	builder := engine.NewBuilder(eval)
	dag, err := builder.BuildFromYAML([]byte(`id: alias
tasks:
  - name: one
    noop: &common
      command: show clock
      count: 3
  - name: two
    noop: *common
  - name: three
    noop:
      <<: *common
      command: show version
  - name: four
    noop:
      <<: [*common, {extra: true, count: 9}]
`))
	if err != nil {
		t.Fatal(err)
	}
	want := []map[string]any{
		{"command": "show clock", "count": 3},
		{"command": "show clock", "count": 3},
		{"command": "show version", "count": 3},
		{"command": "show clock", "count": 3, "extra": true},
	}
	for i, w := range want {
		got := dag.Tasks[i].Params
		if len(got) != len(w) {
			t.Errorf("task %d params %v, want %v", i, got, w)
			continue
		}
		for k, v := range w {
			if got[k] != v {
				t.Errorf("task %d: %s = %v, want %v", i, k, got[k], v)
			}
		}
	}

	for name, tc := range map[string]struct{ runbook, want string }{
		"aliases without bound":              {laughs(), "holds more than"},
		"a merge of a list holding a scalar": {"id: m\ntasks:\n  - noop:\n      <<: [{a: 1}, 2]\n", "takes a mapping or a list of mappings"},
		"an alias inside its own anchor":     {"id: r\ntasks:\n  - noop: &a\n      x: *a\n", "nests deeper"},
		"a merge that recurses":              {"id: r\ntasks:\n  - noop:\n      <<: &m\n        x: *m\n", "nests deeper"},
		"two merge keys":                     {"id: m\ntasks:\n  - noop:\n      <<: {a: 1}\n      <<: {a: 2}\n", "two merge keys"},
		"a merge of a scalar":                {"id: m\ntasks:\n  - noop:\n      <<: just text\n", "takes a mapping or a list of mappings"},
		"an anchor-only key":                 {"id: a\nx-common: &c\n  command: x\ntasks:\n  - noop: *c\n", `"x-common" is there only to hold an anchor`},
	} {
		if _, err := builder.BuildFromYAML([]byte(tc.runbook)); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want an error saying %q", name, err, tc.want)
		}
	}
}

// laughs is a runbook whose aliases double nine times over, each level ten
// times the one before: a billion leaves from a few lines.
func laughs() string {
	var b strings.Builder
	b.WriteString("id: l\ntasks:\n  - noop:\n      a0: &a0 [x, x, x, x, x, x, x, x, x, x]\n")
	for i := 1; i < 9; i++ {
		prev := "*a" + string(rune('0'+i-1))
		b.WriteString("      a" + string(rune('0'+i)) + ": &a" + string(rune('0'+i)) + " [" + strings.Repeat(prev+", ", 9) + prev + "]\n")
	}
	return b.String()
}
