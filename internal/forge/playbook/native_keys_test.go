// Tests of the Ansible keywords the converter now maps onto the runbook's own
// (loop, until, changed_when, failed_when, ignore_errors, vars, set_fact,
// assert, fail): the exact native output for real playbook YAML, the findings
// raised, and a proof that each converted runbook builds with the engine's own
// builder and passes validation.
package playbook_test

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/playbook"
	"github.com/Subject-Void-LLC/the-pleiades/internal/validate"
)

// nativeCase is one playbook and what it converts to.
type nativeCase struct {
	name string
	// play is the play's text after "- hosts: web / gather_facts: false",
	// each line indented as a play key; tasks are indented as under tasks:.
	play  string
	tasks string
	extra []string
	// want is the converted runbook's key-by-key content (without id, hosts
	// and the header); empty when the conversion is blocked.
	want string
	// codes must all be among the findings; absent must not be.
	codes, absent []playbook.Code
	// blocked is the number of blocked tasks.
	blocked int
}

// wholePlaybook builds the playbook text of c.
func (c nativeCase) wholePlaybook() string {
	pb := "- hosts: web\n  gather_facts: false\n"
	if c.play != "" {
		pb += c.play + "\n"
	}
	return pb + "  tasks:\n    " + strings.ReplaceAll(strings.TrimRight(c.tasks, "\n"), "\n", "\n    ") + "\n"
}

// runNative converts each case, compares the written runbook with want as
// data, and builds and validates it.
func runNative(t *testing.T, cases []nativeCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := convert(t, tc.wholePlaybook(), tc.extra...)
			codes := codesOf(res.Report)
			for _, c := range tc.codes {
				if !slices.Contains(codes, c) {
					t.Errorf("findings %v lack %s\n%s", codes, c, res.Runbooks[0].YAML)
				}
			}
			for _, c := range tc.absent {
				if slices.Contains(codes, c) {
					t.Errorf("findings %v hold %s, which should not be raised\n%s", codes, c, res.Runbooks[0].YAML)
				}
			}
			if res.Report.Counts.Blocked != tc.blocked {
				t.Errorf("blocked = %d, want %d; findings %v\n%s", res.Report.Counts.Blocked, tc.blocked, codes, res.Runbooks[0].YAML)
			}
			if tc.blocked > 0 {
				return
			}
			var got, want map[string]any
			if err := yaml.Unmarshal(res.Runbooks[0].YAML, &got); err != nil {
				t.Fatal(err)
			}
			delete(got, "id")
			delete(got, "hosts")
			if err := yaml.Unmarshal([]byte(tc.want), &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("converted runbook differs\n got: %v\nwant: %v\n%s", got, want, res.Runbooks[0].YAML)
			}
			dag := build(t, res.Runbooks[0].YAML)
			if report := validate.Validate(validate.WorldView{DAG: dag}); report.HasErrors() {
				t.Errorf("the converted runbook fails validation:\n%s\n%s", report.String(), res.Runbooks[0].YAML)
			}
		})
	}
}

