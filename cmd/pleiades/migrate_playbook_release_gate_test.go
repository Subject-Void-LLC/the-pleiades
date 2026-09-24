// Release gate for `pleiades forge migrate-playbook`: a playbook real
// Ansible accepts converts into a runbook the platform's own validation
// accepts, with each construct that did not convert cleanly reported
// once, where it is written; and one that cannot convert is refused by
// validation and by run, on every task that did not convert.
package main_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
)

// gatePlaybook uses ansible.builtin only, in two plays on the same hosts:
// a resolved variable, a loop whose task carries become and an argument
// the native method drops (each checked for every copy the loop makes,
// and still reported once), a registered result read by when,
// ignore_errors, notify with its handler, and block/rescue/always.
const gatePlaybook = `- name: prepare
  hosts: web
  gather_facts: false
  vars:
    app_dir: /srv/app
  tasks:
    - name: make the app directory
      ansible.builtin.file:
        path: "{{ app_dir }}"
        state: directory
        mode: "0755"
    - name: install tools
      ansible.builtin.apt:
        name: "{{ item }}"
        update_cache: true
      loop: [curl, git]
      become: true
    - name: check the marker
      ansible.builtin.command: test -f /srv/app/ready
      register: marker
      ignore_errors: true
    - name: write the marker
      ansible.builtin.copy:
        dest: /srv/app/ready
        content: "ok\n"
        mode: "0644"
      when: marker.rc != 0
      notify: restart app
  handlers:
    - name: restart app
      ansible.builtin.service:
        name: app
        state: restarted
- name: deploy
  hosts: web
  gather_facts: false
  tasks:
    - block:
        - name: start the app
          ansible.builtin.service:
            name: app
            state: started
      rescue:
        - name: say it failed
          ansible.builtin.debug:
            msg: failed
      always:
        - name: record the deploy
          ansible.builtin.lineinfile:
            path: /srv/app/log
            line: deployed
`

// gateConstructs is each construct the gate playbook reports, and the
// text found where the report says it is.
var gateConstructs = map[string]string{
	"keyword.become":        "become",
	"module.semantics":      "update_cache",
	"loop.unrolled":         "loop",
	"keyword.ignore_errors": "ignore_errors",
	"keyword.notify":        "notify",
	"when.register":         "marker.rc",
	"handler.dropped":       "name: restart app",
	"block.rescue":          "rescue",
	"debug.dropped":         "ansible.builtin.debug",
	"block.always":          "always",
}

// gateBlockedPlaybook holds two tasks that cannot convert beside one that
// can.
const gateBlockedPlaybook = `- hosts: web
  gather_facts: false
  tasks:
    - name: render the config
      ansible.builtin.template:
        src: app.conf.j2
        dest: /etc/app.conf
    - name: drain it at the load balancer
      ansible.builtin.command: lb drain web
      delegate_to: lb1
    - name: plain
      ansible.builtin.command: uptime
`

// TestMigratePlaybookReleaseGate is the phase's release gate.
func TestMigratePlaybookReleaseGate(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the migrate-playbook release gate, which runs real Ansible in a container, in short mode")
	}
	image := testsupport.BuildAnsibleRunnerImage(t)
	dir := t.TempDir()
	writeFile(t, dir, "site.yml", gatePlaybook)
	writeFile(t, dir, "blocked.yml", gateBlockedPlaybook)
	for _, playbook := range []string{"site.yml", "blocked.yml"} {
		out, err := exec.Command("docker", "run", "--rm", "-v", dir+":/work:ro", image, "ansible-playbook", "--syntax-check", "/work/"+playbook).CombinedOutput()
		if err != nil {
			t.Fatalf("real Ansible refuses %s, so the gate would prove nothing: %v\n%s", playbook, err, out)
		}
	}
	if out, err := runPleiades(t, dir, "init"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	if out, err := runPleiades(t, dir, "add-host", "web", "--type", "linux_server", "--set", "host=10.0.0.5"); err != nil {
		t.Fatalf("add-host: %v\n%s", err, out)
	}

	stdout, stderr, code := runMigrate(t, dir, "site.yml", "--json")
	if code != 3 {
		t.Fatalf("exit %d, want 3 for a conversion with reviews: %s", code, stderr)
	}
	var report struct {
		Runbooks []struct {
			File     string `json:"file"`
			Runnable bool   `json:"runnable"`
		} `json:"runbooks"`
		Findings []struct {
			Code string `json:"code"`
			At   struct {
				Line, Column int
			} `json:"at"`
		} `json:"findings"`
		Counts struct {
			Converted, Blocked int
		} `json:"counts"`
	}
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("report is not JSON: %v\n%s", err, stdout)
	}
	if len(report.Runbooks) != 1 || report.Runbooks[0].File != "site.yaml" || !report.Runbooks[0].Runnable {
		t.Fatalf("runbooks = %+v, want one runnable site.yaml holding both plays", report.Runbooks)
	}
	if report.Counts.Blocked != 0 || report.Counts.Converted != 7 {
		t.Errorf("counts = %+v, want 7 converted and none blocked", report.Counts)
	}
	lines := strings.Split(gatePlaybook, "\n")
	var codes []string
	for _, f := range report.Findings {
		codes = append(codes, f.Code)
		want, known := gateConstructs[f.Code]
		if !known {
			t.Errorf("unexpected finding %s at %d:%d", f.Code, f.At.Line, f.At.Column)
			continue
		}
		if f.At.Line < 1 || f.At.Line > len(lines) || f.At.Column < 1 || !strings.HasPrefix(lines[f.At.Line-1][f.At.Column-1:], want) {
			t.Errorf("%s is reported at %d:%d, which is not where %q is written", f.Code, f.At.Line, f.At.Column, want)
		}
	}
	for code := range gateConstructs {
		if n := countOf(codes, code); n != 1 {
			t.Errorf("%s is reported %d times, want exactly once", code, n)
		}
	}
	out, err := runPleiades(t, dir, "validate")
	if err != nil || !strings.Contains(out, "no issues found") {
		t.Fatalf("validate refuses the conversion: %v\n%s", err, out)
	}

	if _, stderr, code := runMigrate(t, dir, "blocked.yml"); code != 3 {
		t.Fatalf("blocked conversion exit %d, want 3: %s", code, stderr)
	}
	incomplete := filepath.Join("runbooks", "blocked.incomplete.yaml")
	if _, err := os.Stat(filepath.Join(dir, incomplete)); err != nil {
		t.Fatalf("no incomplete runbook: %v", err)
	}
	for _, command := range [][]string{{"validate", incomplete}, {"run", incomplete}} {
		out, err := runPleiades(t, dir, command...)
		if err == nil {
			t.Fatalf("%s accepted an incomplete conversion:\n%s", command[0], out)
		}
		for _, want := range []string{"the guard an incomplete conversion starts with", "did not convert (ansible.builtin.template)", "did not convert (ansible.builtin.command)"} {
			if !strings.Contains(out, want) {
				t.Errorf("%s does not name %q:\n%s", command[0], want, out)
			}
		}
		if command[0] == "run" && !strings.Contains(out, "not executing") {
			t.Errorf("run did not refuse before executing:\n%s", out)
		}
	}
}

// countOf counts want in list.
func countOf(list []string, want string) int {
	n := 0
	for _, item := range list {
		if item == want {
			n++
		}
	}
	return n
}
