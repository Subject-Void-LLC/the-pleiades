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
