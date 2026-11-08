// Black-box tests of ansible_distribution_major_version | int
// (facts_major.go): the converted condition, evaluated by the engine's own
// CEL evaluator over each measured distribution's native facts (what
// facts.gather's own os-release probe reads from the file captured in that
// image), decides what Jinja decides over the major version
// ansible.builtin.setup printed in the same image.
package playbook_test

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/facts"
	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/playbook"
)

// distributionTestdata is where internal/catalog/facts keeps each image's
// captured os-release and ansible.json, Ansible's answers in the same images.
var distributionTestdata = filepath.Join("..", "..", "catalog", "facts", "testdata", "distribution")

// measuredHost is one measured image: the native facts facts.gather reads
// there, and Ansible's ansible_distribution_major_version there.
type measuredHost struct {
	native map[string]any
	major  string
}

// measuredHosts reads every image ansible.json records, running the real
// os-release probe's parser over the captured file.
func measuredHosts(t *testing.T) map[string]measuredHost {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(distributionTestdata, "ansible.json"))
	if err != nil {
		t.Fatal(err)
	}
	var measured struct {
		Images map[string]map[string]string `json:"images"`
	}
	if err := json.Unmarshal(raw, &measured); err != nil {
		t.Fatal(err)
	}
	probes := facts.Probes("Linux", "min", "/etc/os-release")
	if len(probes) != 1 {
		t.Fatalf("%d os-release probes", len(probes))
	}
	out := map[string]measuredHost{}
	for slug, ansible := range measured.Images {
		captured, err := os.ReadFile(filepath.Join(distributionTestdata, slug+".txt"))
		if err != nil {
			t.Fatal(err)
		}
		out[slug] = measuredHost{native: probes[0].Read(string(captured)), major: ansible["ansible_distribution_major_version"]}
	}
	// Debian testing is the image whose version has no whole-number first
	// part (forky/sid): the one where the native fact is absent and Jinja's
	// int is 0.
	if h, ok := out["debian-testing"]; !ok || h.major != "forky/sid" {
		t.Fatalf("the measured images lack Debian testing's forky/sid: %+v", h)
	}
	return out
}

// jinjaInt is Jinja's int filter (do_int) over Ansible's major version s:
// int(s), else int(float(s)), else the default 0.
func jinjaInt(s string) int64 {
	if n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64); err == nil {
		return n
	}
	if f, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil && !math.IsInf(f, 0) && !math.IsNaN(f) {
		return int64(f)
	}
	return 0
}

// asJSON is native as a decoded JSON report holds it: every number a
// float64.
func asJSON(native map[string]any) map[string]any {
	out := make(map[string]any, len(native))
	for k, v := range native {
		switch n := v.(type) {
		case int:
			out[k] = float64(n)
		case int64:
			out[k] = float64(n)
		default:
			out[k] = v
		}
	}
	return out
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
	hosts := measuredHosts(t)
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
			for slug, host := range hosts {
				want := pyCompare(tc.op, jinjaInt(host.major), tc.n)
				// As the method hands the facts over, and as a decoded report
				// holds them.
				for _, native := range []map[string]any{host.native, asJSON(host.native)} {
					if got := evalOnHost(t, cel, native); got != want {
						t.Errorf("%s: %q is %v natively over %v, %v in Jinja (Ansible's major %q)", slug, cel, got, native, want, host.major)
					}
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
		// A filter after | int is not translated.
		{name: "int then bool", when: `ansible_distribution_major_version | int | bool`, code: "when.fact"},
		{name: "int twice", when: `ansible_distribution_major_version | int | int >= 8`, code: "when.fact"},
		{name: "int then length", when: `ansible_distribution_major_version | int | length > 1`, code: "when.fact"},
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
