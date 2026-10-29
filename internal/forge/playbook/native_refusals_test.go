// Tests of the refusals and edge forms of the converter's native-keyword
// mappings: a template the runbook cannot read, an unrolled loop's item read
// through a path, the engine actions set_fact, assert and fail given a form
// the runbook cannot carry, and the keywords a block shares with its tasks.
// Each case is a playbook a user could write, and each blocked case names the
// finding message that says why.
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

// refusalCase is a nativeCase plus the text one finding's message must hold,
// which tells two refusals under the same code apart.
type refusalCase struct {
	nativeCase
	// msg is a substring of some finding's message.
	msg string
}

// runRefusals converts each case, checks the finding codes, the blocked count
// and the message, and for a converted case compares the runbook with want
// and builds and validates it.
func runRefusals(t *testing.T, cases []refusalCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := convert(t, tc.wholePlaybook(), tc.extra...)
			codes := codesOf(res.Report)
			for _, c := range tc.codes {
				if !slices.Contains(codes, c) {
					t.Errorf("findings %v lack %s", codes, c)
				}
			}
			for _, c := range tc.absent {
				if slices.Contains(codes, c) {
					t.Errorf("findings %v hold %s, which should not be raised", codes, c)
				}
			}
			found := tc.msg == ""
			for _, f := range res.Report.Findings {
				if tc.msg != "" && strings.Contains(f.Message, tc.msg) {
					found = true
				}
			}
			if !found {
				t.Errorf("no finding message holds %q: %v", tc.msg, res.Report.Findings)
			}
			if res.Report.Counts.Blocked != tc.blocked {
				t.Errorf("blocked = %d, want %d; findings %v", res.Report.Counts.Blocked, tc.blocked, codes)
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

// rc builds a refusalCase from a name, the tasks, the blocked count, one code
// and the message.
func rc(name, tasks string, blocked int, code playbook.Code, msg string) refusalCase {
	c := refusalCase{nativeCase: nativeCase{name: name, tasks: tasks, blocked: blocked}, msg: msg}
	if code != "" {
		c.codes = []playbook.Code{code}
	}
	return c
}

// factDef defines tier at run time, so a read of it stays a template.
const factDef = "- {name: pick, set_fact: {tier: prod}, when: x is defined}\n"

// factWant is that task as the runbook holds it.
const factWant = `  - name: pick
    when: ["false"]
    set_fact: {tier: prod}
`

// TestNativeRefusals_Templates covers the templates a native loop's task
// holds that the runbook cannot read, and the ones an unrolled loop's item
// is written into.
func TestNativeRefusals_Templates(t *testing.T) {
	list := "- {name: list, command: ls, register: f}\n"
	each := func(cmd string) string {
		return list + "- {name: each, command: \"" + cmd + "\", loop: \"{{ f.stdout }}\"}"
	}
	unrolled := func(cmd, loop string) string {
		return factDef + "- {name: each, command: \"" + cmd + "\", loop: " + loop + "}"
	}
	cases := []refusalCase{
		rc("whitespace control in a looped command", each("echo {{- item }}"), 1, "template.unsupported", "whitespace control"),
		rc("a looped command calling a function", each("echo {{ foo(item) }}"), 1, "template.unsupported", "Jinja this converter cannot evaluate"),
		rc("a looped command reading a registered result whole", each("echo {{ f }}"), 1, "template.unsupported", "read as a whole"),
		rc("a looped command reading a field the method lacks", each("echo {{ f.bogus }}"), 1, "template.unsupported", "returns no equivalent of Ansible's bogus"),
		rc("an unrolled item with a filter", unrolled("echo {{ item | upper }} {{ tier }}", "[a]"), 1, "template.unsupported", "more than a plain path"),
		rc("an unrolled item without the field", unrolled("echo {{ item.zz }} {{ tier }}", "[{n: a}]"), 1, "template.unsupported", "item has no zz"),
		rc("an unrolled item without the index", unrolled("echo {{ item.n[5] }} {{ tier }}", "[{n: [a]}]"), 1, "template.unsupported", "item has no 5"),
		rc("an unrolled item that is a boolean", unrolled("echo {{ item }} {{ tier }}", "[true]"), 1, "template.unsupported", "is not text"),
		rc("an unrolled number", unrolled("echo {{ item }} {{ tier }}", "[7]"), 1, "template.unsupported", "is not text"),
		rc("an unrolled item that is a map", unrolled("echo {{ item }} {{ tier }}", "[{n: a}]"), 1, "template.unsupported", "map or a list"),
		rc("a call on a variable read at run time", factDef+"- {name: c, command: \"echo {{ tier(1) }}\"}", 1, "template.unsupported", "Jinja this converter cannot evaluate"),
	}
	good := rc("an unrolled item read through every kind of path step",
		factDef+"- {name: each, command: \"echo {{ item.n }} {{ item.m[0] }} {{ item['n'] }} {{ item[\\\"n\\\"] }} {{ tier }}\", loop: [{n: a, m: [q]}]}",
		0, "loop.unrolled", "")
	good.want = "tasks:\n" + factWant + `  - name: each (item 1 of 1)
    exec.command: {cmd: "echo a q a a {{ vars.tier | quote }}"}`
	runRefusals(t, append(cases, good))
}

// TestNativeRefusals_Actions covers set_fact, assert and fail given a form the
// runbook cannot carry, and the ones it can.
func TestNativeRefusals_Actions(t *testing.T) {
	cond := "x is defined"
	sf := func(facts, extra string) string {
		return "- {name: s, set_fact: " + facts + extra + ", when: " + cond + "}"
	}
	cases := []refusalCase{
		rc("set_fact written as key=value text", "- {name: s, set_fact: \"a=1\"}", 1, "set_fact.runtime", "not a map"),
		rc("set_fact that registers", sf("{a: 1}", ", register: r"), 1, "assert.unmapped", "registers nothing"),
		rc("set_fact with until", sf("{a: 1}", ", until: r.rc == 0"), 1, "assert.unmapped", "read a method's result"),
		rc("set_fact with a timeout", sf("{a: 1}", ", timeout: 5"), 1, "assert.unmapped", "timeout bounds"),
		rc("set_fact with a repeated key", sf("{a: 1, a: 2}", ""), 1, "yaml.duplicate_key", "key a is repeated"),
		rc("set_fact naming a secret", sf("{db_password: abc}", ""), 1, "vars.unreadable", "fact name"),
		rc("set_fact naming something vars cannot read", sf("{bad-name: abc}", ""), 1, "vars.unreadable", "fact name"),
		rc("set_fact of a vault value", sf("{a: !vault \"$ANSIBLE_VAULT;1.1;AES256\\n6162\"}", ""), 1, "vault.value", "vault-encrypted"),
		rc("set_fact of nothing but cacheable", sf("{cacheable: true}", ""), 1, "assert.unmapped", "sets nothing"),
		rc("set_fact of a name nothing defines", sf("{a: \"{{ nowhere.x }}\"}", ""), 1, "template.unresolved", "nowhere"),
		rc("set_fact with a task var the runbook cannot read", sf("{a: 1}", ", vars: {bad-n: 2}"), 1, "vars.unreadable", "cannot read"),
		rc("set_fact under a condition that does not translate", "- {name: s, set_fact: {a: 1}, when: \"x is version('1','>=')\"}", 1, "when.unsupported", "version"),
		rc("assert in key=value text reads a name nothing defines", "- {name: s, assert: \"that=x\"}", 1, "when.unsupported", "x is not defined"),
		rc("assert in words", "- {name: s, assert: \"just words here\"}", 1, "args.unparsable", "map of arguments"),
		rc("assert given a list", "- {name: s, assert: [a]}", 1, "args.unparsable", "map of arguments"),
		rc("assert with a repeated argument", "- {name: s, assert: {that: true, that: false}}", 1, "args.unparsable", "map of arguments"),
		rc("assert with nothing", "- name: s\n  assert:", 1, "assert.unmapped", "gives no that"),
		rc("assert with a list for a message", "- {name: s, assert: {that: true, fail_msg: [a]}}", 1, "args.value", "not one text"),
		rc("assert with a message naming nothing defined", "- {name: s, assert: {that: true, msg: \"{{ nowhere.x }}\"}}", 1, "template.unresolved", "nowhere"),
		rc("assert that registers", "- {name: s, assert: {that: true}, register: r}", 1, "assert.unmapped", "registers nothing"),
		rc("assert with a timeout", "- {name: s, assert: {that: true}, timeout: 3}", 1, "assert.unmapped", "timeout bounds"),
		rc("assert with changed_when", "- {name: s, assert: {that: true}, changed_when: false}", 1, "assert.unmapped", "read a method's result"),
		rc("assert under a condition that does not translate", "- {name: s, assert: {that: true}, when: \"x is version('1','>=')\"}", 1, "when.unsupported", "version"),
		rc("assert with a task var the runbook cannot read", "- {name: s, assert: {that: true}, vars: {bad-n: 1}}", 1, "vars.unreadable", "cannot read"),
		rc("assert in a loop", "- {name: s, assert: {that: true}, loop: [a]}", 1, "assert.unmapped", "no native form"),
		rc("fail in words", "- {name: s, fail: \"words only\"}", 1, "args.unparsable", "map of arguments"),
		rc("fail with another argument", "- {name: s, fail: {msg: a, other: b}}", 1, "assert.unmapped", "no argument other"),
		rc("fail with a list for a message", "- {name: s, fail: {msg: [a]}}", 1, "args.value", "not one text"),
		rc("fail with a message naming nothing defined", "- {name: s, fail: {msg: \"{{ nowhere.x }}\"}}", 1, "template.unresolved", "nowhere"),
		rc("fail that registers", "- {name: s, fail: {msg: a}, register: r}", 1, "assert.unmapped", "registers nothing"),
		rc("fail under a condition that does not translate", "- {name: s, fail: {msg: a}, when: \"x is version('1','>=')\"}", 1, "when.unsupported", "version"),
		rc("fail in a loop", "- {name: s, fail: {msg: a}, loop: [a]}", 1, "assert.unmapped", "no native form"),
	}
	ign := rc("set_fact with its own ignore_errors", sf("{a: 1}", ", ignore_errors: true"), 0, "keyword.ignore_errors", "")
	ign.want = "tasks:\n  - {name: s, when: ['false'], set_fact: {a: 1}, ignore_errors: true}"
	numMsg := rc("assert with a number for a message", "- {name: s, assert: {that: true, msg: 3}}", 0, "", "")
	numMsg.want = "tasks:\n  - {name: s, assert: {that: ['true'], fail_msg: '3'}}"
	freeFail := rc("fail in key=value text", "- {name: s, fail: \"msg=stop\"}", 0, "", "")
	freeFail.want = "tasks:\n  - {name: s, fail: {msg: stop}}"
	runRefusals(t, append(cases, ign, numMsg, freeFail))
}

// TestNativeRefusals_ScopeKeywords covers the keywords a block puts on every
// task in it.
func TestNativeRefusals_ScopeKeywords(t *testing.T) {
	blk := func(kw string) string { return "- block: [{name: a, command: x}]\n  " + kw }
	cases := []refusalCase{
		rc("failed_when on a block reads a result", blk("failed_when: r.rc > 0"), 1, "keyword.failed_when", "cannot be shared"),
		rc("changed_when on a block", blk("changed_when: false"), 1, "keyword.changed_when", "cannot be shared"),
		rc("until on a block", blk("until: r.rc == 0"), 1, "keyword.retries", "cannot be shared"),
		rc("retries on a block", blk("retries: 3"), 1, "keyword.retries", "cannot be shared"),
		rc("delay on a block", blk("delay: 3"), 1, "keyword.retries", "cannot be shared"),
		rc("a templated ignore_errors on a block", blk("ignore_errors: \"{{ soft }}\""), 1, "keyword.ignore_errors", "is templated"),
	}
	off := rc("ignore_errors false on a block", blk("ignore_errors: false"), 0, "", "")
	off.want = "tasks:\n  - name: ''\n    block:\n      - {name: a, exec.command: {cmd: x}}"
	fw := rc("failed_when false on a block", blk("failed_when: false"), 0, "keyword.ignore_errors", "failed_when: false on a block")
	fw.want = "tasks:\n  - name: ''\n    block:\n      - {name: a, exec.command: {cmd: x}, ignore_errors: true}"
	runRefusals(t, append(cases, off, fw))
}

// TestNativeRefusals_NativeLoopTemplates covers the expressions a native
// loop's task is rewritten with and the variables it cannot read.
func TestNativeRefusals_NativeLoopTemplates(t *testing.T) {
	list := "- {name: list, command: ls, register: f}\n"
	each := func(cmd string) string {
		return list + "- {name: each, command: \"" + cmd + "\", loop: \"{{ f.stdout }}\"}"
	}
	listWant := "tasks:\n  - {name: list, exec.command: {cmd: ls}, register: f}\n"
	eachWant := func(cmd string) string {
		return listWant + "  - name: each\n    exec.command: {cmd: " + cmd + "}\n    loop: \"{{ result.f.stdout }}\""
	}
	quoted := rc("a quoted string inside an expression", each("echo {{ item | default('x') }}"), 0, "template.quoted", "")
	quoted.want = eachWant(`"echo {{ item | default('x') | quote }}"`)
	dq := rc("a double quoted string inside an expression", each("echo {{ item.n | default(\\\"a b\\\") }}"), 0, "template.quoted", "")
	dq.want = eachWant(`'echo {{ item.n | default("a b") | quote }}'`)
	esc := rc("an escaped quote inside a string", each("echo {{ item.n | default('a\\\\'b') }}"), 0, "template.quoted", "")
	esc.want = eachWant(`"echo {{ item.n | default('a\\'b') | quote }}"`)
	num := rc("a number inside an expression", each("echo {{ item.n | default(2) }}"), 0, "template.quoted", "")
	num.want = eachWant(`"echo {{ item.n | default(2) | quote }}"`)
	vars := rc("a play variable read beside the item", each("echo {{ app }} {{ item }}"), 0, "template.quoted", "")
	vars.play = "  vars: {app: /a}"
	vars.want = "vars: {app: /a}\n" + eachWant(`"echo {{ vars.app | quote }} {{ item | quote }}"`)
	cases := []refusalCase{
		rc("a fact read beside the item", each("echo {{ ansible_hostname }} {{ item }}"), 1, "template.fact", "known only on a host"),
		rc("a name nothing defines read beside the item", each("echo {{ undefinedvar }} {{ item }}"), 1, "template.unresolved", "undefinedvar"),
	}
	secret := rc("a secret named variable read beside the item", each("echo {{ api_token }} {{ item }}"), 2, "vars.unreadable", "named like a secret")
	secret.play = "  vars: {api_token: abcdefghijkl}"
	secret.tasks = strings.Replace(secret.tasks, "loop: \"{{ f.stdout }}\"}", "loop: \"{{ f.stdout }}\", vars: {api_token: other}}", 1)
	shaped := rc("a variable holding a secret shaped value read beside the item", each("echo {{ mode }} {{ item }}"), 1, "template.secret", "shaped like a secret")
	shaped.play = "  vars: {mode: a}"
	shaped.tasks = strings.Replace(shaped.tasks, "loop: \"{{ f.stdout }}\"}", "loop: \"{{ f.stdout }}\", vars: {mode: \\\"https://u:SENTINELPASS@example.com/r.git\\\"}}", 1)
	vault := rc("a vault variable read beside the item", each("echo {{ mode }} {{ item }}"), 2, "vars.unreadable", "vault-encrypted")
	vault.play = "  vars: {mode: !vault \"$ANSIBLE_VAULT;1.1;AES256\\n6162\"}"
	vault.tasks = strings.Replace(vault.tasks, "loop: \"{{ f.stdout }}\"}", "loop: \"{{ f.stdout }}\", vars: {mode: b}}", 1)
	argv := rc("an argv list with the item in it", list+"- {name: each, command: {argv: [echo, \"{{ item }}\", plain]}, loop: \"{{ f.stdout }}\"}", 0, "loop.native", "")
	argv.want = listWant + "  - name: each\n    exec.command: {argv: [echo, '{{ item }}', plain]}\n    loop: \"{{ result.f.stdout }}\""
	lit := rc("a loop list holding a registered result", list+"- {name: each, command: echo, loop: [a, \"{{ f.stdout }}\"]}", 0, "loop.native", "")
	lit.want = listWant + "  - name: each\n    exec.command: {cmd: echo}\n    loop: [a, \"{{ result.f.stdout }}\"]"
	label := rc("a label reading a name nothing defines", list+"- {name: each, command: \"echo {{ item }}\", loop: \"{{ f.stdout }}\", loop_control: {label: \"{{ nowhere }}\"}}", 1, "loop.control", "label")
	runRefusals(t, append(cases, quoted, dq, esc, num, vars, secret, shaped, vault, argv, lit, label))
}

// TestNativeRefusals_Loops covers the loops that are refused, and the native
// loops written as lists.
func TestNativeRefusals_Loops(t *testing.T) {
	list := "- {name: list, command: ls, register: f}\n"
	nums := make([]string, 300)
	for i := range nums {
		nums[i] = "1"
	}
	long := "[" + strings.Join(nums, ",") + "]"
	withVars := func(c refusalCase, play string) refusalCase { c.play = play; return c }
	cases := []refusalCase{
		rc("with_dict that registers", "- {name: d, command: \"echo {{ item.key }}\", with_dict: {a: 1}, register: r}", 1, "loop.register", "cannot take a register"),
		withVars(rc("a loop over a list past the bound", "- {name: d, command: \"echo {{ item }}\", loop: \"{{ xs }}\"}", 1, "loop.too_long", "300 items"), "  vars: {xs: "+long+"}"),
		withVars(rc("a loop over text", "- {name: d, command: \"echo {{ item }}\", loop: \"{{ s }}\"}", 1, "loop.runtime", "not a list known here"), "  vars: {s: text}"),
		withVars(rc("with_items over text", "- {name: d, command: \"echo {{ item }}\", with_items: \"{{ s }}\"}", 1, "loop.runtime", "not a list known here"), "  vars: {s: text}"),
		rc("with_dict over a list", "- {name: d, command: \"echo {{ item.key }}\", with_dict: [a]}", 1, "loop.runtime", "not a list known here"),
		rc("with_dict over a name nothing defines", "- {name: d, command: \"echo {{ item.key }}\", with_dict: \"{{ nowhere }}\"}", 1, "loop.runtime", "nowhere"),
		rc("a loop of text around a registered result", list+"- {name: d, command: \"echo {{ item }}\", loop: \"a {{ f.stdout }}\"}", 1, "loop.runtime", "one expression that renders to one"),
		rc("a loop over a registered result read whole", list+"- {name: d, command: \"echo {{ item }}\", loop: \"{{ f }}\"}", 1, "loop.runtime", "read as a whole"),
		rc("a loop over a map that registers", list+"- {name: d, command: \"echo {{ item }}\", loop: {a: 1}, register: r}", 1, "loop.runtime", "not a list"),
		rc("a loop with no list that registers", list+"- {name: d, command: \"echo {{ item }}\", loop: , register: r}", 1, "loop.runtime", "not a list, or one expression"),
		rc("a native with_items list past the bound", list+"- {name: d, command: \"echo {{ item }}\", with_items: [\"{{ f.stdout }}\", "+long+"], register: r}", 1, "loop.runtime", "301 items"),
	}
	flat := rc("with_items flattens one level beside a registered result", list+"- {name: d, command: \"echo {{ item }}\", with_items: [[a, \"{{ f.stdout }}\"], b]}", 0, "loop.native", "")
	flat.want = "tasks:\n  - {name: list, exec.command: {cmd: ls}, register: f}\n  - name: d\n    exec.command: {cmd: \"echo {{ item | quote }}\"}\n    loop: [a, \"{{ result.f.stdout }}\", b]"
	ctl := rc("a loop list of registered results with a label and an index", list+"- {name: d, command: \"echo {{ item }}\", loop: [\"{{ f.stdout }}\", \"{{ f.stdout }}\"], loop_control: {label: \"{{ item }}\", index_var: i}}", 0, "loop.native", "")
	ctl.want = "tasks:\n  - {name: list, exec.command: {cmd: ls}, register: f}\n  - name: d\n    exec.command: {cmd: \"echo {{ item | quote }}\"}\n    loop: [\"{{ result.f.stdout }}\", \"{{ result.f.stdout }}\"]\n    loop_control: {label: \"{{ item }}\", index_var: i}"
	runRefusals(t, append(cases, flat, ctl))
}

// TestNativeRefusals_Conditions covers conditions written wrongly or with a
// form the translator reads and refuses by name, and the forms it translates
// that the other tests do not use.
func TestNativeRefusals_Conditions(t *testing.T) {
	run := "- {name: r, command: probe, register: r}\n"
	when := func(cond string) string {
		return run + "- {name: w, command: go, when: \"" + cond + "\"}"
	}
	cases := []refusalCase{
		rc("a comparison with nothing after it", when("r.rc =="), 1, "when.unsupported", "ends too soon"),
		rc("a bracket never closed", when("(r.rc == 0"), 1, "when.unsupported", "missing )"),
		rc("a list without its commas", when("r.rc in [1 2]"), 1, "when.unsupported", "expected , or ] in a list"),
		rc("a list that stops", when("r.rc in [1,"), 1, "when.unsupported", "ends too soon"),
		rc("a bracket holding nothing", when("r.rc == ()"), 1, "when.unsupported", "unexpected"),
		rc("not with nothing after it", when("not"), 1, "when.unsupported", "ends too soon"),
		rc("a stray not", when("r.rc not 3"), 1, "when.unsupported", "unexpected"),
		rc("a search with no brackets", when("r.stdout is search"), 1, "when.unsupported", "needs a pattern in parentheses"),
		rc("a search for a variable", when("r.stdout is search(r.rc)"), 1, "when.unsupported", "written out as text"),
		rc("a search with a second argument", when("r.stdout is search('a', 'b')"), 1, "when.unsupported", "takes only a pattern"),
		rc("a filter the translator lacks", when("r.stdout | upper == 'A'"), 1, "when.unsupported", "filter"),
		rc("a keyword where a value belongs", when("r.rc == and"), 1, "when.unsupported", "cannot stand where a value belongs"),
		rc("a closing bracket where a value belongs", when("r.rc == )"), 1, "when.unsupported", "unexpected"),
		rc("a condition longer than the bound", when(strings.Repeat("r.rc == 0 and ", 400)+"r.rc == 0"), 1, "when.unsupported", "longer than"),
		rc("a condition nested too deep", when(strings.Repeat("(", 40)+"r.rc == 0"+strings.Repeat(")", 40)), 1, "when.unsupported", "nests too deeply"),
	}
	undef := rc("is undefined on a variable the play defines", "- {name: w, command: go, when: app is undefined}", 0, "", "")
	undef.play = "  vars: {app: /srv}"
	undef.want = "tasks:\n  - name: w\n    exec.command: {cmd: go}\n    when: ['false']"
	runRefusals(t, append(cases, undef))
}

// TestNativeRefusals_Statements covers the {% %} forms a run-time template
// may not hold: each reads a registered result, so it stays a template, and
// each is blocked. A malformed or unsupported statement is refused before
// the rewrite, as Jinja the converter cannot evaluate, so every one reads
// the same; an expression never closed is refused earlier still, as free-form
// arguments that do not parse.
func TestNativeRefusals_Statements(t *testing.T) {
	run := "- {name: r, command: probe, register: out}\n"
	shell := func(text string) string {
		return run + "- {name: s, shell: \"" + text + " {{ out.stdout }}\"}"
	}
	runRefusals(t, []refusalCase{
		rc("a for without in", shell("{% for h %}x{% endfor %}"), 1, "template.unsupported", "Jinja this converter cannot evaluate"),
		rc("Jinja this converter cannot evaluate", shell("{% endfor %}"), 1, "template.unsupported", "Jinja this converter cannot evaluate"),
		rc("a for never closed", shell("{% for h in out.lines %}x"), 1, "template.unsupported", "Jinja this converter cannot evaluate"),
		rc("a set block", shell("{% set x %}y{% endset %}"), 1, "template.unsupported", "Jinja this converter cannot evaluate"),
		rc("a macro", shell("{% macro m() %}x{% endmacro %}"), 1, "template.unsupported", "Jinja this converter cannot evaluate"),
		rc("a raw block never closed", shell("{% raw %}x"), 1, "template.unsupported", "Jinja this converter cannot evaluate"),
		rc("a tag never closed", run+"- {name: s, shell: \"{{ out.stdout }} {% if out.rc\"}", 1, "template.unsupported", "Jinja this converter cannot evaluate"),
		rc("a comment never closed", run+"- {name: s, shell: \"{{ out.stdout }} {# note\"}", 1, "template.unsupported", "Jinja this converter cannot evaluate"),
		rc("an expression never closed", run+"- {name: s, shell: \"{{ out.stdout }} {{ out.rc\"}", 1, "args.unparsable", "unbalanced quotes or {{ }}"),
		rc("an if reading a call", shell("{% if foo(out.rc) %}x{% endif %}"), 1, "template.unsupported", "Jinja this converter cannot evaluate"),
		rc("a for over a call", shell("{% for h in foo(out.rc) %}x{% endfor %}"), 1, "template.unsupported", "Jinja this converter cannot evaluate"),
		rc("a set of a call", shell("{% set x = foo(out.rc) %}"), 1, "template.unsupported", "Jinja this converter cannot evaluate"),
	})
}

// TestNativeRefusals_TaskVarsHoldingRunTimeReads covers a task's vars whose
// list and map values hold a registered result: each template inside them is
// rewritten to read result.<register>, and the command reading them through
// vars converts, builds and validates.
func TestNativeRefusals_TaskVarsHoldingRunTimeReads(t *testing.T) {
	c := rc("a list and a map in task vars, each reading a register",
		"- {name: r, command: probe, register: out}\n"+
			"- {name: s, command: \"echo {{ pair[0] }} {{ m.k }}\", vars: {pair: [\"{{ out.stdout }}\", b], m: {k: \"{{ out.stdout }}\"}}}",
		0, "template.register", "")
	c.want = "tasks:\n" +
		"  - {name: r, exec.command: {cmd: probe}, register: out}\n" +
		"  - name: s\n" +
		"    exec.command: {cmd: \"echo {{ vars.pair[0] | quote }} {{ vars.m.k | quote }}\"}\n" +
		"    vars: {m: {k: \"{{ result.out.stdout }}\"}, pair: [\"{{ result.out.stdout }}\", b]}"
	runRefusals(t, []refusalCase{c})
}
