//go:build integration

// Differential test: the module tables against ansible-core's own
// documentation, from ansible-doc in the repository's Ansible runner image.
// Only ansible.builtin modules ship with ansible-core, so the entries from
// ansible.posix, ansible.netcommon and cisco.ios are not checked here.
package playbook_test

import (
	"encoding/json"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/playbook"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
)

// ansibleOption is the part of an ansible-doc option the tables rely on.
// Choices is a list, or a map from each choice to its description.
type ansibleOption struct {
	Aliases []string        `json:"aliases"`
	Choices json.RawMessage `json:"choices"`
	Default any             `json:"default"`
	Type    string          `json:"type"`
}

// choices returns o's choices as a selector writes them.
func (o ansibleOption) choices(t *testing.T) []string {
	t.Helper()
	var list []any
	if err := json.Unmarshal(o.Choices, &list); err == nil {
		out := make([]string, len(list))
		for i, c := range list {
			out[i] = defaultText(c)
		}
		return out
	}
	var described map[string]any
	if err := json.Unmarshal(o.Choices, &described); err != nil && len(o.Choices) > 0 && string(o.Choices) != "null" {
		t.Fatalf("choices %s are neither a list nor a map", o.Choices)
	}
	var out []string
	for c := range described {
		out = append(out, c)
	}
	return out
}

// TestEntries_ArgsMatchAnsibleCore checks every ansible.builtin entry
// against ansible-doc: each name the table accepts is an option of the
// module or one of that option's aliases, each selector value is one of
// the option's choices, a selector's default is the option's default, and
// a selector read as a boolean is a boolean option.
func TestEntries_ArgsMatchAnsibleCore(t *testing.T) {
	image := testsupport.BuildAnsibleRunnerImage(t)
	var names []string
	for _, e := range playbook.Entries() {
		for _, name := range append([]string{e.Module}, e.Aliases...) {
			if strings.HasPrefix(name, "ansible.builtin.") {
				names = append(names, name)
			}
		}
	}
	out, err := exec.Command("docker", append([]string{"run", "--rm", image, "ansible-doc", "-j"}, names...)...).Output()
	if err != nil {
		t.Fatalf("ansible-doc: %v", err)
	}
	var docs map[string]struct {
		Doc struct {
			Options map[string]ansibleOption `json:"options"`
		} `json:"doc"`
	}
	if err := json.Unmarshal(out, &docs); err != nil {
		t.Fatalf("ansible-doc output: %v", err)
	}
	redirects := builtinRedirects(t, image)
	for _, e := range playbook.Entries() {
		for _, name := range append([]string{e.Module}, e.Aliases...) {
			short, builtin := strings.CutPrefix(name, "ansible.builtin.")
			if _, documented := docs[name]; builtin && !documented && redirects[short] != e.Module {
				t.Errorf("ansible-core neither documents %s nor redirects it to %s", name, e.Module)
			}
		}
	}
	for _, e := range playbook.Entries() {
		doc, ok := docs[e.Module]
		if !strings.HasPrefix(e.Module, "ansible.builtin.") {
			continue
		}
		if !ok {
			t.Errorf("ansible-doc does not document %s", e.Module)
			continue
		}
		checkEntryAgainst(t, e, doc.Doc.Options)
	}
}

// ansibleDocExceptions are the places a table departs from ansible-doc on
// purpose, each with why.
var ansibleDocExceptions = map[string]string{
	"ansible.builtin.command warn": "warn was removed in ansible-core 2.14; playbooks written for older releases still carry it, and it only ever controlled a warning",
	"ansible.builtin.shell warn":   "as for command",
	"ansible.builtin.dnf state":    "ansible-doc lists no default, but dnf installs unless autoremove is set, which the table blocks",
	"ansible.builtin.file state":   "Ansible keeps an existing path's state when none is given, which for a file is file; file.permissions refuses a missing path, as state=file does",
}

// builtinRedirects returns ansible-core's module and action redirects
// (yum's action to ansible.builtin.dnf), from its own runtime routing
// table, which ansible-doc does not follow. A task's keyword resolves as an
// action first, so both count.
func builtinRedirects(t *testing.T, image string) map[string]string {
	t.Helper()
	const script = `import json, os, ansible, yaml
path = os.path.join(os.path.dirname(ansible.__file__), "config", "ansible_builtin_runtime.yml")
routing = yaml.safe_load(open(path))["plugin_routing"]
out = {}
for section in ("modules", "action"):
    out.update({k: v["redirect"] for k, v in routing[section].items() if isinstance(v, dict) and "redirect" in v})
print(json.dumps(out))`
	out, err := exec.Command("docker", "run", "--rm", image, "python3", "-c", script).Output()
	if err != nil {
		t.Fatalf("reading ansible-core's redirects: %v", err)
	}
	var redirects map[string]string
	if err := json.Unmarshal(out, &redirects); err != nil {
		t.Fatalf("ansible-core's redirects: %v", err)
	}
	return redirects
}

// checkEntryAgainst checks one entry against its module's options.
func checkEntryAgainst(t *testing.T, e playbook.Entry, options map[string]ansibleOption) {
	t.Helper()
	for _, a := range e.Args {
		opt, ok := options[a.Name]
		if _, excepted := ansibleDocExceptions[e.Module+" "+a.Name]; !ok && excepted {
			continue
		}
		if !ok {
			// A free-form module's text arrives under _raw_params, which
			// ansible-doc does not list.
			if a.Name != e.RawArg || !slices.Contains(a.Aliases, "_raw_params") {
				t.Errorf("%s: %s is not an option", e.Module, a.Name)
			}
			continue
		}
		for _, alias := range a.Aliases {
			if alias != "_raw_params" && !slices.Contains(opt.Aliases, alias) {
				t.Errorf("%s: %s is not an alias of %s (aliases %v)", e.Module, alias, a.Name, opt.Aliases)
			}
		}
	}
	for _, s := range e.Selectors {
		opt := options[s.Arg]
		if s.Bool != (opt.Type == "bool") {
			t.Errorf("%s: selector %s reads a boolean %v, but the option's type is %s", e.Module, s.Arg, s.Bool, opt.Type)
		}
		_, excepted := ansibleDocExceptions[e.Module+" "+s.Arg]
		if def := defaultText(opt.Default); !excepted && s.Absent != def {
			t.Errorf("%s: selector %s defaults to %q, but Ansible's default is %q", e.Module, s.Arg, s.Absent, def)
		}
		choices := opt.choices(t)
		if len(choices) == 0 {
			continue
		}
		for _, c := range s.Choices {
			for _, v := range c.Values {
				if !slices.Contains(choices, v) {
					t.Errorf("%s: %s=%s is not one of Ansible's choices %v", e.Module, s.Arg, v, choices)
				}
			}
		}
	}
}

// defaultText renders an option's default or choice as a selector writes
// it: a boolean as true or false, no default as "".
func defaultText(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case bool:
		if x {
			return "true"
		}
		return "false"
	case string:
		return x
	}
	b, _ := json.Marshal(v)
	return string(b)
}
