// Black-box tests for Translate: what a conversion writes, what it
// reports, and what it never lets through.
package playbook_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"go.yaml.in/yaml/v3"

	_ "github.com/Subject-Void-LLC/the-pleiades/internal/catalog"
	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/playbook"
	"github.com/Subject-Void-LLC/the-pleiades/internal/validate"
)

// convert translates one in-memory playbook.
func convert(t *testing.T, playbookYAML string, extra ...string) playbook.Result {
	t.Helper()
	fsys := fstest.MapFS{"site.yml": {Data: []byte(playbookYAML)}}
	for i := 0; i+1 < len(extra); i += 2 {
		fsys[extra[i]] = &fstest.MapFile{Data: []byte(extra[i+1])}
	}
	res, err := playbook.Translate(fsys, "site.yml", playbook.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// build compiles a runbook with the engine's own builder.
func build(t *testing.T, yamlText []byte) *engine.DAG {
	t.Helper()
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatal(err)
	}
	dag, err := engine.NewBuilder(eval).BuildFromYAML(yamlText)
	if err != nil {
		t.Fatalf("does not build: %v\n%s", err, yamlText)
	}
	return dag
}

// codesOf returns the report's finding codes.
func codesOf(r playbook.Report) []playbook.Code {
	var out []playbook.Code
	for _, f := range r.Findings {
		out = append(out, f.Code)
	}
	return out
}

// TestTranslate_DAGIdenticalToHandAuthored is the phase's Adversarial
// Pattern Justification: the conversion of testdata/twin/site.yml,
// built by the engine's own BuildFromYAML, is the same DAG as the runbook
// a person writes by hand in native syntax for it. Version hashes the
// whole resolved definition, so equal versions mean equal runbooks; the
// graph's shape is compared too, so a failure says where they differ.
func TestTranslate_DAGIdenticalToHandAuthored(t *testing.T) {
	res, err := playbook.Translate(os.DirFS("testdata/twin"), "site.yml", playbook.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Runbooks) != 1 || res.Runbooks[0].Incomplete {
		t.Fatalf("runbooks = %+v, want one complete runbook; findings %v", res.Runbooks, codesOf(res.Report))
	}
	twin, err := os.ReadFile("testdata/twin/site.native.yaml")
	if err != nil {
		t.Fatal(err)
	}
	got, want := build(t, res.Runbooks[0].YAML), build(t, twin)
	if got.EntryPoint != want.EntryPoint {
		t.Errorf("entry point %s, hand-written %s", got.EntryPoint, want.EntryPoint)
	}
	if !slices.Equal(sortedKeys(got.Nodes), sortedKeys(want.Nodes)) {
		t.Errorf("nodes %v, hand-written %v", sortedKeys(got.Nodes), sortedKeys(want.Nodes))
	}
	if !slices.Equal(sortedKeys(got.Conditions), sortedKeys(want.Conditions)) {
		t.Errorf("conditions %v, hand-written %v", sortedKeys(got.Conditions), sortedKeys(want.Conditions))
	}
	for id, edges := range want.Adjacency {
		if !slices.Equal(edges, got.Adjacency[id]) {
			t.Errorf("edges from %s: %v, hand-written %v", id, got.Adjacency[id], edges)
		}
	}
	if got.Version != want.Version {
		t.Errorf("version %s, hand-written %s\n%s", got.Version, want.Version, res.Runbooks[0].YAML)
	}
}

// sortedKeys returns m's keys in order.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// TestReport_EachConstructExactlyOnce proves a construct is reported once
// however many tasks it lands in: a loop of three items carrying notify
// and become gives one of each, and no two findings share a code and a
// position.
func TestReport_EachConstructExactlyOnce(t *testing.T) {
	res := convert(t, `- hosts: web
  gather_facts: false
  tasks:
    - name: each
      command: echo {{ item }}
      loop: [a, b, c]
      become: true
      notify: restart
  handlers:
    - name: restart
      command: /bin/true
`)
	count := map[playbook.Code]int{}
	seen := map[string]bool{}
	for _, f := range res.Report.Findings {
		count[f.Code]++
		key := string(f.Code) + " " + f.At.String()
		if seen[key] {
			t.Errorf("reported twice: %s", key)
		}
		seen[key] = true
	}
	for _, code := range []playbook.Code{"loop.unrolled", "keyword.become", "keyword.notify", "handler.dropped"} {
		if count[code] != 1 {
			t.Errorf("%s reported %d times, want once: %v", code, count[code], codesOf(res.Report))
		}
	}
	if res.Report.Counts.Tasks != 3 {
		t.Errorf("tasks = %d, want the loop's 3", res.Report.Counts.Tasks)
	}
}

// TestReport_NoValues is the never-show-secrets rule applied to a
// conversion: a variable named like a secret, a value shaped like one and
// a vault value are neither written into the runbook nor printed in the
// report, in either view; each task using one is blocked instead.
func TestReport_NoValues(t *testing.T) {
	const plain, url, vault = "SENTINEL-PLAIN-hunter2", "SENTINEL-URLPASS", "SENTINELVAULT0123456789"
	res := convert(t, `- hosts: web
  gather_facts: false
  vars:
    db_password: `+plain+`
    repo: "https://deploy:`+url+`@example.com/repo.git"
    token_value: !vault |
      $ANSIBLE_VAULT;1.1;AES256
      `+vault+`
  tasks:
    - name: use the password
      command: echo {{ db_password }}
    - name: use the url
      command: git clone {{ repo }}
    - name: use the vault
      command: echo {{ token_value }}
`)
	text := res.Report.String()
	js, err := json.Marshal(res.Report)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{plain, url, vault} {
		for _, rb := range res.Runbooks {
			if strings.Contains(string(rb.YAML), secret) {
				t.Errorf("%s reached runbook %s", secret, rb.File)
			}
		}
		if strings.Contains(text, secret) || strings.Contains(string(js), secret) {
			t.Errorf("%s reached the report", secret)
		}
	}
	if res.Report.Counts.Blocked != 3 {
		t.Errorf("blocked = %d, want all three tasks: %v", res.Report.Counts.Blocked, codesOf(res.Report))
	}
}

// TestReport_RefusedValuesNotPrinted covers values refused as ambiguous
// rather than as secret: an octal-looking number where text is meant, a
// decimal mode, a base-60 number and a text Ansible would split at its
// commas. Each finding describes the value's kind and points at it; none
// prints it, since a refused value can still be one a person would not
// want in a report (a PIN written as 0123).
func TestReport_RefusedValuesNotPrinted(t *testing.T) {
	const octal, decimal, base60, comma = "0123456701", "1234567", "12:34:56", "SENTINELCOMMA"
	const state, notInt, token, kind, field = "SENTINELSTATE", "SENTINELINT", "SENTINELTOKEN", "SENTINELKIND", "SENTINELFIELD"
	res := convert(t, `- hosts: web
  gather_facts: false
  vars: {word: `+notInt+`, count: 3}
  tasks:
    - {copy: {dest: /x, content: `+octal+`}}
    - {file: {path: /x, state: directory, mode: `+decimal+`}}
    - {lineinfile: {path: /x, line: `+base60+`}}
    - {ios_config: {lines: "enable secret 5 `+comma+`,x"}}
    - {service: {name: nginx, state: `+state+`}}
    - {command: probe, register: probe}
    - {command: x, when: "word | int == 1"}
    - {command: x, when: "count == 3 '`+token+`'"}
    - {command: x, when: "count < '`+kind+`'"}
    - {command: x, when: "probe.rc == '`+field+`'"}
`)
	js, err := json.Marshal(res.Report)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{octal, decimal, base60, comma, state, notInt, token, kind, field} {
		if strings.Contains(res.Report.String(), value) || strings.Contains(string(js), value) {
			t.Errorf("%s reached the report", value)
		}
	}
	if res.Report.Counts.Blocked != 9 {
		t.Errorf("blocked = %d, want every task but the probe: %v", res.Report.Counts.Blocked, codesOf(res.Report))
	}
}

// TestEmit_CommentInjection proves text from the playbook cannot become
// YAML of its own, or reach a terminal: a file name and a task name
// carrying a newline, a task of their own and an escape sequence leave the
// runbook's tasks exactly as converted, and the written file holds no
// control character for a later cat to send to a terminal. (yaml.v3 itself
// comment-prefixes every line of a multi-line comment, which stops the
// injection; the escaping is what keeps the control character out.)
func TestEmit_CommentInjection(t *testing.T) {
	const evil = "evil\x1b[2J\n- fqcn: exec.shell\n#.yml"
	fsys := fstest.MapFS{evil: {Data: []byte(`- hosts: web
  gather_facts: false
  tasks:
    - name: "first\n- name: injected\n  fqcn: exec.shell"
      command: /bin/true
    - name: second
      frobnicate: {}
`)}}
	res, err := playbook.Translate(fsys, evil, playbook.Options{})
	if err != nil {
		t.Fatal(err)
	}
	dag := build(t, res.Runbooks[0].YAML)
	for id, task := range dag.Nodes {
		if task.FQCN == "exec.shell" {
			t.Errorf("node %s runs exec.shell, which the playbook never asked for:\n%s", id, res.Runbooks[0].YAML)
		}
	}
	for _, line := range strings.Split(string(res.Runbooks[0].YAML), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "- fqcn: exec.shell") {
			t.Errorf("an injected line reached the runbook: %q", line)
		}
	}
	if strings.ContainsRune(string(res.Runbooks[0].YAML), 0x1b) {
		t.Error("a raw escape character reached the runbook file")
	}
	if strings.ContainsRune(res.Report.String(), 0x1b) {
		t.Error("a raw escape character reached the report")
	}
}

