// Tests for the strict key check (schema_keys.go, json_strict.go) and the
// messages a runbook author sees for an Ansible keyword, an undotted
// non-module key and a malformed params.target (FAILURE_PATTERNS 10, 11).
package engine_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
)

// buildJSON compiles payload through the JSON surface.
func buildJSON(t *testing.T, payload string) (*engine.DAG, error) {
	t.Helper()
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatal(err)
	}
	return engine.NewBuilder(eval).Build([]byte(payload))
}

// TestStrictKeys_RefusesUnknownKeyAtEveryLevel covers every place a key
// used to be dropped silently, in both formats, and that each refusal
// names the key, where it sits and, for YAML, its line.
func TestStrictKeys_RefusesUnknownKeyAtEveryLevel(t *testing.T) {
	for _, tc := range []struct {
		name, yaml string
		want       []string
	}{
		{
			name: "metadata key nothing reads",
			yaml: "id: m\nmetadata:\n  description: d\n  mcp: true\ntasks:\n  - name: a\n    fqcn: noop\n",
			want: []string{`unknown key "mcp"`, "under metadata", "(line 4)", "category, description, interruptible, labels, service_effecting"},
		},
		{
			name: "misspelled metadata key",
			yaml: "id: m\nmetadata:\n  servce_effecting: true\ntasks:\n  - name: a\n    fqcn: noop\n",
			want: []string{`"servce_effecting"`, `did you mean "service_effecting"?`},
		},
		{
			name: "secret_mask key",
			yaml: "id: s\ntasks:\n  - name: a\n    fqcn: noop\n    register: r\n  - name: b\n    fqcn: noop\n    secret_mask:\n      register: r\n      fields: [x]\n      feilds: [y]\n",
			want: []string{`unknown key "feilds"`, "under tasks[1].secret_mask", "(line 11)", `did you mean "fields"?`},
		},
		{
			name: "key inside a nested block",
			yaml: "id: n\ntasks:\n  - name: g\n    block:\n      - name: a\n        fqcn: noop\n        secret_mask:\n          register: r\n          fields: [x]\n          extra: 1\n",
			want: []string{`unknown key "extra"`, "under tasks[0].block[0].secret_mask"},
		},
		{
			name: "merge key at task level carrying an unknown key",
			yaml: "id: g\ntasks:\n  - <<: {fqcn: noop, bogus: 1}\n    name: a\n",
			want: []string{`unknown key "bogus"`, "under tasks[0]"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := buildYAML(t, tc.yaml)
			if err == nil {
				t.Fatalf("built, want refused")
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q does not contain %q", err, w)
				}
			}
		})
	}
}

// TestStrictKeys_MergeKeyWithKnownKeysBuilds proves a YAML merge key
// carrying only known keys still builds, so the check does not refuse a
// legal YAML feature along with the unknown keys.
func TestStrictKeys_MergeKeyWithKnownKeysBuilds(t *testing.T) {
	dag, err := buildYAML(t, "id: g\ntasks:\n  - <<: {fqcn: noop, register: r}\n    name: a\n")
	if err != nil {
		t.Fatalf("a merge of known keys was refused: %v", err)
	}
	if got := dag.Nodes["tasks[0]"]; got == nil || got.FQCN != "noop" || got.Register != "r" {
		t.Errorf("merged task = %+v, want fqcn noop and register r", got)
	}
}

