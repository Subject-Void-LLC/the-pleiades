// Tests for the translator's parts: Ansible's YAML 1.1 scalars, free-form
// arguments, the variable rule, and the parser's limits.
package playbook

import (
	"reflect"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// scalar parses one YAML scalar as written.
func scalar(t *testing.T, text string) *yaml.Node {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte("v: "+text), &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Content[0].Content[1]
}

// TestYAML11Scalars covers the values Ansible and a runbook read
// differently, converted into each native type or refused.
func TestYAML11Scalars(t *testing.T) {
	for _, tc := range []struct {
		text, typ, param string
		want             any
		refused          bool
	}{
		{"yes", "bool", "x", true, false},
		{"Off", "bool", "x", false, false},
		{`"yes"`, "bool", "x", true, false},
		{"yes", "string", "x", nil, true},
		{`"yes"`, "string", "x", "yes", false},
		{"0644", "string", "mode", "0644", false},
		{"644", "string", "mode", nil, true},
		{`"644"`, "string", "mode", "644", false},
		{"0644", "string", "path", nil, true},
		{"1:30", "int", "x", nil, true},
		{"1:30", "string", "x", nil, true},
		{"80", "string", "port", "80", false},
		{"80", "int", "port", int64(80), false},
		{"0x1F", "int", "x", int64(31), false},
		{"1.10", "string", "version", "1.10", false},
		{"~", "string", "x", nil, true},
		{"[a, b]", "list of string", "x", []any{"a", "b"}, false},
		{"[yes]", "list of string", "x", nil, true},
		{"{a: 1}", "string", "x", nil, true},
		{"a", "list of string", "x", []any{"a"}, false},
		{"a,b", "list of string", "x", nil, true},
		{"on", "list of string", "x", nil, true},
		{"t", "bool", "x", true, false},
		{"1", "bool", "x", true, false},
		{"2", "bool", "x", nil, true},
		{"01", "bool", "x", nil, true},
		{"017", "int", "x", int64(15), false},
		{`"12"`, "int", "x", int64(12), false},
		{"1.5", "int", "x", nil, true},
		{"1.5", "float", "x", 1.5, false},
		{"2", "float", "x", 2.0, false},
		{"on", "string", "x", nil, true},
		{"[1, 2]", "int or list of int", "x", []any{int64(1), int64(2)}, false},
		{"[a]", "int", "x", nil, true},
		{"{a: 1}", "dict", "x", map[string]any{"a": int64(1)}, false},
	} {
		t.Run(tc.text+"_"+tc.typ+"_"+tc.param, func(t *testing.T) {
			got, err := toNative(scalar(t, tc.text), tc.typ, tc.param)
			if tc.refused {
				if err == nil {
					t.Errorf("accepted as %#v, want refused", got)
				}
				return
			}
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Errorf("= %#v, %v; want %#v", got, err, tc.want)
			}
		})
	}
}

// TestKVArgs covers Ansible's free-form splitting: quotes, {{ }}, a
// command's own keys, and what is refused.
func TestKVArgs(t *testing.T) {
	args, raw, err := parseKV(`name="a b" state=present`, nil)
	if err != nil || args["name"] != "a b" || args["state"] != "present" || raw != "" {
		t.Errorf("k=v = %v %q %v", args, raw, err)
	}
	command := map[string]bool{"creates": true, "chdir": true}
	args, raw, err = parseKV(`make -j "{{ n }} x" TARGET=all creates=/tmp/done chdir=/src`, command)
	if err != nil || args["creates"] != "/tmp/done" || args["chdir"] != "/src" || raw != `make -j "{{ n }} x" TARGET=all` {
		t.Errorf("command = %v %q %v", args, raw, err)
	}
	for _, bad := range []string{`name="unterminated`, `x={{ open`, `just words`} {
		if _, _, err := parseKV(bad, nil); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}

// FuzzKVArgs never panics, and a command's free-form text survives a
// split and join with nothing lost but whitespace between tokens.
func FuzzKVArgs(f *testing.F) {
	for _, seed := range []string{`a=b c="d e"`, `echo "x y" creates=/z`, `{{ a }} b={{ c }}`, `'\''`, `a=`} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		_, _, _ = parseKV(s, nil)
		args, raw, err := parseKV(s, map[string]bool{"creates": true})
		if err != nil {
			return
		}
		joined := strings.Join(strings.Fields(raw), "")
		if _, ok := args["creates"]; !ok && joined != strings.Join(strings.Fields(s), "") {
			t.Fatalf("free-form text %q became %q", s, raw)
		}
	})
}