// TestNative_Loops covers loop and with_items kept as the runbook's loop,
// with loop_control and the template rules the loop's values are held to.
func TestNative_Loops(t *testing.T) {
	runNative(t, []nativeCase{
		{
			name:  "loop over a registered result",
			tasks: "- {name: list, command: ls /tmp, register: found}\n- name: each\n  command: echo {{ item }}\n  loop: \"{{ found.stdout }}\"",
			want: `tasks:
  - name: list
    exec.command: {cmd: ls /tmp}
    register: found
  - name: each
    exec.command: {cmd: "echo {{ item | quote }}"}
    loop: "{{ result.found.stdout }}"`,
			codes: []playbook.Code{"loop.native", "template.quoted", "template.register"},
		},
		{
			name: "loop_var, index_var, label, item fields and a condition on the item",
			tasks: `- {name: list, command: ls /tmp, register: found}
- name: each
  command: echo {{ x.name }} {{ i }}
  loop: "{{ found.stdout }}"
  loop_control: {loop_var: x, index_var: i, label: "{{ x.name }}"}
  when: x.kind == 'file'`,
			want: `tasks:
  - name: list
    exec.command: {cmd: ls /tmp}
    register: found
  - name: each
    exec.command: {cmd: "echo {{ x.name | quote }} {{ i | quote }}"}
    loop: "{{ result.found.stdout }}"
    loop_control: {loop_var: x, index_var: i, label: "{{ x.name }}"}
    when: ['item.kind == "file"']`,
			codes: []playbook.Code{"loop.native"},
		},
		{
			name:  "with_items keeps its key",
			tasks: "- {name: list, command: ls /tmp, register: found}\n- {name: each, command: \"echo {{ item }}\", with_items: \"{{ found.stdout }}\"}",
			want: `tasks:
  - name: list
    exec.command: {cmd: ls /tmp}
    register: found
  - name: each
    exec.command: {cmd: "echo {{ item | quote }}"}
    with_items: "{{ result.found.stdout }}"`,
			codes: []playbook.Code{"loop.native"},
		},
		{
			name:  "a looped task that registers",
			tasks: "- {name: each, command: \"echo {{ item }}\", loop: [a, b], register: out}",
			want: `tasks:
  - name: each
    exec.command: {cmd: "echo {{ item | quote }}"}
    loop: [a, b]
    register: out`,
			codes: []playbook.Code{"loop.native"},
		},
		{
			name:  "a literal list is still unrolled",
			tasks: "- {name: each, command: \"echo {{ item }}\", loop: [a, b]}",
			want: `tasks:
  - name: each (item 1 of 2)
    exec.command: {cmd: echo a}
  - name: each (item 2 of 2)
    exec.command: {cmd: echo b}`,
			codes:  []playbook.Code{"loop.unrolled"},
			absent: []playbook.Code{"loop.native"},
		},
		{
			name:    "with_dict over a runtime value stays blocked",
			tasks:   "- {name: each, command: x, with_dict: \"{{ found.stdout }}\"}",
			codes:   []playbook.Code{"loop.runtime"},
			blocked: 1,
		},
		{
			name:    "with_nested stays blocked",
			tasks:   "- {name: each, command: x, with_nested: [[a, b], [c, d]]}",
			codes:   []playbook.Code{"loop.runtime"},
			blocked: 1,
		},
		{
			name:    "a loop over a magic variable stays blocked",
			tasks:   "- {name: each, command: \"echo {{ item }}\", loop: \"{{ groups['web'] }}\"}",
			codes:   []playbook.Code{"loop.runtime"},
			blocked: 1,
		},
		{
			name:    "a loop_control option the loop has no equivalent for",
			tasks:   "- {name: each, command: x, loop: \"{{ found.stdout }}\", loop_control: {pause: 2}}",
			codes:   []playbook.Code{"loop.control"},
			blocked: 1,
		},
	})
}