// TestEmit_GuardFirst proves an incomplete conversion cannot run: its
// file says so, its first task is the guard, and the platform's own
// validation refuses the guard and every placeholder.
func TestEmit_GuardFirst(t *testing.T) {
	res := convert(t, `- hosts: web
  gather_facts: false
  tasks:
    - name: fine
      command: /bin/true
    - name: unknown
      frobnicate: {size: 3}
`)
	rb := res.Runbooks[0]
	if !rb.Incomplete || !strings.HasSuffix(rb.File, ".incomplete.yaml") {
		t.Fatalf("runbook %s incomplete=%v, want an incomplete file", rb.File, rb.Incomplete)
	}
	dag := build(t, rb.YAML)
	if dag.Nodes["tasks[0]"] == nil || dag.Nodes["tasks[0]"].FQCN != engine.IncompleteGuard {
		t.Errorf("first task is %+v, want the guard", dag.Nodes["tasks[0]"])
	}
	report := validate.Validate(validate.WorldView{DAG: dag})
	text := report.String()
	for _, want := range []string{"the guard an incomplete conversion starts with", "(frobnicate)"} {
		if !strings.Contains(text, want) {
			t.Errorf("validation does not refuse %s:\n%s", want, text)
		}
	}
	if strings.Contains(string(rb.YAML), "size") {
		t.Error("the placeholder copied the task's arguments")
	}
}

