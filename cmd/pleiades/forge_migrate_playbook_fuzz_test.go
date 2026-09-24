// Command pleiades's fuzz target for `forge migrate-playbook`, called in
// process with a fuzzed playbook and output name.
package main

import (
	"os"
	"path/filepath"
	"testing"
)

// FuzzRunForgeMigratePlaybook calls runForgeMigratePlaybook directly with
// a fuzzed playbook and output name, for the reason
// forge_new_device_fuzz_test.go gives: the generic dispatch harness never
// reaches a forge subcommand's own flags. A bad playbook or name must
// return an error, never panic, and nothing may be written outside the
// test's own directory.
func FuzzRunForgeMigratePlaybook(f *testing.F) {
	f.Add("- hosts: web\n  tasks:\n    - {command: echo hi}\n", "runbooks")
	f.Add("- hosts: web\n  tasks:\n    - {template: {src: a, dest: b}}\n", "out")
	f.Add("{}", "../escape")
	f.Add("- hosts: [a, b]\n  tasks: []\n", "")
	f.Add("- &a {hosts: x, tasks: [*a]}", "runbooks")

	f.Fuzz(func(t *testing.T, content, out string) {
		parent := t.TempDir()
		dir := filepath.Join(parent, "work")
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		playbookPath := filepath.Join(dir, "site.yml")
		if err := os.WriteFile(playbookPath, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("runForgeMigratePlaybook panicked: %v", r)
			}
		}()
		_ = runForgeMigratePlaybook([]string{playbookPath, "--out", filepath.Join(dir, filepath.Base(filepath.Clean("/"+out))), "--force"})
		if entries, _ := os.ReadDir(parent); len(entries) != 1 {
			t.Fatalf("wrote outside the work directory: %v", entries)
		}
	})
}
