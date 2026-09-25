// Package playbook: the modules that are Ansible engine features rather
// than work on a device: debug, set_fact, meta, include_* and import_*.
package playbook

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"

	"go.yaml.in/yaml/v3"
)

// resetConnection is meta: reset_connection's native call. It closes the
// connection a run keeps open to the device, as Ansible's closes its
// persistent one, so the next task logs in again.
var resetConnection = &Entry{
	Module: "ansible.builtin.meta",
	Default: &Call{
		FQCN:  "pleiades.builtin.connection.reset",
		Class: ClassObserve,
		Basis: "it closes the platform's own connection and changes nothing on the device",
	},
}

// engineModule handles spec when its module is an engine feature,
// reporting whether it did.
func (t *translator) engineModule(spec leafSpec, ctx taskCtx) ([]*outTask, bool) {
	name := spec.name
	switch shortName(spec.module) {
	case "debug":
		t.raise("debug.dropped", spec.modAt, name, "a debug task only prints")
		return nil, true
	case "set_fact":
		if spec.modNode != nil && deref(spec.modNode).Kind == yaml.MappingNode && len(spec.whens) == 0 && !spec.hasLoop {
			t.raise("set_fact.resolved", spec.modAt, name, "its variables are read from the playbook where they are used")
			return nil, true
		}
		id := t.raise("set_fact.runtime", spec.modAt, name, "set_fact runs conditionally or in a loop, so its values exist only at run time")
		return []*outTask{t.placeholder(name, spec.module, spec.at, ctx, append(spec.cites, id)...)}, true
	case "include_vars":
		id := t.raise("set_fact.runtime", spec.modAt, name, "include_vars sets variables only at run time")
		return []*outTask{t.placeholder(name, spec.module, spec.at, ctx, append(spec.cites, id)...)}, true
	case "meta":
		v := deref(spec.modNode)
		if v != nil && v.Kind == yaml.ScalarNode {
			switch v.Value {
			case "flush_handlers", "noop", "refresh_inventory", "clear_facts", "clear_host_errors":
				t.raise("meta.dropped", spec.modAt, name, "meta: "+v.Value+" has nothing to run")
				return nil, true
			case "reset_connection":
				if len(ctx.blockers) == 0 && len(spec.blocks) == 0 && !spec.hasLoop {
					return t.convertOnce(resetConnection, spec, nil, nil, "", ctx), true
				}
			}
		}
		id := t.raise("meta.unsupported", spec.modAt, name, "this meta action changes how the run proceeds")
		return []*outTask{t.placeholder(name, spec.module, spec.at, ctx, append(spec.cites, id)...)}, true
	case "include_tasks", "include":
		id := t.raise("include.tasks", spec.modAt, name, "include_tasks decides what it includes at run time")
		return []*outTask{t.placeholder(name, spec.module, spec.at, ctx, append(spec.cites, id)...)}, true
	case "include_role", "import_role":
		id := t.raise("include.role", spec.modAt, name, spec.module+" runs a role")
		return []*outTask{t.placeholder(name, spec.module, spec.at, ctx, append(spec.cites, id)...)}, true
	case "import_playbook":
		id := t.raise("import.playbook", spec.modAt, name, "import_playbook belongs at the top of a playbook, not in a task list")
		return []*outTask{t.placeholder(name, spec.module, spec.at, ctx, append(spec.cites, id)...)}, true
	case "import_tasks":
		return t.importTasks(spec, ctx), true
	}
	return nil, false
}

// importTasks inlines an imported task file as a block, translated like
// the playbook's own tasks. The file is read inside the playbook's
// directory only; anything else leaves the task for a person.
func (t *translator) importTasks(spec leafSpec, ctx taskCtx) []*outTask {
	file, blocked := importFile(spec.modNode)
	if blocked == "" {
		file = t.importPath(file)
	}
	if blocked != "" || len(spec.blocks) > 0 || len(ctx.blockers) > 0 {
		ids := append(append([]string{}, ctx.blockers...), spec.blocks...)
		if blocked != "" {
			ids = append(ids, t.raise("import.tasks_missing", spec.modAt, spec.name, blocked))
		}
		return []*outTask{t.placeholder(spec.name, spec.module, spec.at, ctx, append(ids, spec.cites...)...)}
	}
	if slices.Contains(ctx.imports, file) {
		id := t.raise("import.tasks_missing", spec.modAt, spec.name, fmt.Sprintf("%s imports itself", file))
		return []*outTask{t.placeholder(spec.name, spec.module, spec.at, ctx, append(spec.cites, id)...)}
	}
	root, err := t.readTaskFile(file)
	if err != nil {
		id := t.raise("import.tasks_missing", spec.modAt, spec.name, fmt.Sprintf("%s could not be read as a task list inside the playbook's directory", file))
		return []*outTask{t.placeholder(spec.name, spec.module, spec.at, ctx, append(spec.cites, id)...)}
	}
	inner := ctx
	inner.depth++
	inner.imports = append(slices.Clone(ctx.imports), file)
	outer := t.file
	t.file = file
	children := t.translateTasks(root, inner)
	t.file = outer
	if len(children) == 0 {
		return nil
	}
	name := spec.name
	if name == "" {
		name = "import " + file
	}
	return []*outTask{{name: name, block: children, tags: spec.tags, at: spec.at, cites: spec.cites}}
}

// importPath resolves an imported file's name as Ansible does: beside the
// file that imports it first, then in the playbook's own directory. A
// role's tasks/main.yml importing setup.yml means tasks/setup.yml. A
// candidate that would leave the playbook's directory is never tried.
func (t *translator) importPath(name string) string {
	if dir := path.Dir(t.file); dir != "." {
		if beside := path.Join(dir, name); fs.ValidPath(beside) {
			if _, err := fs.Stat(t.fsys, beside); err == nil {
				return beside
			}
		}
	}
	return name
}

// importFile reads import_tasks' file name, or says why it cannot be
// used.
func importFile(n *yaml.Node) (string, string) {
	n = deref(n)
	if n != nil && n.Kind == yaml.MappingNode {
		n = deref(lookup(n, "file"))
	}
	switch {
	case n == nil || n.Kind != yaml.ScalarNode || n.Value == "":
		return "", "import_tasks names no file"
	case hasTemplate(n.Value):
		return "", "import_tasks names its file with a template"
	}
	name := path.Clean(n.Value)
	if !fs.ValidPath(name) {
		return "", "import_tasks names a file outside the playbook's directory"
	}
	return name, ""
}

// readTaskFile reads and parses an imported task list.
func (t *translator) readTaskFile(file string) (*yaml.Node, error) {
	data, err := readBounded(t.fsys, file, maxVarsFileBytes)
	if err != nil {
		return nil, err
	}
	root, err := parseYAML(data, file)
	if err != nil {
		return nil, err
	}
	if root.Kind != yaml.SequenceNode {
		return nil, errors.New("not a task list")
	}
	return root, nil
}