// TestNative_UntilChangedFailedIgnore covers the keywords that read the task's
// own result, translated by the one condition translator.
func TestNative_UntilChangedFailedIgnore(t *testing.T) {
	runNative(t, []nativeCase{
		{
			name: "until with retries and delay, rc and search",
			tasks: `- name: wait
  command: probe
  register: probe
  until: probe.rc == 0 and probe.stdout is search('ready')
  retries: 5
  delay: 2`,
			want: `tasks:
  - name: wait
    exec.command: {cmd: probe}
    register: probe
    until: ['(result.probe.rc == 0) && (result.probe.stdout.matches("ready"))']
    retries: 5
    delay: 2`,
		},
		{
			name: "until with in, a list, and match",
			tasks: `- name: wait
  command: probe
  register: probe
  until:
    - "'ready' in probe.stdout"
    - probe.stdout is match('ok')`,
			want: `tasks:
  - name: wait
    exec.command: {cmd: probe}
    register: probe
    until: ['result.probe.stdout.contains("ready")', 'result.probe.stdout.matches("^(?:ok)")']`,
		},
		{
			name:    "until without a register",
			tasks:   "- {name: wait, command: probe, until: probe.rc == 0}",
			codes:   []playbook.Code{"keyword.retries"},
			blocked: 1,
		},
		{
			name:    "until with a test that has no translation",
			tasks:   "- {name: wait, command: probe, register: probe, until: probe is succeeded}",
			codes:   []playbook.Code{"keyword.retries"},
			blocked: 1,
		},
		{
			name:    "a search whose pattern means something else to RE2",
			tasks:   "- {name: wait, command: probe, register: probe, until: \"probe.stdout is search('done$')\"}",
			codes:   []playbook.Code{"keyword.retries"},
			blocked: 1,
		},
		{
			name:    "retries past the native bound",
			tasks:   "- {name: wait, command: probe, register: probe, until: probe.rc == 0, retries: 101}",
			codes:   []playbook.Code{"keyword.retries"},
			blocked: 1,
		},
		{
			name:    "a templated delay",
			tasks:   "- {name: wait, command: probe, register: probe, until: probe.rc == 0, delay: \"{{ d }}\"}",
			codes:   []playbook.Code{"keyword.retries"},
			blocked: 1,
		},
		{
			name:  "retries without until do nothing",
			tasks: "- {name: once, command: probe, retries: 4, delay: 1}",
			want: `tasks:
  - name: once
    exec.command: {cmd: probe}`,
			codes: []playbook.Code{"args.ignored"},
		},
		{
			name: "changed_when and failed_when over the result",
			tasks: `- name: cfg
  command: show
  register: cfg
  changed_when: "'diff' in cfg.stdout"
  failed_when: cfg.rc > 1`,
			want: `tasks:
  - name: cfg
    exec.command: {cmd: show}
    register: cfg
    changed_when: ['result.cfg.stdout.contains("diff")']
    failed_when: ['result.cfg.rc > 1']`,
		},
		{
			name:  "changed_when false with no register gets one of its own",
			tasks: "- {name: read, command: uptime, changed_when: false}",
			want: `tasks:
  - name: read
    exec.command: {cmd: uptime}
    register: forge_result_1
    changed_when: ["false"]`,
		},
		{
			name:  "ignore_errors true",
			tasks: "- {name: try, command: maybe, ignore_errors: yes}",
			want: `tasks:
  - name: try
    exec.command: {cmd: maybe}
    ignore_errors: true`,
			codes: []playbook.Code{"keyword.ignore_errors"},
		},
		{
			name:  "failed_when false becomes ignore_errors",
			tasks: "- {name: try, command: maybe, failed_when: false}",
			want: `tasks:
  - name: try
    exec.command: {cmd: maybe}
    ignore_errors: true`,
			codes: []playbook.Code{"keyword.ignore_errors"},
		},
		{
			name:  "a block's ignore_errors reaches its tasks",
			tasks: "- block: [{name: a, command: x}, {name: b, command: y}]\n  ignore_errors: true",
			want: `tasks:
  - name: ""
    block:
      - {name: a, exec.command: {cmd: x}, ignore_errors: true}
      - {name: b, exec.command: {cmd: y}, ignore_errors: true}`,
			codes: []playbook.Code{"keyword.ignore_errors"},
		},
		{
			name:    "a condition on a block that reads one task's result",
			tasks:   "- block: [{name: a, command: x, register: r}]\n  failed_when: r.rc > 0",
			codes:   []playbook.Code{"keyword.failed_when"},
			blocked: 1,
		},
		{
			name:    "a templated ignore_errors",
			tasks:   "- {name: try, command: maybe, ignore_errors: \"{{ soft }}\"}",
			codes:   []playbook.Code{"keyword.ignore_errors"},
			blocked: 1,
		},
	})
}

