// The form a converted runbook is written in.
package playbook_test

import (
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// TestEmit_ModuleAsKey: every task a conversion writes names its method as
// its key, in a block, a rescue, a placeholder and the incomplete guard
// alike, and no task is written as fqcn: and params:, which is not how a
// runbook is written.
func TestEmit_ModuleAsKey(t *testing.T) {
	res := convert(t, `- hosts: web
  gather_facts: false
  tasks:
    - name: curl
      apt: {name: curl}
    - block:
        - name: nginx up
          service: {name: nginx, state: started}
      rescue:
        - name: ping
          ping:
    - name: not converted
      pause: {seconds: 1}
`)
	if len(res.Runbooks) != 1 {
		t.Fatalf("%d runbooks", len(res.Runbooks))
	}
	text := string(res.Runbooks[0].YAML)
	var doc map[string]any
	if err := yaml.Unmarshal(res.Runbooks[0].YAML, &doc); err != nil {
		t.Fatal(err)
	}
	keys := map[string]bool{}
	var walk func(any)
	walk = func(v any) {
		for _, task := range v.([]any) {
			for k, sub := range task.(map[string]any) {
				keys[k] = true
				if k == "block" || k == "rescue" || k == "always" {
					walk(sub)
				}
			}
		}
	}
	walk(doc["tasks"])
	for _, want := range []string{"pkg.apt.install", "svc.start", "net.ssh.ping", "ansible.unconverted.pause", "ansible.incomplete"} {
		if !keys[want] {
			t.Errorf("no task is keyed %s:\n%s", want, text)
		}
	}
	if keys["fqcn"] || keys["params"] {
		t.Errorf("a task is written with fqcn: or params:\n%s", text)
	}
	if !strings.Contains(text, "    pkg.apt.install:\n      name: curl\n") {
		t.Errorf("the parameters are not the method key's value:\n%s", text)
	}
}
