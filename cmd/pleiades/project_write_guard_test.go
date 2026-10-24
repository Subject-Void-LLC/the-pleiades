// End-to-end tests, through the built binary and no device, of what
// pleiades.builtin.project.write refuses: a path a device's output could
// choose, the project's inventory, an executable mode; and of the advice a
// command gives for a variable it has no flag to take.
package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// guardProject is a project with one device, box, whose address nothing
// contacts: every task here acts on no device.
func guardProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init"},
		{"add-host", "box", "--type", "linux_server", "--set", "host=192.0.2.1"},
	} {
		if out, err := runPleiades(t, dir, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	return dir
}

// TestCLI_ProjectWriteRefusesWhatARunMustNotReplace: a write to the
// inventory or its state file, and a mode with an execute bit, fail the
// task and leave the file as it was; a path rendered from a registered
// result is refused before the run, while one from --extra-vars is written.
func TestCLI_ProjectWriteRefusesWhatARunMustNotReplace(t *testing.T) {
	dir := guardProject(t)
	inventoryFile := filepath.Join(dir, "inventory.yaml")
	before, err := os.ReadFile(inventoryFile)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"path=inventory.yaml", "content=hosts: []"}, "which a run never writes"},
		{[]string{"path=INVENTORY.yaml", "content=hosts: []"}, "which a run never writes"},
		{[]string{"path=.inventory-state.generated.yaml", "content=hosts: []"}, "which a run never writes"},
		{[]string{"path=configs/run.sh", "content=x", "mode=0755"}, "sets an execute bit"},
	} {
		args := append([]string{"adhoc", "box", "pleiades.builtin.project.write"}, tc.args...)
		rep, raw, code := runPleiadesJSON(t, dir, args...)
		if code == 0 || len(rep.Tasks) != 1 || rep.Tasks[0].Status != "failed" || !strings.Contains(rep.Tasks[0].Error, tc.want) {
			t.Errorf("%v: exit %d, want a failed task saying %q:\n%s", tc.args, code, tc.want, raw)
		}
	}
	if after, _ := os.ReadFile(inventoryFile); string(after) != string(before) {
		t.Errorf("the inventory changed:\n%s", after)
	}
	for _, name := range []string{".inventory-state.generated.yaml", "configs/run.sh"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			t.Errorf("%s was written", name)
		}
	}

	// A path from a registered result is the device's choice: refused at
	// validation, before the run; the same path from --extra-vars is the
	// person's, and is written.
	rb := filepath.Join(dir, "runbooks", "keep.yaml")
	body := `# Keeps a value in the project, under a path the person chose.
# Blast radius: none; acts on no device. Changes: one project file.
# Undo: none; the project is under version control.
id: keep
tasks:
  - name: read
    noop:
      name: inventory.yaml
    register: r
  - name: keep
    pleiades.builtin.project.write:
      path: PATH
      content: kept
`
	if err := os.WriteFile(rb, []byte(strings.Replace(body, "PATH", `"{{ result.r.name }}"`, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := runPleiades(t, dir, "validate", "runbooks/keep.yaml"); err == nil || !strings.Contains(out, "may read only vars, never result") {
		t.Errorf("a path from a result passed validation: %v\n%s", err, out)
	}
	rep, raw, code := runPleiadesJSON(t, dir, "run", "runbooks/keep.yaml")
	if code == 0 || rep.Outcome.Status != "invalid" {
		t.Errorf("a path from a result ran: exit %d:\n%s", code, raw)
	}
	if err := os.WriteFile(rb, []byte(strings.Replace(body, "PATH", `"configs/{{ vars.site }}.conf"`, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if rep, raw, code := runPleiadesJSON(t, dir, "run", "runbooks/keep.yaml", "-e", "site=east"); code != 0 || rep.Outcome.Status != "complete" {
		t.Errorf("a path from --extra-vars: exit %d:\n%s", code, raw)
	}
	if got, err := os.ReadFile(filepath.Join(dir, "configs", "east.conf")); err != nil || string(got) != "kept" {
		t.Errorf("configs/east.conf holds %q, %v", got, err)
	}
	if after, _ := os.ReadFile(inventoryFile); string(after) != string(before) {
		t.Errorf("the inventory changed:\n%s", after)
	}
}

// TestCLI_AVariableAdviceNamesOnlyAFlagTheCommandHas: run tells a person
// to give a missing variable with --extra-vars; adhoc, which has no such
// flag, says it takes none rather than naming one.
func TestCLI_AVariableAdviceNamesOnlyAFlagTheCommandHas(t *testing.T) {
	dir := guardProject(t)
	rep, raw, code := runPleiadesJSON(t, dir, "adhoc", "box", "pleiades.builtin.project.write", "path=configs/{{ vars.site }}.conf", "content=x")
	if code == 0 || rep.Outcome.Status != "invalid" || len(rep.Validation) == 0 {
		t.Fatalf("adhoc reading a variable: exit %d:\n%s", code, raw)
	}
	for _, v := range rep.Validation {
		if v.Rule == "vars" && (strings.Contains(v.Message, "--extra-vars") || !strings.Contains(v.Message, "takes no variables")) {
			t.Errorf("adhoc's advice: %s", v.Message)
		}
	}

	rb := filepath.Join(dir, "runbooks", "site.yaml")
	if err := os.WriteFile(rb, []byte("# Writes one file. Blast radius: none. Changes: one project file. Undo: none.\nid: site\ntasks:\n  - name: keep\n    pleiades.builtin.project.write:\n      path: \"configs/{{ vars.site }}.conf\"\n      content: x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rep, raw, code = runPleiadesJSON(t, dir, "run", "runbooks/site.yaml")
	found := false
	for _, v := range rep.Validation {
		found = found || (v.Rule == "vars" && strings.Contains(v.Message, "give it with --extra-vars site=<value>"))
	}
	if code == 0 || !found {
		t.Errorf("run's advice does not name --extra-vars: exit %d:\n%s", code, raw)
	}
}
