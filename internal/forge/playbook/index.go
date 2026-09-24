// Package playbook: the pass that finds every variable definition in a
// playbook before any task is translated, so a variable's definitions are
// all counted before any of them is used.
package playbook

import (
	"io/fs"
	"path"
	"strings"

	"go.yaml.in/yaml/v3"
)

// indexPlaybook records every definition in plays.
func (t *translator) indexPlaybook(plays []*yaml.Node) {
	for _, play := range plays {
		t.ix.addLiterals(lookup(play, "vars"), t.file)
		t.indexVarsFiles(lookup(play, "vars_files"))
		if prompts := deref(lookup(play, "vars_prompt")); prompts != nil {
			for _, p := range prompts.Content {
				if name := deref(lookup(p, "name")); name != nil {
					t.ix.addRuntime(name.Value, nodePos(name, t.file))
				}
			}
		}
		for _, list := range []string{"pre_tasks", "tasks", "post_tasks", "handlers"} {
			t.indexTasks(lookup(play, list), 0)
		}
	}
}

// indexVarsFiles reads each vars file inside the playbook's directory. One
// that cannot be read makes every variable's definition count unknown.
func (t *translator) indexVarsFiles(files *yaml.Node) {
	files = deref(files)
	if files == nil {
		return
	}
	for _, f := range files.Content {
		f = deref(f)
		if f == nil || f.Kind != yaml.ScalarNode || hasTemplate(f.Value) {
			t.unreadableVars(f, "a templated or listed-alternative vars file")
			continue
		}
		name := path.Clean(f.Value)
		if !fs.ValidPath(name) {
			t.unreadableVars(f, "a vars file outside the playbook's directory")
			continue
		}
		data, err := readBounded(t.fsys, name, maxVarsFileBytes)
		if err != nil {
			t.unreadableVars(f, "a vars file this converter could not read")
			continue
		}
		root, err := parseYAML(data, name)
		if err != nil || root.Kind != yaml.MappingNode {
			t.unreadableVars(f, "a vars file that is not a map")
			continue
		}
		t.ix.addLiterals(root, name)
	}
}

// unreadableVars records a vars file that could not be read.
func (t *translator) unreadableVars(n *yaml.Node, why string) {
	t.ix.unknownSource = "vars_files"
	t.raise("vars.files", nodePos(n, t.file), "", why)
}

// indexTasks records the definitions in a task list, at any depth.
func (t *translator) indexTasks(list *yaml.Node, depth int) {
	list = deref(list)
	if list == nil || list.Kind != yaml.SequenceNode || depth > maxTaskDepth {
		return
	}
	for _, task := range list.Content {
		t.ix.addLiterals(lookup(task, "vars"), t.file)
		entries, _ := mapEntries(task)
		module, looped, conditional := "", false, false
		for _, e := range entries {
			switch {
			case e.key == "loop" || strings.HasPrefix(e.key, "with_"):
				looped = true
			case e.key == "when":
				conditional = true
			case isModuleKey(e.key) && e.key != "block":
				module = e.key
			}
		}
		if rv := deref(lookup(task, "register")); rv != nil {
			t.ix.addRegister(rv.Value, module, nodePos(rv, t.file))
		}
		if lc := lookup(task, "loop_control"); lc != nil {
			if lv := deref(lookup(lc, "loop_var")); lv != nil {
				t.ix.addRuntime(lv.Value, nodePos(lv, t.file))
			}
		}
		switch shortName(module) {
		case "set_fact":
			args := deref(lookup(task, module))
			if looped || conditional || args == nil || args.Kind != yaml.MappingNode {
				t.addRuntimeKeys(args)
			} else {
				t.ix.addLiterals(args, t.file)
			}
		case "include_vars":
			t.ix.unknownSource = "include_vars"
		}
		if shortName(module) == "import_tasks" {
			if file, blocked := importFile(lookup(task, module)); blocked == "" && !t.indexed[file] {
				t.indexed[file] = true
				if root, err := t.readTaskFile(file); err == nil {
					outer := t.file
					t.file = file
					t.indexTasks(root, depth+1)
					t.file = outer
				}
			}
		}
		for _, sub := range []string{"block", "rescue", "always"} {
			t.indexTasks(lookup(task, sub), depth+1)
		}
	}
}

// addRuntimeKeys records each key of a set_fact that runs conditionally
// as set only at run time.
func (t *translator) addRuntimeKeys(args *yaml.Node) {
	entries, _ := mapEntries(args)
	for _, e := range entries {
		t.ix.addRuntime(e.key, nodePos(e.keyAt, t.file))
	}
}

// shortName is a module name without its collection: ansible.builtin.x,
// ansible.legacy.x and x are all x.
func shortName(module string) string {
	for _, prefix := range []string{"ansible.builtin.", "ansible.legacy."} {
		if rest, ok := strings.CutPrefix(module, prefix); ok {
			return rest
		}
	}
	return module
}
