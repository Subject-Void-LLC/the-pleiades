// Black-box tests of ansible_distribution_major_version | int
// (facts_major.go): the converted condition, evaluated by the engine's own
// CEL evaluator over each host's native facts, decides what Jinja decides
// over the Ansible facts ToAnsible gives for the same host.
package playbook_test

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/playbook"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/ansiblefacts"
)

// majorHosts are native fact sets as facts.gather reports them, one per kind
// of major version: a one- and a two-digit major, Debian testing (whose
// version forky/sid has no major), a rolling release with no version at all,
// and Windows.
var majorHosts = map[string]map[string]any{
	"debian 12":      {"os.distribution": "debian", "os.family": "debian", "os.version": "12.12", "os.version_major": int64(12)},
	"rhel 8":         {"os.distribution": "rhel", "os.family": "redhat", "os.version": "8.10", "os.version_major": int64(8)},
	"rhel 10":        {"os.distribution": "rhel", "os.family": "redhat", "os.version": "10.0", "os.version_major": int64(10)},
	"debian testing": {"os.distribution": "debian", "os.family": "debian", "os.version": "forky/sid"},
	"arch":           {"os.distribution": "arch", "os.family": "arch"},
	"alpine 3":       {"os.distribution": "alpine", "os.family": "alpine", "os.version": "3.20.3", "os.version_major": int64(3)},
	"windows 10":     {"os.distribution": "windows", "os.family": "windows", "os.kernel.name": "windows", "os.version": "10.0.20348", "os.version_major": int64(10)},
}

// jinjaInt is Jinja's int filter over Ansible's major version as ToAnsible
// gives it for native: the number a whole-number string spells, and 0, the
// filter's default, for any other string. A host ToAnsible gives no major
// (no version at all) reports Ansible's placeholder NA there, whose int is 0
// too.
func jinjaInt(native map[string]any) int64 {
	s, _ := ansiblefacts.ToAnsible(native)["ansible_distribution_major_version"].(string)
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// pyCompare is Python's comparison op between two integers.
func pyCompare(op string, a, b int64) bool {
	switch op {
	case "==":
		return a == b
	case "!=":
		return a != b
	case "<":
		return a < b
	case "<=":
		return a <= b
	case ">":
		return a > b
	}
	return a >= b
}

// markWhen is the condition the converted runbook's task named mark carries.
func markWhen(t *testing.T, runbook []byte) string {
	t.Helper()
	var doc struct {
		Tasks []map[string]any `yaml:"tasks"`
	}
	if err := yaml.Unmarshal(runbook, &doc); err != nil {
		t.Fatalf("the runbook does not parse: %v\n%s", err, runbook)
	}
	for _, task := range doc.Tasks {
		if task["name"] != "mark" {
			continue
		}
		switch w := task["when"].(type) {
		case string:
			return w
		case []any:
			if len(w) == 1 {
				s, _ := w[0].(string)
				return s
			}
		}
	}
	t.Fatalf("no task named mark with one condition:\n%s", runbook)
	return ""
}

// evalOnHost evaluates cel with the engine's evaluator for the device dev
// whose gathered facts are facts.
func evalOnHost(t *testing.T, cel string, facts map[string]any) bool {
	t.Helper()
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatal(err)
	}
	prg, err := eval.Compile(cel)
	if err != nil {
		t.Fatalf("the condition %q does not compile: %v", cel, err)
	}
	ok, err := prg.Eval(map[string]any{
		"nodes":  map[string]any{"gathered_facts": map[string]any{"dev": facts}},
		"device": map[string]any{"id": "dev"},
		"stat":   map[string]any{}, "vars": map[string]any{},
	})
	if err != nil {
		t.Fatalf("the condition %q does not evaluate: %v", cel, err)
	}
	return ok
}

