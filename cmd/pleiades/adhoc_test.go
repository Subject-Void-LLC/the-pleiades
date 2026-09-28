// Tests for adhoc's parameters and the runbook it builds from them.
package main

import (
	"reflect"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
)

func TestParseAdhocParams(t *testing.T) {
	got, err := parseAdhocParams([]string{
		"command=echo a=b", "port=2222", "force=true", "version=15.2",
		"mode:='0644'", "env:={LANG: C, N: 1}", "names:=[a, b]", "empty=", "nothing:=",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"command": "echo a=b", "port": 2222, "force": true, "version": "15.2",
		"mode": "0644", "env": map[string]any{"LANG": "C", "N": 1}, "names": []any{"a", "b"},
		"empty": "", "nothing": nil,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %#v\nwant %#v", got, want)
	}

	for _, bad := range [][]string{
		{"justtext"},
		{"=value"},
		{"two words=x"},
		{"a-b=x"},
		{"x=1", "x=2"},
		{"x=1", "x:=2"},
		{"env:={unclosed"},
	} {
		if _, err := parseAdhocParams(bad, nil); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}

func TestAdhocRunbookBuildsOneTask(t *testing.T) {
	params := map[string]any{"command": "uptime", "env": map[string]any{"LANG": "C"}}
	payload, err := adhocRunbook("web", "exec.command", params)
	if err != nil {
		t.Fatal(err)
	}
	dag := buildForPrint(t, string(payload), engine.TagFilter{})
	if dag.ID != adhocRunbookID || dag.Hosts != "web" || len(dag.Nodes) != 1 {
		t.Fatalf("dag %q on %q with %d nodes, from:\n%s", dag.ID, dag.Hosts, len(dag.Nodes), payload)
	}
	task := dag.Nodes["tasks[0]"]
	if task == nil || task.FQCN != "exec.command" || !reflect.DeepEqual(task.Params, params) {
		t.Fatalf("task %+v, from:\n%s", task, payload)
	}

	// No parameters is a method key with nothing under it, as a person
	// writes `virt.vbox.vm.list:` in a runbook.
	payload, err = adhocRunbook("lab", "facts.gather", nil)
	if err != nil {
		t.Fatal(err)
	}
	if dag := buildForPrint(t, string(payload), engine.TagFilter{}); dag.Nodes["tasks[0]"].FQCN != "facts.gather" {
		t.Fatalf("from:\n%s", payload)
	}
}

func TestAdhocRunbookRefusals(t *testing.T) {
	for _, c := range []struct{ hosts, method, want string }{
		{"", "exec.command", "a device or a tag"},
		{"web", "", "a method"},
		{"web", "when", "task keyword"},
		{"web", "name", "task keyword"},
		{"web", "block", "task keyword"},
		{"web", "import_tasks", "task keyword"},
	} {
		if _, err := adhocRunbook(c.hosts, c.method, nil); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q %q: %v, want an error naming %q", c.hosts, c.method, err, c.want)
		}
	}
}

// TestAdhocRunbookKeepsItsShape feeds adhocRunbook values meant to break
// out of the runbook it builds: a hosts value carrying YAML, a parameter
// value carrying a second task. Each must come back as the one string it
// was, in the one place it was put.
func TestAdhocRunbookKeepsItsShape(t *testing.T) {
	hosts := "web\ntasks:\n  - noop:"
	value := "x\n  - name: second\n    exec.command:\n      command: rm -rf /"
	payload, err := adhocRunbook(hosts, "exec.command", map[string]any{"command": value})
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Hosts string           `yaml:"hosts"`
		Tasks []map[string]any `yaml:"tasks"`
	}
	if err := yaml.Unmarshal(payload, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Hosts != hosts || len(doc.Tasks) != 1 {
		t.Fatalf("hosts %q and %d tasks, from:\n%s", doc.Hosts, len(doc.Tasks), payload)
	}
	if got := doc.Tasks[0]["exec.command"].(map[string]any)["command"]; got != value {
		t.Fatalf("command came back as %q", got)
	}
}

// FuzzAdhocParams holds parseAdhocParams and adhocRunbook to their
// contract for any input: either an error, or a runbook that decodes to
// exactly one task carrying exactly the parameters parsed.
func FuzzAdhocParams(f *testing.F) {
	for _, seed := range []string{"command=uptime", "env:={A: 1}", "x:=[1, 2]", "a=b=c", "k:=", "=", "z:=!!binary x"} {
		f.Add("web", "exec.command", seed)
	}
	f.Fuzz(func(t *testing.T, hosts, method, token string) {
		params, err := parseAdhocParams([]string{token}, nil)
		if err != nil {
			return
		}
		payload, err := adhocRunbook(hosts, method, params)
		if err != nil {
			return
		}
		var doc struct {
			Hosts string           `yaml:"hosts"`
			Tasks []map[string]any `yaml:"tasks"`
		}
		if err := yaml.Unmarshal(payload, &doc); err != nil {
			t.Fatalf("the built runbook does not decode: %v\n%s", err, payload)
		}
		if doc.Hosts != hosts || len(doc.Tasks) != 1 || len(doc.Tasks[0]) != 2 {
			t.Fatalf("the runbook changed shape:\n%s", payload)
		}
	})
}

// TestParseAdhocParams_KeepsWhatAMethodDeclaresAString: file.permissions
// declares mode a string, so mode=0755 is the text its author typed. Typed
// as a number, the method refuses it (rightly: 0755 read as a number is
// 755, not the mode meant), which was the first thing a user met running a
// file method ad hoc. A parameter declared anything else keeps the usual
// typing, and a method not yet registered gets it throughout.
func TestParseAdhocParams_KeepsWhatAMethodDeclaresAString(t *testing.T) {
	declared := declaredTypes("file.permissions")
	if declared["mode"] != "string" {
		t.Fatalf("file.permissions declares mode as %q; this test assumes a string", declared["mode"])
	}
	got, err := parseAdhocParams([]string{"path=/srv/app", "mode=0755", "owner=1000"}, declared)
	if err != nil {
		t.Fatal(err)
	}
	if got["mode"] != "0755" || got["owner"] != "1000" {
		t.Errorf("declared strings were typed: %#v", got)
	}
	ports, err := parseAdhocParams([]string{"port=8443"}, declaredTypes("fw.firewalld.allow"))
	if err != nil || ports["port"] != 8443 {
		t.Errorf("a declared int: %#v, %v", ports, err)
	}
	if declaredTypes("no.such.method") != nil {
		t.Error("an unknown method declared types")
	}
}
