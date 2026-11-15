// CLI test through the built binary for run 10's first defect: a block with
// a rescue rendered its tasks without the device's group_vars and host_vars,
// so a task that rendered at the top level failed there with "no value
// supplied", though validate passed it. The runbook contacts no device: it
// uses the engine's own actions.
package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeProject writes files, relative paths to their contents, under dir.
func writeProject(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// varsProject is a project with web1 tagged web and edge1 tagged wan, web1
// bringing role from host_vars and site from group_vars, and runbook at
// runbooks/check.yaml, which validate must pass.
func varsProject(t *testing.T, runbook string) string {
	t.Helper()
	dir := t.TempDir()
	if out, err := runPleiades(t, dir, "init"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	for _, host := range [][]string{{"web1", "web"}, {"edge1", "wan"}} {
		if out, err := runPleiades(t, dir, "add-host", host[0], "--type", "linux_server", "--set", "host=127.0.0.1", "--tag", host[1]); err != nil {
			t.Fatalf("add-host %s: %v\n%s", host[0], err, out)
		}
	}
	writeProject(t, dir, map[string]string{
		"host_vars/web1.yml":  "role: spoke\n",
		"group_vars/web.yml":  "site: east\n",
		"runbooks/check.yaml": runbook,
	})
	if out, err := runPleiades(t, dir, "validate", "runbooks/check.yaml"); err != nil || !strings.Contains(out, "no issues found") {
		t.Fatalf("validate: %v\n%s", err, out)
	}
	return dir
}

// TestCLI_AGuardedBlockReadsTheDevicesVars: the block's task, its rescue and
// its always each render the device's host_vars and group_vars, as a
// top-level task does, so the failure the block means is the one it reports.
func TestCLI_AGuardedBlockReadsTheDevicesVars(t *testing.T) {
	dir := varsProject(t, `# What this does: reads web1's host_vars and group_vars in a block, its rescue and its always.
# Blast radius: none (engine actions only, no device is contacted).
# Changes: nothing.
# Undo: nothing to undo.
id: guarded
hosts: web
tasks:
  - name: guarded
    block:
      - name: break it
        fail:
          msg: "role {{ vars.role }} at {{ vars.site }}"
    rescue:
      - name: in the rescue
        assert:
          that: ["vars.site == 'east'"]
          fail_msg: "rescue saw {{ vars.role }}"
    always:
      - name: in the always
        set_fact:
          seen: "{{ vars.role }}-{{ vars.site }}"
`)
	out, err := runPleiades(t, dir, "run", "runbooks/check.yaml")
	if err != nil || strings.Contains(out, "no value supplied") || !strings.Contains(out, "failed: role spoke at east") || !strings.Contains(out, "run complete through a rescue") {
		t.Fatalf("run: %v\n%s", err, out)
	}
}
