// Phase 136a's release gate: a pleiades built with no Collection linked (the
// nocatalog tag, the shape of the core once each group ships as a program of
// its own) still reserves every first-party name and still says which
// Collection holds a first-party method it does not include.
package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestCLI_WithNoCollectionLinkedTheCoreStillReservesAndRoutes builds the real
// binary with the catalog unlinked and proves, through it: an approved
// program claiming container.image.pull is refused for the container
// namespace (before Phase 136a nothing reserved it in such a build); a runbook
// calling container.image.pull is refused naming the container Collection; and
// the playbook converter's report names the Collection a mapped method needs.
// (pleiades doc holds its compiled catalog data to the registry and refuses on
// such a build before it looks a name up; docs from installed manifests are the
// split's S12.)
func TestCLI_WithNoCollectionLinkedTheCoreStillReservesAndRoutes(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a second pleiades binary")
	}
	core := filepath.Join(t.TempDir(), "pleiades-core")
	build := exec.Command("go", "build", "-tags", "nocatalog", "-o", core, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building with the catalog unlinked: %v\n%s", err, out)
	}
	run := func(dir string, env map[string]string, args ...string) (string, error) {
		t.Helper()
		cmd := exec.Command(core, args...)
		cmd.Dir = dir
		cmd.Env = os.Environ()
		for k, v := range env {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	collections := t.TempDir()
	if err := os.Chmod(collections, 0o700); err != nil {
		t.Fatal(err)
	}
	impostor := strings.ReplaceAll(promptProgram, "clitest.prompt.run", "container.image.pull")
	if err := os.WriteFile(filepath.Join(collections, "impostor"), []byte(impostor), 0o700); err != nil { // #nosec G306 -- a test program that must be executable
		t.Fatal(err)
	}
	env := map[string]string{"PLEIADES_COLLECTIONS_DIR": collections}
	if out, err := run(t.TempDir(), env, "collection", "approve", "impostor", "--yes"); err != nil {
		t.Fatalf("approving: %v\n%s", err, out)
	}
	if out, err := run(t.TempDir(), env, "doc", "--list"); err == nil || !strings.Contains(out, `"container" namespace, which belongs to The Pleiades itself`) {
		t.Fatalf("doc --list with the impostor = %v, want it refused for the container namespace:\n%s", err, out)
	}
	// A prefix counts only at a dot: a program named netbox is its own, and
	// loads once the impostor is gone.
	if err := os.Remove(filepath.Join(collections, "impostor")); err != nil {
		t.Fatal(err)
	}
	netbox := strings.Replace(strings.ReplaceAll(promptProgram, "clitest.prompt.run", "netbox.devices.sync"), `"supportsCheck":false}`, `"supportsCheck":false,"reversibility":{"readOnly":true,"notes":"a test"}}`, 1)
	if err := os.WriteFile(filepath.Join(collections, "netbox"), []byte(netbox), 0o700); err != nil { // #nosec G306 -- a test program that must be executable
		t.Fatal(err)
	}
	if out, err := run(t.TempDir(), env, "collection", "approve", "netbox", "--yes"); err != nil {
		t.Fatalf("approving netbox: %v\n%s", err, out)
	}

	dir := t.TempDir()
	for _, args := range [][]string{{"init"}, {"add-host", "dockerhost", "--type", "linux_server"}} {
		if out, err := run(dir, nil, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	runbook := "# Pulls one image (the release gate's runbook; changes nothing, no undo).\nid: pull\nhosts: dockerhost\ntasks:\n  - name: pull alpine\n    container.image.pull:\n      image: alpine:3\n"
	if err := os.WriteFile(filepath.Join(dir, "runbooks", "pull.yaml"), []byte(runbook), 0o600); err != nil {
		t.Fatal(err)
	}
	// No catalog: even file.copy is a method this build does not include.
	copyRunbook := "# Copies one file (the release gate's runbook; one file written, undo removes it).\nid: copy\nhosts: dockerhost\ntasks:\n  - name: copy a file\n    file.copy:\n      src: a.txt\n      dest: /tmp/a.txt\n"
	if err := os.WriteFile(filepath.Join(dir, "runbooks", "copy.yaml"), []byte(copyRunbook), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := run(dir, nil, "validate", "runbooks/copy.yaml"); err == nil || !strings.Contains(out, "it belongs to the first-party file Collection, which this build does not include") {
		t.Fatalf("validate copy.yaml = %v, want the file Collection named:\n%s", err, out)
	}
	netboxRunbook := "# Reads devices from NetBox (the release gate's runbook; changes nothing, no undo).\nid: sync\nhosts: dockerhost\ntasks:\n  - name: sync\n    netbox.devices.sync: {}\n"
	if err := os.WriteFile(filepath.Join(dir, "runbooks", "sync.yaml"), []byte(netboxRunbook), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := run(dir, env, "validate", "runbooks/sync.yaml"); strings.Contains(out, "belongs to The Pleiades itself") || strings.Contains(out, "not a registered collection name") {
		t.Fatalf("validate sync.yaml = %v, want the netbox program loaded and its method known:\n%s", err, out)
	}
	out, err := run(dir, nil, "validate", "runbooks/pull.yaml")
	if err == nil || !strings.Contains(out, `"container.image.pull", which is not a registered collection name; it belongs to the first-party container Collection, which this build does not include`) {
		t.Fatalf("validate = %v, want the container Collection named:\n%s", err, out)
	}

	// The converter's report names the Collection too: ansible.builtin.file
	// maps to file.directory, which this build does not include.
	mig := t.TempDir()
	writeFile(t, mig, "site.yml", cleanPlaybook)
	if out, err := run(mig, nil, "forge", "migrate-playbook", "site.yml"); !strings.Contains(out, "file.directory is not a registered native method; it belongs to the first-party file Collection") {
		t.Fatalf("migrate-playbook = %v, want the file Collection named:\n%s", err, out)
	}
}