// TestStrictKeys_AgreesWithKnownFields is the differential check: for a
// runbook written without module-as-key sugar, the engine must accept or
// refuse exactly as yaml.v3's own strict decoder (KnownFields) does, the
// check the engine cannot call directly on its rewritten tree. Every map
// in a valid runbook gets an extra key in turn, and both must agree on
// each variant, and on the unmodified runbook.
func TestStrictKeys_AgreesWithKnownFields(t *testing.T) {
	const base = `id: diff
name: differential
hosts: web
metadata:
  service_effecting: true
  labels: [a]
check_mode: true
pretasks:
  - name: p
    fqcn: noop
tasks:
  - name: g
    block:
      - name: leaf
        fqcn: noop
        register: r
        params:
          nested: {deep: 1}
      - name: masked
        fqcn: noop
        secret_mask:
          register: r
          fields: [x]
    rescue:
      - name: re
        fqcn: noop
  - name: par
    parallel:
      - name: p1
        fqcn: noop
posttasks:
  - name: z
    fqcn: noop
    lock_acquisition: all_at_plan_time
`
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(base), &root); err != nil {
		t.Fatal(err)
	}
	var maps []*yaml.Node
	var collect func(n *yaml.Node)
	collect = func(n *yaml.Node) {
		if n.Kind == yaml.MappingNode {
			maps = append(maps, n)
		}
		for _, c := range n.Content {
			collect(c)
		}
	}
	collect(&root)

	variants := [][]byte{[]byte(base)}
	for i := range maps {
		var copyRoot yaml.Node
		if err := yaml.Unmarshal([]byte(base), &copyRoot); err != nil {
			t.Fatal(err)
		}
		var copies []*yaml.Node
		var collectCopy func(n *yaml.Node)
		collectCopy = func(n *yaml.Node) {
			if n.Kind == yaml.MappingNode {
				copies = append(copies, n)
			}
			for _, c := range n.Content {
				collectCopy(c)
			}
		}
		collectCopy(&copyRoot)
		copies[i].Content = append(copies[i].Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "zz_added"},
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: "1"})
		out, err := yaml.Marshal(&copyRoot)
		if err != nil {
			t.Fatal(err)
		}
		variants = append(variants, out)
	}

	for i, doc := range variants {
		_, engineErr := buildYAML(t, string(doc))
		dec := yaml.NewDecoder(bytes.NewReader(doc))
		dec.KnownFields(true)
		var def engine.WorkflowDef
		strictErr := dec.Decode(&def)
		if (engineErr == nil) != (strictErr == nil) {
			t.Errorf("variant %d disagrees: engine=%v, KnownFields=%v\n%s", i, engineErr, strictErr, doc)
		}
	}
	if len(variants) < 14 {
		t.Fatalf("only %d variants: the base runbook lost its maps", len(variants))
	}
}

// TestStrictKeys_JSONExactCaseAndDuplicates covers the two ways
// encoding/json forgives a JSON runbook: a key in the wrong case, which it
// would match to a field anyway, and a repeated key, which it would
// silently collapse to its last value.
func TestStrictKeys_JSONExactCaseAndDuplicates(t *testing.T) {
	for _, tc := range []struct {
		name, json string
		want       []string
	}{
		{"field in the wrong case", `{"id":"j","tasks":[{"name":"a","FQCN":"noop"}]}`, []string{`"FQCN"`}},
		{"metadata in the wrong case", `{"id":"j","metadata":{"Description":"d"},"tasks":[{"name":"a","fqcn":"noop"}]}`, []string{`unknown key "Description"`, `did you mean "description"?`}},
		{"duplicate top-level key", `{"id":"j","tasks":[],"tasks":[{"name":"a","fqcn":"noop"}]}`, []string{`duplicate key "tasks"`}},
		{"duplicate task key", `{"id":"j","tasks":[{"name":"a","fqcn":"noop","fqcn":"ssh_exec"}]}`, []string{`duplicate key "fqcn"`, "under tasks[0]"}},
		{"duplicate param key", `{"id":"j","tasks":[{"name":"a","fqcn":"noop","params":{"x":1,"x":2}}]}`, []string{`duplicate key "x"`}},
		{"secret_mask key", `{"id":"j","tasks":[{"name":"a","fqcn":"noop","secret_mask":{"register":"r","fields":["x"],"extra":1}}]}`, []string{`unknown key "extra"`}},
		{"trailing data", `{"id":"j","tasks":[{"name":"a","fqcn":"noop"}]} {"id":"k"}`, []string{"trailing data"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := buildJSON(t, tc.json)
			if err == nil {
				t.Fatalf("built, want refused")
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q does not contain %q", err, w)
				}
			}
		})
	}
	if _, err := buildJSON(t, `{"id":"j","metadata":{"labels":["a"]},"tasks":[{"name":"a","when":"true","fqcn":"noop"}]}`); err != nil {
		t.Errorf("a valid JSON runbook was refused: %v", err)
	}
	if _, err := buildJSON(t, strings.Repeat("[", 600)+strings.Repeat("]", 600)); err == nil || !strings.Contains(err.Error(), "nests deeper") {
		t.Errorf("a 600-deep JSON document = %v, want refused for its depth", err)
	}
}