// TestNative_Vars covers vars at the play, block and task level, the vars a
// template is rewritten to read, and the precedence finding.
func TestNative_Vars(t *testing.T) {
	runNative(t, []nativeCase{
		{
			name:  "a variable set at the play and the task",
			play:  "  vars: {app: /srv}",
			tasks: "- name: use\n  command: echo {{ app }}\n  vars: {app: /opt}",
			want: `vars: {app: /srv}
tasks:
  - name: use
    exec.command: {cmd: "echo {{ vars.app | quote }}"}
    vars: {app: /opt}`,
			codes: []playbook.Code{"vars.precedence", "template.quoted"},
		},
		{
			name:  "block vars and a task under it",
			tasks: "- name: group\n  vars: {mode: fast}\n  block:\n    - {name: use, command: \"run {{ mode }}\"}\n    - {name: other, command: \"run {{ mode }}\", vars: {mode: slow}}",
			want: `tasks:
  - name: group
    vars: {mode: fast}
    block:
      - {name: use, exec.command: {cmd: "run {{ vars.mode | quote }}"}}
      - {name: other, exec.command: {cmd: "run {{ vars.mode | quote }}"}, vars: {mode: slow}}`,
			codes: []playbook.Code{"vars.precedence"},
		},
		{
			name:  "a variable defined once is still folded and not carried",
			play:  "  vars: {app: /srv}",
			tasks: "- {name: use, command: \"echo {{ app }}\"}",
			want: `tasks:
  - name: use
    exec.command: {cmd: echo /srv}`,
			absent: []playbook.Code{"vars.precedence"},
		},
		{
			name:    "a secret var is never carried",
			play:    "  vars: {db_password: hunter2hunter2}",
			tasks:   "- {name: use, command: \"echo {{ db_password }}\", vars: {db_password: other-secret-value}}",
			codes:   []playbook.Code{"vars.unreadable"},
			blocked: 1,
		},
		{
			name:    "a vars name the runbook cannot read",
			tasks:   "- {name: use, command: x, vars: {\"bad-name\": 1}}",
			codes:   []playbook.Code{"vars.unreadable"},
			blocked: 1,
		},
		{
			name:  "group_vars beside the playbook",
			tasks: "- {name: use, command: x}",
			extra: []string{"group_vars/all.yml", "ntp: pool\n"},
			want: `tasks:
  - name: use
    exec.command: {cmd: x}`,
			codes: []playbook.Code{"vars.inventory"},
		},
	})
}

// TestNative_SetFact covers set_fact kept as the runbook's action.
func TestNative_SetFact(t *testing.T) {
	runNative(t, []nativeCase{
		{
			name:  "conditional set_fact reading a play var",
			play:  "  vars: {env: prod}",
			tasks: "- name: pick\n  set_fact: {tier: \"{{ env }}-tier\", extra: 1}\n  when: env == 'prod'\n- {name: use, command: \"echo {{ tier }}\"}",
			want: `tasks:
  - name: pick
    set_fact: {tier: prod-tier, extra: 1}
  - name: use
    exec.command: {cmd: "echo {{ vars.tier | quote }}"}`,
			codes: []playbook.Code{"template.quoted"},
		},
		{
			name:  "set_fact on a name also in a task's vars",
			play:  "  vars: {mode: a}",
			tasks: "- {name: set, set_fact: {mode: b}}\n- {name: use, command: \"echo {{ mode }}\", vars: {mode: c}}",
			want: `vars: {mode: a}
tasks:
  - name: set
    set_fact: {mode: b}
  - name: use
    exec.command: {cmd: "echo {{ vars.mode | quote }}"}
    vars: {mode: c}`,
			codes: []playbook.Code{"vars.precedence"},
		},
		{
			name:  "cacheable is dropped with a finding",
			tasks: "- {name: set, set_fact: {a: 1, cacheable: true}, when: x is defined}\n- {name: use, command: \"echo {{ a }}\"}",
			want: `tasks:
  - name: set
    set_fact: {a: 1}
    when: ["false"]
  - name: use
    exec.command: {cmd: "echo {{ vars.a | quote }}"}`,
			codes: []playbook.Code{"set_fact.cacheable"},
		},
		{
			name:   "a literal set_fact is folded as before",
			tasks:  "- {name: set, set_fact: {a: one}}\n- {name: use, command: \"echo {{ a }}\"}",
			want:   "tasks:\n  - name: use\n    exec.command: {cmd: echo one}",
			codes:  []playbook.Code{"set_fact.resolved"},
			absent: []playbook.Code{"set_fact.runtime"},
		},
		{
			name:    "a looped set_fact has no native form",
			tasks:   "- {name: set, set_fact: {a: 1}, loop: [x]}",
			codes:   []playbook.Code{"set_fact.runtime"},
			blocked: 1,
		},
		{
			name:    "a set_fact value shaped like a secret",
			tasks:   "- {name: set, set_fact: {a: \"https://u:SENTINELPASS@example.com/r.git\"}, when: x is defined}",
			codes:   []playbook.Code{"template.secret"},
			blocked: 1,
		},
	})
}

