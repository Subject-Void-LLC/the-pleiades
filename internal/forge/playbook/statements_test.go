// Tests that a playbook's Jinja statements convert into templates the
// platform renders as Ansible would, and that ansible.builtin.template
// becomes file.template with its block options.
package playbook_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
)

// TestTranslate_StatementsRenderAsAnsibleWould converts tasks whose
// arguments hold {% %}, then renders the runbook's parameter through the
// platform's renderer with the values Ansible would have, and compares.
func TestTranslate_StatementsRenderAsAnsibleWould(t *testing.T) {
	res := convert(t, `- hosts: web
  gather_facts: false
  vars: {env: prod}
  tasks:
    - name: resolved now
      command: "echo {% if env == 'prod' %}live{% else %}test{% endif %}"
    - set_fact: {hosts: [a, b]}
    - name: read at run time
      shell: "{% for h in hosts %}ping {{ h }}; {% endfor %}{# done #}"
    - name: probe
      command: cat /etc/flag
      register: out
    - name: decided at run time
      shell: "{% if out.stdout == 'yes' %}echo {{ out.stdout }}{% else %}true{% endif %}"
    - name: raw stays as written
      shell: "printf '{% raw %}{%s}{% endraw %}' x"
      loop: [1]
`)
	if len(res.Runbooks) != 1 || res.Runbooks[0].Incomplete {
		t.Fatalf("conversion incomplete: %v\n%s", codesOf(res.Report), res.Runbooks[0].YAML)
	}
	dag := build(t, res.Runbooks[0].YAML)
	params := map[string]string{}
	for _, task := range dag.Nodes {
		for _, key := range []string{"cmd", "argv"} {
			switch v := task.Params[key].(type) {
			case string:
				params[strings.SplitN(task.Name, " (item", 2)[0]] = v
			case []any:
				var parts []string
				for _, p := range v {
					s, _ := p.(string)
					parts = append(parts, s)
				}
				params[strings.SplitN(task.Name, " (item", 2)[0]] = strings.Join(parts, " ")
			}
		}
	}

	if got := params["resolved now"]; got != "echo live" {
		t.Errorf("a statement over a playbook variable resolved to %q, want %q as Ansible renders it", got, "echo live")
	}

	eng := render.New()
	for name, want := range map[string]string{"read at run time": "ping a; ping b; ", "decided at run time": "echo 'yes'", "raw stays as written": "printf '{%s}' x"} {
		src, ok := params[name]
		if !ok {
			t.Fatalf("no parameter for %q in %v", name, params)
		}
		tmpl, err := eng.Compile(src)
		if err != nil {
			t.Fatalf("%s: the converted template %q does not compile: %v", name, src, err)
		}
		got, err := tmpl.Render(map[string]any{"vars": map[string]any{}, "result": map[string]any{"out": map[string]any{"stdout": "yes"}}})
		if err != nil || got != want {
			t.Errorf("%s: %q renders %q, %v; want %q", name, src, got, err, want)
		}
	}
}

// TestTranslate_TemplateModule converts ansible.builtin.template to
// file.template, its trim_blocks and lstrip_blocks read by Ansible's
// boolean rules, with a review saying the template file's names change;
// an argument file.template cannot honor blocks the task.
func TestTranslate_TemplateModule(t *testing.T) {
	res := convert(t, `- hosts: web
  gather_facts: false
  tasks:
    - name: ntp
      template: {src: templates/ntp.conf.j2, dest: /etc/ntp.conf, mode: "0644", trim_blocks: no, lstrip_blocks: yes}
    - name: keep
      template: {src: a.j2, dest: /etc/a, force: no}
`)
	text := string(res.Runbooks[0].YAML)
	for _, want := range []string{"file.template:", "src: templates/ntp.conf.j2", "trim_blocks: false", "lstrip_blocks: true"} {
		if !strings.Contains(text, want) {
			t.Errorf("the conversion lacks %q:\n%s", want, text)
		}
	}
	codes := codesOf(res.Report)
	if !slices.Contains(codes, "module.semantics") || !slices.Contains(codes, "args.unmapped") {
		t.Errorf("findings %v, want module.semantics for the template file and args.unmapped for force", codes)
	}
}
