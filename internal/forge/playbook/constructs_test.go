// The construct table as a test: what the converter does with each
// Ansible construct, one case per row of docs/03's table, asserting the
// finding it raises and whether the task converted.
package playbook_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/playbook"
)

// TestTranslate_Constructs converts one small play per construct.
func TestTranslate_Constructs(t *testing.T) {
	for _, tc := range []struct {
		name string
		// tasks is the play's task list, indented as under tasks:.
		tasks string
		// play holds extra play keys, indented as a play's own.
		play string
		// extra are more files beside the playbook, name then content.
		extra []string
		code  playbook.Code
		// converted and blocked are the expected task counts.
		converted, blocked int
	}{
		{name: "notify", tasks: "- {command: x, notify: h}", code: "keyword.notify", converted: 1},
		{name: "become", tasks: "- {command: x, become: true}", code: "keyword.become", converted: 1},
		{name: "become false", tasks: "- {command: x, become: false}", converted: 1},
		{name: "become_user", tasks: "- {command: x, become: true, become_user: postgres}", code: "keyword.become_user", blocked: 1},
		{name: "ignore_errors", tasks: "- {command: x, ignore_errors: yes}", code: "keyword.ignore_errors", converted: 1},
		{name: "changed_when", tasks: "- {command: x, changed_when: false}", converted: 1},
		{name: "changed_when expr", tasks: "- {command: x, changed_when: \"'x' in r.stdout\"}", code: "keyword.changed_when", converted: 1},
		{name: "failed_when false", tasks: "- {command: x, failed_when: false}", code: "keyword.ignore_errors", converted: 1},
		{name: "failed_when expr", tasks: "- {command: x, failed_when: r.rc > 1}", code: "keyword.failed_when", blocked: 1},
		{name: "retries", tasks: "- {command: x, retries: 3, until: r.rc == 0}", code: "keyword.retries", blocked: 1},
		{name: "async", tasks: "- {command: x, async: 60, poll: 0}", code: "keyword.async", blocked: 1},
		{name: "delegate_to", tasks: "- {command: x, delegate_to: localhost}", code: "keyword.delegate_to", blocked: 1},
		{name: "run_once", tasks: "- {command: x, run_once: true}", code: "keyword.run_once", blocked: 1},
		{name: "throttle", tasks: "- {command: x, throttle: 1}", code: "keyword.throttle", blocked: 1},
		{name: "local_action", tasks: "- {local_action: command x}", code: "keyword.local_action", blocked: 1},
		{name: "connection local", tasks: "- {command: x, connection: local}", code: "keyword.local_action", blocked: 1},
		{name: "delegate_to another host", tasks: "- {command: x, delegate_to: db1}", code: "keyword.delegate_to", blocked: 1},
		{name: "delegate_to templated", tasks: "- {command: x, delegate_to: \"{{ h }}\"}", code: "keyword.delegate_to", blocked: 1},
		{name: "delegate_to false", tasks: "- {command: x, delegate_to: false}", converted: 1},
		{name: "debug delegated to localhost", tasks: "- {debug: {msg: x}, delegate_to: localhost}", code: "debug.dropped"},
		{name: "play delegate_to", tasks: "- {command: x}", play: "  delegate_to: localhost", code: "keyword.delegate_to", blocked: 1},
		{name: "connection ssh", tasks: "- {command: x, connection: ssh}", code: "keyword.connection", converted: 1},
		{name: "no_log", tasks: "- {command: x, no_log: true}", code: "keyword.no_log", blocked: 1},
		{name: "environment", tasks: "- {command: x, environment: {A: b}}", code: "keyword.environment", blocked: 1},
		{name: "module_defaults", tasks: "- {command: x, module_defaults: {}}", code: "keyword.module_defaults", blocked: 1},
		{name: "timeout", tasks: "- {command: x, timeout: 5}", code: "keyword.timeout", blocked: 1},
		{name: "diff", tasks: "- {command: x, diff: true}", code: "keyword.reporting", converted: 1},
		{name: "check_mode on uncheckable", tasks: "- {command: x, check_mode: true}", code: "keyword.check_mode", blocked: 1},
		{name: "check_mode on a guarded command", tasks: "- {command: x creates=/y, check_mode: true}", converted: 1},
		{name: "check_mode false", tasks: "- {command: x, check_mode: false}", code: "keyword.check_mode_false", converted: 1},
		{name: "check_mode template", tasks: "- {command: x, check_mode: \"{{ c }}\"}", code: "keyword.check_mode", blocked: 1},
		{name: "templated tags", tasks: "- {command: x, tags: \"{{ t }}\"}", code: "keyword.tags", blocked: 1},
		{name: "filter-only tag", tasks: "- {command: x, tags: [all]}", code: "keyword.tags", blocked: 1},
		{name: "templated register", tasks: "- {command: x, register: \"{{ r }}\"}", code: "args.value", blocked: 1},
		{name: "two modules", tasks: "- {command: x, shell: y}", code: "module.ambiguous", blocked: 1},
		{name: "no module", tasks: "- {name: nothing}", code: "module.ambiguous", blocked: 1},
		{name: "unmapped module", tasks: "- {frobnicate: {a: 1}}", code: "module.unmapped", blocked: 1},
		{name: "manual module", tasks: "- {template: {src: a, dest: b}}", code: "module.manual", blocked: 1},
		{name: "unmapped argument", tasks: "- {command: {cmd: x, bogus: 1}}", code: "args.unmapped", blocked: 1},
		{name: "blocked argument", tasks: "- {command: {cmd: x, strip_empty_ends: false}}", code: "args.unmapped", blocked: 1},
		{name: "dropped argument", tasks: "- {command: {cmd: x, warn: false}}", code: "keyword.reporting", converted: 1},
		{name: "args keyword", tasks: "- {command: x, args: {chdir: /tmp}}", converted: 1},
		{name: "unbalanced free-form", tasks: "- {command: \"echo 'x\"}", code: "args.unparsable", blocked: 1},
		{name: "action string", tasks: "- {action: command echo hi}", converted: 1},
		{name: "action map", tasks: "- {action: {module: command, cmd: echo hi}}", converted: 1},
		{name: "debug", tasks: "- {debug: {msg: hi}}", code: "debug.dropped"},
		{name: "set_fact literal", tasks: "- {set_fact: {a: one}}\n- {command: \"echo {{ a }}\"}", code: "set_fact.resolved", converted: 1},
		{name: "set_fact conditional", tasks: "- {set_fact: {a: 1}, when: b is defined}\n- {command: \"echo {{ a }}\"}", code: "set_fact.runtime", blocked: 2},
		{name: "include_vars", tasks: "- {include_vars: x.yml}", code: "set_fact.runtime", blocked: 1},
		{name: "meta flush", tasks: "- {meta: flush_handlers}", code: "meta.dropped"},
		{name: "meta end_play", tasks: "- {meta: end_play}", code: "meta.unsupported", blocked: 1},
		{name: "meta reset_connection", tasks: "- {meta: reset_connection}", converted: 1},
		{name: "meta reset_connection with when", tasks: "- {meta: reset_connection, when: a}", play: "  vars: {a: true}", converted: 1},
		{name: "meta reset_connection in a loop", tasks: "- {meta: reset_connection, loop: [a]}", code: "meta.unsupported", blocked: 1},
		{name: "include_tasks", tasks: "- {include_tasks: x.yml}", code: "include.tasks", blocked: 1},
		{name: "include_role", tasks: "- {include_role: {name: r}}", code: "include.role", blocked: 1},
		{name: "import_playbook in tasks", tasks: "- {import_playbook: x.yml}", code: "import.playbook", blocked: 1},
		{name: "with_items flattens", tasks: "- {command: \"echo {{ item }}\", with_items: [[a, b], c]}", code: "loop.unrolled", converted: 3},
		{name: "with_dict keeps order", tasks: "- {command: \"echo {{ item.key }}\", with_dict: {z: 1, a: 2}}", code: "loop.unrolled", converted: 2},
		{name: "loop_var and index", tasks: "- {command: \"echo {{ x }}\", loop: [a, b], loop_control: {loop_var: x, index_var: i}}", code: "loop.unrolled", converted: 2},
		{name: "loop pause", tasks: "- {command: x, loop: [a], loop_control: {pause: 2}}", code: "loop.control", blocked: 1},
		{name: "loop register", tasks: "- {command: x, loop: [a], register: r}", code: "loop.register", blocked: 1},
		{name: "lookup loop", tasks: "- {command: x, with_fileglob: '*.conf'}", code: "loop.runtime", blocked: 1},
		{name: "runtime loop", tasks: "- {command: x, loop: \"{{ groups['web'] }}\"}", code: "loop.runtime", blocked: 1},
		{name: "fact template", tasks: "- {command: \"echo {{ ansible_hostname }}\"}", code: "template.fact", blocked: 1},
		{name: "unsupported template", tasks: "- {command: \"echo {{ a | regex_replace('x', 'y') }}\"}", play: "  vars: {a: x}", code: "template.unsupported", blocked: 1},
		{name: "supported filter", tasks: "- {command: \"echo {{ a | upper }}\"}", play: "  vars: {a: x}", converted: 1},
		{name: "whole map in text", tasks: "- {command: \"echo {{ m }}\"}", play: "  vars: {m: {k: v}}", code: "template.unsupported", blocked: 1},
		{name: "number field of a map", tasks: "- {command: \"echo {{ m.n }}\"}", play: "  vars: {m: {n: 1}}", code: "template.unsupported", blocked: 1},
		{name: "number inside text", tasks: "- {command: \"echo {{ n }}\"}", play: "  vars: {n: 3}", code: "template.unsupported", blocked: 1},
		{name: "vars file", tasks: "- {command: \"echo {{ a }}\"}", play: "  vars_files: [v.yml]", extra: []string{"v.yml", "a: from-file\n"}, converted: 1},
		{name: "unreadable vars file", tasks: "- {command: x}", play: "  vars_files: [../v.yml]", code: "vars.files", converted: 1},
		{name: "templated when", tasks: "- {command: x, when: \"{{ a }}\"}", code: "when.unsupported", blocked: 1},
		{name: "list when", tasks: "- {command: x, when: [a, b]}", play: "  vars: {a: true, b: true}", converted: 1},
		{name: "block when pushed down", tasks: "- block: [{command: x}, {command: y}]\n  when: a", play: "  vars: {a: true}", converted: 2},
		{name: "block delegate_to", tasks: "- block: [{command: x}, {command: y}]\n  delegate_to: h", code: "keyword.delegate_to", blocked: 2},
		{name: "block become", tasks: "- block: [{command: x}]\n  become: true", code: "keyword.become", converted: 1},
		{name: "block check_mode", tasks: "- block: [{command: x creates=/y}]\n  check_mode: true", converted: 1},
		{name: "block always", tasks: "- block: [{command: x}]\n  always: [{command: y}]", code: "block.always", converted: 2},
		{name: "block of debug", tasks: "- block: [{debug: {msg: x}}]\n  rescue: [{command: y}]", code: "debug.dropped"},
		{name: "block unknown key", tasks: "- block: [{command: x}]\n  frob: 1", code: "module.ambiguous", blocked: 1},
		{name: "play serial", tasks: "- {command: x}", play: "  serial: 1", code: "play.serial", converted: 1},
		{name: "play roles", tasks: "- {command: x}", play: "  roles: [r]", code: "play.roles", converted: 1},
		{name: "play vars_prompt", tasks: "- {command: x}", play: "  vars_prompt: [{name: p}]", code: "play.vars_prompt", converted: 1},
		{name: "play strategy", tasks: "- {command: x}", play: "  strategy: free", code: "play.order", converted: 1},
		{name: "play become", tasks: "- {command: x}", play: "  become: true", code: "keyword.become", converted: 1},
		{name: "play environment", tasks: "- {command: x}", play: "  environment: {A: b}", code: "keyword.environment", blocked: 1},
		{name: "play unknown key", tasks: "- {command: x}", play: "  frob: 1", code: "keyword.unknown", converted: 1},
		{name: "play check_mode", tasks: "- {command: x creates=/y}", play: "  check_mode: yes", converted: 1},
		{name: "play collections", tasks: "- {command: x}", play: "  collections: [community.general]", converted: 1},
		{name: "duplicate task key", tasks: "- command: x\n  command: y", code: "yaml.duplicate_key", blocked: 1},
		{name: "not a map", tasks: "- just text", code: "module.ambiguous", blocked: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pb := "- hosts: web\n  gather_facts: false\n"
			if tc.play != "" {
				pb += tc.play + "\n"
			}
			pb += "  tasks:\n    " + strings.ReplaceAll(tc.tasks, "\n", "\n    ") + "\n"
			res := convert(t, pb, tc.extra...)
			codes := codesOf(res.Report)
			if tc.code != "" && !slices.Contains(codes, tc.code) {
				t.Errorf("findings %v lack %s\n%s", codes, tc.code, pb)
			}
			c := res.Report.Counts
			if c.Converted != tc.converted || c.Blocked != tc.blocked {
				t.Errorf("converted %d blocked %d, want %d and %d; findings %v\n%s", c.Converted, c.Blocked, tc.converted, tc.blocked, codes, pb)
			}
		})
	}
}

// TestTranslate_Hosts covers what a play's hosts become.
func TestTranslate_Hosts(t *testing.T) {
	for _, tc := range []struct {
		hosts string
		code  playbook.Code
		want  string
	}{
		{"web", "", "web"},
		{"all", "play.hosts_all", "all"},
		{"localhost", "play.hosts_local", ""},
		{"web:!db", "play.hosts_pattern", ""},
		{"[a, b]", "play.hosts_pattern", ""},
	} {
		res := convert(t, "- hosts: "+tc.hosts+"\n  tasks: [{command: x}]\n")
		if tc.code != "" && !slices.Contains(codesOf(res.Report), tc.code) {
			t.Errorf("hosts %s: findings %v lack %s", tc.hosts, codesOf(res.Report), tc.code)
		}
		if got := res.Report.Runbooks[0].Hosts; got != tc.want {
			t.Errorf("hosts %s became %q, want %q", tc.hosts, got, tc.want)
		}
		if !slices.Contains(codesOf(res.Report), "play.facts") {
			t.Errorf("hosts %s: gather_facts left at its default raised no play.facts", tc.hosts)
		}
	}
}
