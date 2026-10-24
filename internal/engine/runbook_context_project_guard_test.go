// Tests that the paths a run may never write or read through the project
// writer and reader are matched by what they are, not only by their names.
package engine

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestWriteProjectFile_RefusesTheGuardedPaths: with the inventory, its
// state file (not yet written) and a collections directory guarded, as the
// command line guards them, a write to any of them or into the directory is
// refused, in any letter case and through a hard link to the inventory,
// and nothing is written; an ordinary project file still is.
func TestWriteProjectFile_RefusesTheGuardedPaths(t *testing.T) {
	dir := projectWith(t, map[string]string{
		"inventory.yaml":         "hosts: []\n",
		"collections/program":    "#!/bin/sh\n",
		".pleiades/master.key":   "secret-sentinel",
		"configs/keep/README.md": "x",
	})
	guards := []string{filepath.Join(dir, "inventory.yaml"), filepath.Join(dir, ".inventory-state.generated.yaml"), "collections"}
	c := &runbookContext{projectDir: dir, projectGuards: guards}
	if err := os.Link(filepath.Join(dir, "inventory.yaml"), filepath.Join(dir, "configs", "alias.yaml")); err != nil {
		t.Fatalf("a hard link: %v", err)
	}
	for _, name := range []string{
		"inventory.yaml",
		"Inventory.YAML",
		".inventory-state.generated.yaml",
		"collections/.pleiades-approvals.json",
		"collections/program",
		"COLLECTIONS/new/x",
		"configs/alias.yaml",
	} {
		for _, write := range []bool{false, true} {
			if _, err := c.WriteProjectFile(name, []byte("hosts: [evil]\n"), 0o600, write); err == nil || !strings.Contains(err.Error(), "which a run never writes") {
				t.Errorf("WriteProjectFile(%q, write %v) = %v; want it refused as guarded", name, write, err)
			}
		}
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "inventory.yaml")); string(got) != "hosts: []\n" {
		t.Errorf("the inventory now holds %q", got)
	}
	for _, name := range []string{".inventory-state.generated.yaml", "collections/.pleiades-approvals.json"} {
		if _, err := os.Lstat(filepath.Join(dir, name)); err == nil {
			t.Errorf("%s was written", name)
		}
	}
	if changed, err := c.WriteProjectFile("configs/r3.conf", []byte("x"), 0o600, true); err != nil || !changed {
		t.Errorf("an ordinary project file: %v, %v", changed, err)
	}
}

// TestWriteProjectFile_AGuardHoldingTheProjectRefusesEveryWrite: a guarded
// directory that is the project itself (a collections directory set to it)
// holds every path, so nothing is written.
func TestWriteProjectFile_AGuardHoldingTheProjectRefusesEveryWrite(t *testing.T) {
	dir := projectWith(t, nil)
	c := &runbookContext{projectDir: dir, projectGuards: []string{dir}}
	if _, err := c.WriteProjectFile("configs/x.conf", []byte("x"), 0o600, true); err == nil || !strings.Contains(err.Error(), "the project directory is inside") {
		t.Errorf("a write in a guarded project: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "configs")); err == nil {
		t.Error("a directory was created")
	}
}

// TestRefuseGuarded_MatchesByIdentity: a guard carrying no name at all is
// still found on any path through it, by the directory itself; this is
// what refuses a name the system resolves to .pleiades some other way (a
// Windows 8.3 short name, a trailing dot or space), which a test on this
// file system cannot spell. A path elsewhere passes, and so does one that
// does not exist yet.
func TestRefuseGuarded_MatchesByIdentity(t *testing.T) {
	dir := projectWith(t, map[string]string{".pleiades/credentials.yaml": "secret-sentinel", "files/ok": "ok"})
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	secrets, err := secretsGuard(root)
	if err != nil || secrets.info == nil {
		t.Fatalf("secretsGuard = %+v, %v", secrets, err)
	}
	nameless := secrets
	nameless.rel = ""
	for _, name := range []string{".pleiades", ".pleiades/credentials.yaml", ".pleiades/new/x"} {
		if err := refuseGuarded(root, name, name, []guardedPath{nameless}); err == nil || !strings.Contains(err.Error(), "holds its credentials") {
			t.Errorf("refuseGuarded(%q) by identity alone = %v", name, err)
		}
	}
	for _, name := range []string{"files/ok", "files/new/x"} {
		if err := refuseGuarded(root, name, name, []guardedPath{nameless}); err != nil {
			t.Errorf("refuseGuarded(%q) = %v; want it allowed", name, err)
		}
	}

	// With no .pleiades yet, the name alone guards it.
	bare := projectWith(t, nil)
	broot, err := os.OpenRoot(bare)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = broot.Close() }()
	missing, err := secretsGuard(broot)
	if err != nil || missing.info != nil {
		t.Fatalf("secretsGuard on a project with no .pleiades = %+v, %v", missing, err)
	}
	if err := refuseGuarded(broot, ".Pleiades/x", ".Pleiades/x", []guardedPath{missing}); err == nil {
		t.Error("a missing .pleiades was reachable by another letter case")
	}
}

// TestProjectGuards_AProjectThatCannotBeExaminedIsAnError: where the
// project's .pleiades cannot be looked up (a project directory the account
// may list but not search), a read and a write are errors naming it, never
// a read or write that skipped the check.
func TestProjectGuards_AProjectThatCannotBeExaminedIsAnError(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("Unix modes, as an account they bind")
	}
	dir := projectWith(t, map[string]string{"files/ok": "ok"})
	if err := os.Chmod(dir, 0o400); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	c := &runbookContext{projectDir: dir}
	if got, err := c.ReadProjectFile("files/ok", 16); err == nil || !strings.Contains(err.Error(), ".pleiades") {
		t.Errorf("ReadProjectFile = %q, %v; want an error naming .pleiades", got, err)
	}
	if _, err := c.WriteProjectFile("files/new", []byte("x"), 0o600, true); err == nil || !strings.Contains(err.Error(), ".pleiades") {
		t.Errorf("WriteProjectFile = %v; want an error naming .pleiades", err)
	}
}