// TestReservedTaskKeys_MatchTask keeps ReservedTaskKeys and Task's own
// yaml tags one set, the way TestRunbookKeys_MatchWorkflowDef does for the
// top level: a field added without a reserved key would be read as a
// module name, and a reserved key without a field would be dropped.
func TestReservedTaskKeys_MatchTask(t *testing.T) {
	var fromTags []string
	var collect func(typ reflect.Type)
	collect = func(typ reflect.Type) {
		for f := range typ.Fields() {
			name, opts, _ := strings.Cut(f.Tag.Get("yaml"), ",")
			if opts == "inline" {
				collect(f.Type)
				continue
			}
			if f.IsExported() && name != "" && name != "-" {
				fromTags = append(fromTags, name)
			}
		}
	}
	collect(reflect.TypeFor[engine.Task]())
	var fromMap []string
	for k := range engine.ReservedTaskKeys {
		fromMap = append(fromMap, k)
	}
	slices.Sort(fromTags)
	slices.Sort(fromMap)
	if !slices.Equal(fromTags, fromMap) {
		t.Errorf("ReservedTaskKeys = %v, Task's yaml tags = %v", fromMap, fromTags)
	}
}

// TestAnsibleKeywordMessage covers the answer an author gets for writing
// an Ansible keyword a runbook does not have: it names the keyword as an
// Ansible keyword and points at the shipped migration guide, at task
// level, at the top level, and in JSON.
func TestAnsibleKeywordMessage(t *testing.T) {
	const guide = "docs/03-migrating-from-ansible.md"
	for _, tc := range []struct {
		name, payload string
		json          bool
		want          string
	}{
		{"loop beside fqcn", "id: k\ntasks:\n  - name: a\n    fqcn: noop\n    loop: [1, 2]\n", false, `uses "loop", an Ansible keyword`},
		{"with_items beside a module key", "id: k\ntasks:\n  - name: a\n    pkg.apt.install: {name: x}\n    with_items: [a]\n", false, `uses "with_items", an Ansible keyword`},
		{"become alone", "id: k\ntasks:\n  - name: a\n    become: true\n", false, `uses "become", an Ansible keyword`},
		{"notify on a block", "id: k\ntasks:\n  - name: g\n    block:\n      - name: a\n        fqcn: noop\n    notify: h\n", false, `uses "notify", an Ansible keyword`},
		{"play keyword at the top", "id: k\ngather_facts: false\ntasks:\n  - name: a\n    fqcn: noop\n", false, `"gather_facts" (an Ansible keyword`},
		{"json task keyword", `{"id":"k","tasks":[{"name":"a","fqcn":"noop","ignore_errors":true}]}`, true, `uses "ignore_errors", an Ansible keyword`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var err error
			if tc.json {
				_, err = buildJSON(t, tc.payload)
			} else {
				_, err = buildYAML(t, tc.payload)
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), guide) {
				t.Errorf("error = %v, want it to contain %q and cite %s", err, tc.want, guide)
			}
		})
	}
}

// TestUndottedNonModuleKeyRefused covers a lone undotted key that is not
// one of the engine's own actions: it used to become a task calling a
// method of that name, which validation skipped and the run then failed
// on. The engine's own undotted actions still read as module names.
func TestUndottedNonModuleKeyRefused(t *testing.T) {
	_, err := buildYAML(t, "id: u\ntasks:\n  - name: a\n    paramz: {x: 1}\n")
	if err == nil || !strings.Contains(err.Error(), `"paramz" is neither a task key nor a module name`) || !strings.Contains(err.Error(), `did you mean "params"?`) {
		t.Errorf("paramz = %v, want refused with a suggestion", err)
	}
	_, err = buildJSON(t, `{"id":"u","tasks":[{"name":"a","apt":{"name":"x"}}]}`)
	if err == nil || !strings.Contains(err.Error(), `"apt" is neither a task key nor a module name`) {
		t.Errorf("JSON apt = %v, want refused", err)
	}
	for _, action := range []string{"noop", "ssh_exec", "set_metadata"} {
		payload := "id: u\ntasks:\n  - name: a\n    " + action + ": {data: {k: v}}\n"
		if _, err := buildYAML(t, payload); err != nil {
			t.Errorf("engine action %s written as a key = %v, want it built", action, err)
		}
	}
}