// TestVars_ExactlyOneValue covers the variable rule: one literal
// definition resolves; two, a run-time one, an undefined one, a fact, a
// secret-shaped name and an unreadable source do not.
func TestVars_ExactlyOneValue(t *testing.T) {
	ix := newVarIndex()
	var vars yaml.Node
	if err := yaml.Unmarshal([]byte("one: a\ntwo: b\napi_token: t\n"), &vars); err != nil {
		t.Fatal(err)
	}
	ix.addLiterals(vars.Content[0], "pb.yml")
	ix.addLiterals(vars.Content[0], "pb.yml")
	ix.defs["one"] = ix.defs["one"][:1]
	ix.addRuntime("later", Position{})
	for _, tc := range []struct {
		name string
		code Code
	}{
		{"one", ""},
		{"two", "template.unresolved"},
		{"later", "template.unresolved"},
		{"nowhere", "template.unresolved"},
		{"ansible_facts", "template.fact"},
		{"api_token", "template.secret"},
	} {
		_, rerr := ix.literal(tc.name)
		switch {
		case tc.code == "" && rerr != nil:
			t.Errorf("%s refused: %v", tc.name, rerr)
		case tc.code != "" && (rerr == nil || rerr.code != tc.code):
			t.Errorf("%s = %v, want %s", tc.name, rerr, tc.code)
		}
	}
	ix.unknownSource = "include_vars"
	if _, rerr := ix.literal("one"); rerr == nil {
		t.Error("a variable resolved while an unread source could also set it")
	}
}

// TestParse_Limits covers the parser's bounds: an alias bomb and deep
// nesting are refused before any translation.
func TestParse_Limits(t *testing.T) {
	var bomb strings.Builder
	bomb.WriteString("- a0: &a0 [x, x, x, x, x, x, x, x, x, x]\n")
	for i := 1; i < 8; i++ {
		prev := strings.Repeat("*a"+string(rune('0'+i-1))+", ", 10)
		bomb.WriteString("  a" + string(rune('0'+i)) + ": &a" + string(rune('0'+i)) + " [" + strings.TrimSuffix(prev, ", ") + "]\n")
	}
	if _, err := parseYAML([]byte(bomb.String()), "bomb.yml"); err == nil || !strings.Contains(err.Error(), "expands to more than") {
		t.Errorf("alias bomb = %v, want refused for its expanded size", err)
	}
	deep := strings.Repeat("[", 300) + strings.Repeat("]", 300)
	if _, err := parseYAML([]byte(deep), "deep.yml"); err == nil {
		t.Error("a 300-deep document was accepted")
	}
}

// TestClassify_ImperativeModulesNeverAsserted pins the rule that a module
// with no desired state is never declared asserted: running a command is
// the only way to learn what it does, whatever guard it carries.
func TestClassify_ImperativeModulesNeverAsserted(t *testing.T) {
	imperative := map[string]bool{"ansible.builtin.command": true, "ansible.builtin.shell": true, "ansible.builtin.raw": true, "ansible.netcommon.cli_command": true, "ansible.netcommon.cli_config": true}
	for _, e := range Entries() {
		if !imperative[e.Module] {
			continue
		}
		calls := []*Call{e.Default}
		for _, s := range e.Selectors {
			for _, c := range s.Choices {
				calls = append(calls, c.Call)
			}
		}
		for _, c := range calls {
			if c != nil && c.Class != ClassImperative {
				t.Errorf("%s maps to %s as %s, want imperative", e.Module, c.FQCN, c.Class)
			}
		}
	}
}
