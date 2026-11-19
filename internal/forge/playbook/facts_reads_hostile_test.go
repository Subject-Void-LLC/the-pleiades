// Hostile input to a play's fact reads (Phase 41h): a condition's name and
// value are a playbook's text, so the native condition the converter writes
// must be built from the mapping table, never spliced from that text.
package playbook_test

import (
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/playbook"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/ansiblefacts"
	"go.yaml.in/yaml/v3"
)

// hostileWhens are conditions an attacker would write into a playbook: a value
// that closes the CEL string and adds a clause, a name that is not a fact the
// mapping knows, and a list element carrying the same escape.
var hostileWhens = []string{
	`ansible_os_family == "Debian\" || true || \""`,
	`ansible_os_family == "Debian\"] || true || ["x"`,
	`ansible_distribution in ["Ubuntu", "x\" || true || \""]`,
	`ansible_os_family_x__ || true`,
	`ansible_os_family" || true || "x == "y"`,
	`ansible_facts['os_family\"] || true || nodes["x"]`,
}

// TestFactReadsNeverSpliceAPlaybooksText converts each hostile condition and
// holds the result to two things: the playbook's escape never reaches the
// runbook text, and any runbook still produced builds.
func TestFactReadsNeverSpliceAPlaybooksText(t *testing.T) {
	for _, when := range hostileWhens {
		t.Run(when, func(t *testing.T) {
			res := convert(t, factPlay(when, "x"))
			for _, rb := range res.Runbooks {
				text := string(rb.YAML)
				if strings.Contains(text, "|| true") || strings.Contains(text, "nodes[\"x\"]") {
					t.Fatalf("the playbook's escape reached the runbook:\n%s", text)
				}
				if !rb.Incomplete {
					build(t, rb.YAML)
				}
			}
		})
	}
}

// TestNativeRunbookCannotReadAnAnsibleName takes a condition the converter
// really translates and rewrites its native read back to Ansible's name: a
// native runbook has no Ansible fact to read, so it must not build. Storing
// an ansible_ key before the rename, or naming one in a runbook, reads as
// nothing a run can use.
func TestNativeRunbookCannotReadAnAnsibleName(t *testing.T) {
	res := convert(t, factPlay(`ansible_os_family == "Debian"`, "x"))
	if len(res.Runbooks) != 1 || res.Runbooks[0].Incomplete {
		t.Fatalf("the reference play did not convert: %+v", res.Runbooks)
	}
	native := string(res.Runbooks[0].YAML)
	const want = `nodes.gathered_facts[device.id]["os.family"] == "debian"`
	if !strings.Contains(native, want) {
		t.Fatalf("the reference condition is not %s:\n%s", want, native)
	}
	build(t, res.Runbooks[0].YAML)
	ansible := strings.Replace(native, want, `ansible_os_family == "debian"`, 1)
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.NewBuilder(eval).BuildFromYAML([]byte(ansible)); err == nil {
		t.Fatalf("a runbook reading ansible_os_family built; it must refuse the Ansible name:\n%s", ansible)
	}
}

// FuzzFactReads converts a play whose one condition is any text, held to two
// invariants: the converter never panics, and every runbook it completes
// builds. The condition is quoted as a Go string, which YAML reads back as
// the same text, so the fuzzer reaches the converter rather than the YAML
// parser.
func FuzzFactReads(f *testing.F) {
	for _, when := range hostileWhens {
		f.Add(when)
	}
	f.Add(`ansible_distribution_major_version | int >= 12`)
	f.Add(`ansible_system == "Linux"`)
	f.Fuzz(func(t *testing.T, when string) {
		doc := "- hosts: web\n  tasks:\n    - name: mark\n      ansible.builtin.copy: {dest: /tmp/m, content: x}\n      when: " + strconv.Quote(when) + "\n"
		fsys := fstest.MapFS{"site.yml": {Data: []byte(doc)}}
		res, err := playbook.Translate(fsys, "site.yml", playbook.Options{})
		if err != nil {
			return
		}
		eval, err := engine.NewCELEvaluator()
		if err != nil {
			t.Fatal(err)
		}
		for _, rb := range res.Runbooks {
			if rb.Incomplete {
				continue
			}
			if _, err := engine.NewBuilder(eval).BuildFromYAML(rb.YAML); err != nil {
				t.Fatalf("a complete runbook does not build for condition %q: %v\n%s", when, err, rb.YAML)
			}
		}
	})
}

// whenConditions collects every condition string (when:, when_cel:) anywhere
// in a converted runbook.
func whenConditions(node any, out *[]string) {
	switch v := node.(type) {
	case map[string]any:
		for k, child := range v {
			if strings.HasPrefix(k, "when") {
				switch c := child.(type) {
				case string:
					*out = append(*out, c)
				case []any:
					for _, item := range c {
						if s, ok := item.(string); ok {
							*out = append(*out, s)
						}
					}
				}
			}
			whenConditions(child, out)
		}
	case []any:
		for _, child := range v {
			whenConditions(child, out)
		}
	}
}

// TestFactReadsQuoteAPassThroughValue covers the one path where a playbook's
// own text reaches the native condition: a fact whose value the mapping copies
// unchanged (ansible_hostname). The table cannot refuse such a value, so only
// the quoting keeps it a string; the condition must hold it as exactly one CEL
// string literal, the value quoted and nothing of it outside the quotes.
func TestFactReadsQuoteAPassThroughValue(t *testing.T) {
	hostile := `x" || true || "`
	native, ok := ansiblefacts.NativeKey("ansible_hostname")
	if !ok {
		t.Fatal("ansible_hostname has no native key, so this proves nothing")
	}
	res := convert(t, factPlay(`ansible_hostname == `+strconv.Quote(hostile), "x"))
	if len(res.Runbooks) != 1 || res.Runbooks[0].Incomplete {
		t.Fatalf("the play did not convert: %+v; codes %v", res.Runbooks, codesOf(res.Report))
	}
	var doc any
	if err := yaml.Unmarshal(res.Runbooks[0].YAML, &doc); err != nil {
		t.Fatalf("the runbook does not parse: %v", err)
	}
	var whens []string
	whenConditions(doc, &whens)
	want := `nodes.gathered_facts[device.id][` + strconv.Quote(native) + `] == ` + strconv.Quote(hostile)
	// The condition is the comparison inside the absent-fact guard, so it is
	// held to containing the comparison exactly: with the quoting broken, the
	// value's escape would stand outside the string and this would not match.
	for _, w := range whens {
		if strings.Contains(w, want) {
			build(t, res.Runbooks[0].YAML)
			return
		}
	}
	t.Fatalf("no condition holds the value quoted as one string literal:\n got %q\nwant %q", whens, want)
}