// TestNative_AssertAndFail covers assert and fail.
func TestNative_AssertAndFail(t *testing.T) {
	runNative(t, []nativeCase{
		{
			name: "assert with that, messages and quiet",
			tasks: `- {name: list, command: ls /tmp, register: found}
- name: check
  ansible.builtin.assert:
    that:
      - "'a' in found.stdout"
      - found.rc == 0
    fail_msg: the listing is wrong
    success_msg: fine
    quiet: yes`,
			want: `tasks:
  - name: list
    exec.command: {cmd: ls /tmp}
    register: found
  - name: check
    assert:
      that:
        - 'stat.found.all(d, stat.found[d].stdout.contains("a"))'
        - 'stat.found.all(d, stat.found[d].rc == 0)'
      fail_msg: the listing is wrong
      success_msg: fine`,
			codes: []playbook.Code{"assert.quiet", "when.register"},
		},
		{
			name:  "assert with one condition as text",
			play:  "  vars: {n: 3}",
			tasks: "- {name: check, assert: {that: n == 3}}",
			want: `tasks:
  - name: check
    assert:
      that: ["true"]`,
		},
		{
			name:  "fail with a message and a condition",
			tasks: "- {name: list, command: ls /tmp, register: found}\n- {name: stop, ansible.builtin.fail: {msg: stop here}, when: found.rc != 0}",
			want: `tasks:
  - name: list
    exec.command: {cmd: ls /tmp}
    register: found
  - name: stop
    fail: {msg: stop here}
    when: ['stat.found.all(d, stat.found[d].rc != 0)']`,
		},
		{
			name:    "an assert condition that does not translate",
			tasks:   "- {name: check, assert: {that: \"x is version('1.0', '>=')\"}}",
			codes:   []playbook.Code{"when.unsupported"},
			blocked: 1,
		},
		{
			name:    "an assert in a loop",
			tasks:   "- {name: check, assert: {that: true}, loop: [a]}",
			codes:   []playbook.Code{"assert.unmapped"},
			blocked: 1,
		},
		{
			name:    "an assert argument the native one lacks",
			tasks:   "- {name: check, assert: {that: true, bogus: 1}}",
			codes:   []playbook.Code{"assert.unmapped"},
			blocked: 1,
		},
	})
}

// TestNative_NoSecretsInVars proves a secret reaches neither the runbook nor
// the report through vars, set_fact or an assert message.
func TestNative_NoSecretsInVars(t *testing.T) {
	const secret = "SENTINEL-hunter2-value"
	res := convert(t, `- hosts: web
  gather_facts: false
  vars:
    api_token: `+secret+`
  tasks:
    - {name: a, command: "echo {{ api_token }}", vars: {api_token: other}}
    - {name: b, set_fact: {api_token: `+secret+`}, when: x is defined}
    - {name: c, assert: {that: true, fail_msg: "{{ api_token }}"}}
`)
	for _, rb := range res.Runbooks {
		if strings.Contains(string(rb.YAML), secret) {
			t.Errorf("the secret reached %s:\n%s", rb.File, rb.YAML)
		}
	}
	if strings.Contains(res.Report.String(), secret) {
		t.Error("the secret reached the report")
	}
}