// TestReport_EmittedPositionsPointAtTheirTask proves every task's emitted
// position is the line its task starts on in the written runbook, which is
// what an editor needs to put a finding on the right line.
func TestReport_EmittedPositionsPointAtTheirTask(t *testing.T) {
	res, err := playbook.Translate(os.DirFS("testdata/twin"), "site.yml", playbook.Options{})
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(res.Runbooks[0].YAML), "\n")
	for _, task := range res.Report.Tasks {
		if task.Emitted.Line == 0 {
			t.Errorf("%s has no emitted position", task.NodeID)
			continue
		}
		line := lines[task.Emitted.Line-1]
		var got map[string]any
		if err := yaml.Unmarshal([]byte(strings.TrimPrefix(strings.TrimSpace(line), "- ")), &got); err != nil || got["name"] != task.Name {
			t.Errorf("%s: line %d is %q, want its task %q", task.NodeID, task.Emitted.Line, line, task.Name)
		}
	}
	for _, f := range res.Report.Findings {
		if f.Code == "loop.unrolled" && f.Emitted == nil {
			t.Error("the loop's finding has no emitted position")
		}
	}
}

// TestTranslate_Plays covers grouping: plays sharing hosts become one
// runbook with a block each, a change of hosts starts a second one, and a
// host pattern blocks its runbook.
func TestTranslate_Plays(t *testing.T) {
	res := convert(t, `- name: one
  hosts: web
  gather_facts: false
  tasks: [{name: a, command: /bin/true}]
- name: two
  hosts: web
  gather_facts: false
  tasks: [{name: b, command: /bin/true}]
- name: three
  hosts: db
  gather_facts: false
  tasks: [{name: c, command: /bin/true}]
- name: four
  hosts: "web:&prod"
  gather_facts: false
  tasks: [{name: d, command: /bin/true}]
`)
	if len(res.Runbooks) != 3 {
		t.Fatalf("got %d runbooks, want 3", len(res.Runbooks))
	}
	first := build(t, res.Runbooks[0].YAML)
	if first.Nodes["tasks[0].block[0]"] == nil || first.Nodes["tasks[1].block[0]"] == nil || first.Hosts != "web" {
		t.Errorf("the two web plays are not one block each: %v", sortedKeys(first.Nodes))
	}
	if res.Runbooks[1].Incomplete || !res.Runbooks[2].Incomplete {
		t.Errorf("incomplete = %v, %v; want only the pattern's runbook", res.Runbooks[1].Incomplete, res.Runbooks[2].Incomplete)
	}
	if !slices.Contains(codesOf(res.Report), "play.split") || !slices.Contains(codesOf(res.Report), "play.hosts_pattern") {
		t.Errorf("findings %v lack play.split and play.hosts_pattern", codesOf(res.Report))
	}
}