// TestBuild_RefusesMalformedTarget is FAILURE_PATTERNS 11's regression
// test: a present params.target that is not a non-empty string is refused
// when the runbook is built, naming the kind of value and never the value,
// instead of silently running the task against hosts:.
func TestBuild_RefusesMalformedTarget(t *testing.T) {
	const secret = "s3cr3t-target-value"
	for _, tc := range []struct {
		name, target, kind string
	}{
		{"list", "[" + secret + ", web2]", "a list"},
		{"map", "{host: " + secret + "}", "a map"},
		{"number", "42", "a number"},
		{"boolean", "true", "a boolean"},
		{"null", "null", "null"},
		{"empty string", `""`, "an empty string"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := "id: t\nhosts: web\ntasks:\n  - name: a\n    fqcn: noop\n    params:\n      target: " + tc.target + "\n"
			_, err := buildYAML(t, payload)
			if err == nil {
				t.Fatal("built, want refused")
			}
			if !strings.Contains(err.Error(), "sets params.target to "+tc.kind) {
				t.Errorf("error %q does not name %s", err, tc.kind)
			}
			if strings.Contains(err.Error(), secret) {
				t.Errorf("error %q prints the target's value", err)
			}
		})
	}
	if _, err := buildJSON(t, `{"id":"t","tasks":[{"name":"a","fqcn":"noop","params":{"target":1.5}}]}`); err == nil || !strings.Contains(err.Error(), "a number") {
		t.Errorf("JSON float target = %v, want refused as a number", err)
	}
	if _, err := buildYAML(t, "id: t\ntasks:\n  - name: a\n    fqcn: noop\n    params:\n      target: web1\n"); err != nil {
		t.Errorf("a string target was refused: %v", err)
	}
}

// TestStrictKeys_AliasFanOutIsCheckedOnce proves the walker visits a node
// shared by many aliases once, so an alias bomb under params costs the
// check nothing before the decoder's own alias budget refuses it.
func TestStrictKeys_AliasFanOutIsCheckedOnce(t *testing.T) {
	var b strings.Builder
	b.WriteString("id: bomb\ntasks:\n  - name: a\n    fqcn: noop\n    params:\n      l0: &l0 [1, 2]\n")
	for i := 1; i <= 12; i++ {
		refs := make([]string, 8)
		for j := range refs {
			refs[j] = "*l" + strconv.Itoa(i-1)
		}
		fmt.Fprintf(&b, "      l%d: &l%d [%s]\n", i, i, strings.Join(refs, ", "))
	}
	start := time.Now()
	_, err := buildYAML(t, b.String())
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("an alias fan-out took %v to refuse", elapsed)
	}
	if err == nil {
		t.Error("an alias bomb built, want the decoder's alias budget to refuse it")
	}
}

// TestImportTasks_StrictKeysAndContainment covers an imported task file:
// its keys get the same strict check, and a symlink inside the runbook's
// directory cannot read a file outside it.
func TestImportTasks_StrictKeysAndContainment(t *testing.T) {
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	dir := t.TempDir()
	write := func(path, body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	runbook := filepath.Join(dir, "main.yaml")
	write(runbook, "id: imp\ntasks:\n  - name: pull\n    import_tasks:\n      file: sub.yaml\n")

	write(filepath.Join(dir, "sub.yaml"), "- name: a\n  fqcn: noop\n  secret_mask: {register: r, fields: [x], bogus: 1}\n")
	if _, err := engine.NewBuilder(eval).BuildFromYAMLFile(runbook); err == nil || !strings.Contains(err.Error(), `unknown key "bogus"`) {
		t.Errorf("an unknown key in an imported file = %v, want refused", err)
	}

	write(filepath.Join(outside, "tasks.yaml"), "- name: escaped\n  fqcn: noop\n")
	if err := os.Remove(filepath.Join(dir, "sub.yaml")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "tasks.yaml"), filepath.Join(dir, "sub.yaml")); err != nil {
		t.Skipf("cannot create a symlink here: %v", err)
	}
	if _, err := engine.NewBuilder(eval).BuildFromYAMLFile(runbook); err == nil {
		t.Error("an import through a symlink out of the runbook's directory built, want refused")
	}
}