func TestTranslate_MajorVersionThroughIntDecidesPerHostAsJinjaDoes(t *testing.T) {
	for _, tc := range []struct {
		when string
		// op and n are the comparison Jinja makes, the integer on the left.
		op string
		n  int64
	}{
		{`ansible_distribution_major_version | int >= 8`, ">=", 8},
		{`ansible_distribution_major_version|int == 12`, "==", 12},
		{`(ansible_distribution_major_version | int) < 10`, "<", 10},
		{`ansible_distribution_major_version | int <= 8`, "<=", 8},
		{`ansible_distribution_major_version | int > 8`, ">", 8},
		{`ansible_facts.distribution_major_version | int != 7`, "!=", 7},
		{`ansible_facts['distribution_major_version'] | int > 9`, ">", 9},
		// The number on the left: 8 <= x is x >= 8.
		{`8 <= ansible_distribution_major_version | int`, ">=", 8},
		{`10 > ansible_distribution_major_version | int`, "<", 10},
	} {
		t.Run(tc.when, func(t *testing.T) {
			res := convert(t, factPlay(tc.when, "x"))
			codes := codesOf(res.Report)
			if len(res.Runbooks) != 1 || res.Runbooks[0].Incomplete || !slices.Contains(codes, "play.facts_gathered") {
				t.Fatalf("the condition did not convert over an inserted gather: codes %v\n%s", codes, res.Report.String())
			}
			build(t, res.Runbooks[0].YAML)
			cel := markWhen(t, res.Runbooks[0].YAML)
			for host, facts := range majorHosts {
				want := pyCompare(tc.op, jinjaInt(facts), tc.n)
				if got := evalOnHost(t, cel, facts); got != want {
					t.Errorf("%s: %q is %v natively, %v in Jinja (int of the major is %d)", host, cel, got, want, jinjaInt(facts))
				}
			}
		})
	}
}

func TestTranslate_MajorVersionReadsJinjaWouldNotTranslateAreRefused(t *testing.T) {
	for _, tc := range []struct {
		name, when string
		code       playbook.Code
		// mentions are words the refusal must carry.
		mentions []string
	}{
		// Jinja raises comparing an integer with text.
		{name: "a text operand", when: `ansible_distribution_major_version | int >= "8"`, code: "when.fact"},
		{name: "a fraction", when: `ansible_distribution_major_version | int >= 8.5`, code: "when.fact"},
		{name: "membership", when: `ansible_distribution_major_version | int in [8, 9]`, code: "when.fact"},
		{name: "read as a truth", when: `ansible_distribution_major_version | int`, code: "when.fact"},
		// int with arguments, and a filter before it, are the parser's to refuse.
		{name: "int with a default", when: `ansible_distribution_major_version | int(5) >= 8`, code: "when.unsupported"},
		{name: "default before int", when: `ansible_distribution_major_version | default(0) | int >= 8`, code: "when.unsupported"},
		// | int of any other fact differs where the fact is no number.
		{name: "int of another fact", when: `ansible_distribution_version | int >= 8`, code: "when.fact"},
		{name: "the bare text ordering", when: `ansible_distribution_major_version >= "8"`, code: "when.fact",
			mentions: []string{"ansible_distribution_major_version | int", "os.version_major"}},
		{name: "a number against the text", when: `ansible_distribution_major_version == 12`, code: "when.fact",
			mentions: []string{"| int", "os.version_major"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := convert(t, factPlay(tc.when, "x"))
			if codes := codesOf(res.Report); !slices.Contains(codes, tc.code) {
				t.Errorf("codes %v, want %s", codes, tc.code)
			}
			for _, rb := range res.Runbooks {
				if strings.Contains(string(rb.YAML), `"os.version_major"`) {
					t.Errorf("the refused condition was converted:\n%s", rb.YAML)
				}
			}
			for _, word := range tc.mentions {
				if !strings.Contains(res.Report.String(), word) {
					t.Errorf("the refusal does not name %q:\n%s", word, res.Report.String())
				}
			}
		})
	}
}
