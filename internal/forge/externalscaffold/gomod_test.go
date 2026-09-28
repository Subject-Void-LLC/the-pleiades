// Package externalscaffold_test: tests of the go.mod a generated program gets.
package externalscaffold_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/externalscaffold"
)

// goModOf returns the go.mod Generate produced for cfg, and whether there
// was one.
func goModOf(t *testing.T, cfg externalscaffold.Config) (string, bool) {
	t.Helper()
	files, err := externalscaffold.Generate(cfg)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	for _, f := range files {
		if f.Path == "go.mod" {
			return string(f.Content), true
		}
	}
	return "", false
}

// TestGoMod_IsAModuleAndAGoLineAndNothingElse covers the generated go.mod:
// no require, whose right version only the go command can compute, and no
// replace, which nothing verifies. --no-go-mod (Config.NoGoMod) writes
// none, for a program that should join a module of the author's own.
func TestGoMod_IsAModuleAndAGoLineAndNothingElse(t *testing.T) {
	got, ok := goModOf(t, externalscaffold.Config{Name: "acme.motd.read"})
	if !ok {
		t.Fatal("Generate wrote no go.mod")
	}
	if want := "module acme-motd-read\n\ngo 1.26.0\n"; got != want {
		t.Errorf("go.mod = %q, want exactly %q", got, want)
	}
	if _, ok := goModOf(t, externalscaffold.Config{Name: "acme.motd.read", NoGoMod: true}); ok {
		t.Error("NoGoMod still wrote a go.mod")
	}
}

// TestGoMod_TheGoLineIsThisModules keeps the generated go line equal to
// the one in The Pleiades's own go.mod: a program importing The Pleiades needs at
// least that Go, and a hand-kept copy drifts.
func TestGoMod_TheGoLineIsThisModules(t *testing.T) {
	ours, err := os.ReadFile(filepath.Join(repoRoot(t), "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	var directive string
	for _, line := range strings.Split(string(ours), "\n") {
		if v, ok := strings.CutPrefix(line, "go "); ok {
			directive = strings.TrimSpace(v)
		}
	}
	got, _ := goModOf(t, externalscaffold.Config{Name: "acme.motd.read"})
	if !strings.Contains(got, "\ngo "+directive+"\n") || directive == "" {
		t.Errorf("the generated go.mod says %q; The Pleiades's own go line is %q", got, directive)
	}
}

// TestGoMod_KeepsAProgramOutOfAnEnclosingModule covers the reason the
// go.mod exists: a program scaffolded inside a checkout (this one, since
// forge's default directory is wherever the author stands) is a module of
// its own that the checkout's ./... never builds, where one with no go.mod
// joins the checkout and is compiled, vetted and committed with it. The
// no-go.mod control runs in a throwaway module rather than this one, so
// no other test ever sees a stranger's program in this tree.
func TestGoMod_KeepsAProgramOutOfAnEnclosingModule(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go list")
	}
	listed := func(moduleRoot, pattern, program string) bool {
		t.Helper()
		cmd := exec.Command("go", "list", "-e", pattern)
		cmd.Dir = moduleRoot
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("go list %s: %v\n%s", pattern, err, out)
		}
		return strings.Contains(string(out), program)
	}
	write := func(dir string, noGoMod bool) {
		t.Helper()
		files, err := externalscaffold.Generate(externalscaffold.Config{Name: "acme.motd.read", NoGoMod: noGoMod})
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			if err := os.WriteFile(filepath.Join(dir, f.Path), f.Content, 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}

	// Inside this checkout, beside this package and not under testdata/,
	// exactly where an author standing here would land.
	here, err := os.MkdirTemp(".", "scaffolded-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(here) })
	write(here, false)
	if listed(repoRoot(t), "./internal/forge/externalscaffold/...", filepath.Base(here)) {
		t.Errorf("a program with its own go.mod, scaffolded inside this checkout, is part of its ./...")
	}

	// The control, in a throwaway module: without the go.mod, it joins.
	enclosing := t.TempDir()
	if err := os.WriteFile(filepath.Join(enclosing, "go.mod"), []byte("module enclosing\n\ngo 1.26.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	joined := filepath.Join(enclosing, "joined")
	if err := os.Mkdir(joined, 0o700); err != nil {
		t.Fatal(err)
	}
	write(joined, true)
	if !listed(enclosing, "./...", "enclosing/joined") {
		t.Error("the control failed: a program with no go.mod did not join the module around it, so the assertion above proves nothing")
	}
}