// TestTranslate_ImportTasks covers import_tasks: a file inside the
// playbook's directory is inlined as a block; one that imports itself,
// one named by a template, and one reached through a symlink out of the
// directory are refused.
func TestTranslate_ImportTasks(t *testing.T) {
	res := convert(t, `- hosts: web
  gather_facts: false
  tasks:
    - import_tasks: sub.yml
    - import_tasks: loop.yml
    - import_tasks: "{{ which }}.yml"
`, "sub.yml", "- name: inner\n  command: /bin/true\n", "loop.yml", "- import_tasks: loop.yml\n")
	dag := build(t, res.Runbooks[0].YAML)
	var names []string
	for _, task := range dag.Nodes {
		names = append(names, task.Name)
	}
	if !slices.Contains(names, "inner") {
		t.Errorf("sub.yml was not inlined: %v", names)
	}
	if n := strings.Count(strings.Join(slices.Collect(func(yield func(string) bool) {
		for _, f := range res.Report.Findings {
			yield(string(f.Code))
		}
	}), " "), "import.tasks_missing"); n != 2 {
		t.Errorf("import.tasks_missing raised %d times, want the cycle and the template: %v", n, codesOf(res.Report))
	}

	dir, outside := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "x.yml"), []byte("- name: escaped\n  command: /bin/true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "x.yml"), filepath.Join(dir, "x.yml")); err != nil {
		t.Skip(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "site.yml"), []byte("- hosts: web\n  gather_facts: false\n  tasks:\n    - import_tasks: x.yml\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	escaped, err := playbook.Translate(root.FS(), "site.yml", playbook.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(escaped.Runbooks[0].YAML), "escaped") {
		t.Error("an import through a symlink out of the playbook's directory was inlined")
	}
}

// TestTranslate_ImportTasksResolvesAsAnsible proves a nested import's
// file is looked for beside the file importing it first, as Ansible looks
// for a role's tasks/setup.yml from its tasks/main.yml, and then in the
// playbook's own directory.
func TestTranslate_ImportTasksResolvesAsAnsible(t *testing.T) {
	res := convert(t, `- hosts: web
  gather_facts: false
  tasks:
    - import_tasks: tasks/main.yml
`, "tasks/main.yml", "- import_tasks: setup.yml\n- import_tasks: common.yml\n",
		"tasks/setup.yml", "- name: beside\n  command: /bin/true\n",
		"common.yml", "- name: at the root\n  command: /bin/true\n")
	dag := build(t, res.Runbooks[0].YAML)
	var names []string
	for _, task := range dag.Nodes {
		names = append(names, task.Name)
	}
	for _, want := range []string{"beside", "at the root"} {
		if !slices.Contains(names, want) {
			t.Errorf("%q was not inlined: %v; findings %v", want, names, codesOf(res.Report))
		}
	}
}

// TestReport_CodesAreClosedAndDocumented proves every code the tables can
// raise is one codes.go documents.
func TestReport_CodesAreClosedAndDocumented(t *testing.T) {
	docs := playbook.Codes()
	for code, doc := range docs {
		if doc.Summary == "" {
			t.Errorf("code %s has no summary", code)
		}
	}
	for _, e := range playbook.Entries() {
		used := []playbook.Code{e.Code}
		for _, a := range e.Args {
			used = append(used, a.Code)
		}
		for _, s := range e.Selectors {
			for _, c := range s.Choices {
				used = append(used, c.Code)
			}
		}
		for _, code := range used {
			if _, ok := docs[code]; code != "" && !ok {
				t.Errorf("%s raises %q, which codes.go does not document", e.Module, code)
			}
		}
	}
}

// TestReport_TextAndJSONSameModel proves the text view says what the JSON
// view says: every finding, and the same counts.
func TestReport_TextAndJSONSameModel(t *testing.T) {
	res, err := playbook.Translate(os.DirFS("testdata/twin"), "site.yml", playbook.Options{})
	if err != nil {
		t.Fatal(err)
	}
	text := res.Report.String()
	for _, f := range res.Report.Findings {
		if !strings.Contains(text, f.ID+" ") {
			t.Errorf("text lacks finding %s", f.ID)
		}
	}
	var decoded playbook.Report
	js, _ := json.Marshal(res.Report)
	if err := json.Unmarshal(js, &decoded); err != nil {
		t.Fatalf("the JSON view does not decode: %v", err)
	}
	if decoded.SchemaVersion != playbook.SchemaVersion || decoded.Counts != res.Report.Counts {
		t.Errorf("decoded counts %+v, want %+v", decoded.Counts, res.Report.Counts)
	}
}
