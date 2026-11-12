// Package externalscaffold_test: the same generated source builds, by the
// README's own build line, to the one digest the approval list pins.
package externalscaffold_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/externalscaffold"
	"github.com/Subject-Void-LLC/the-pleiades/internal/loader"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport/need"
)

// TestGenerate_TheSameSourceBuildsToOneDigest is Phase 42's reproducible
// program build: one source, scaffolded twice into two different
// directories and built by the README's own offline commands, gives one
// program digest, the one loader.ProgramDigest computes and
// `pleiades collection approve` pins. The second build is made as unlike
// the first as one machine allows: a directory of another name at another
// depth, inside a git repository where the first is in none, no C
// compiler, and its own GOPATH, module cache and build cache, the module
// cache filled through a file proxy over this machine's so the build stays
// offline. Its fresh build cache means the standard library and every
// dependency are compiled again rather than reused from the first build.
// Each of those differences is one a setting on the build line removes, so
// dropping any one setting from the README fails this test.
//
// The control builds the same two directories with a plain `go build`. Its
// two digests must differ, or the two builds were not different enough to
// prove anything and the equal digests above would be the setup's doing
// rather than the build line's.
func TestGenerate_TheSameSourceBuildsToOneDigest(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the reproducible build gate in -short mode: it builds the standard library from an empty cache")
	}
	need.POSIXShell(t)

	cfg := externalscaffold.Config{Name: fmt.Sprintf("test.repro%d.probe", os.Getpid())}

	dirA := mkdirAll(t, filepath.Join(t.TempDir(), "first"))
	binA := buildIn(t, cfg, dirA, nil)

	other := t.TempDir()
	// The second program sits inside a git repository with a commit, as a
	// clone does, where the first sits in none, as an unpacked archive
	// does: without -buildvcs=false the second binary would carry the
	// repository's revision and state.
	repo := mkdirAll(t, filepath.Join(other, "somewhere"))
	gitCommit(t, repo)
	dirB := mkdirAll(t, filepath.Join(repo, "else", "second"))
	envB := []string{
		// A machine with no C compiler, where the go command's default is
		// cgo off, which is what this says. Where the first build has a
		// compiler, without CGO_ENABLED=0 on the build line it links
		// through it and the second does not.
		"CGO_ENABLED=0",
		"GOPATH=" + filepath.Join(other, "gopath"),
		"GOMODCACHE=" + filepath.Join(other, "modcache"),
		"GOCACHE=" + filepath.Join(other, "gocache"),
		// The new module cache is filled from this machine's own, read as
		// a proxy, so the second build needs no network either.
		"GOPROXY=file://" + filepath.Join(goEnv(t, "GOMODCACHE"), "cache", "download"),
		// The checksums were verified when this machine's cache was
		// filled; this cache is a copy of it.
		"GOSUMDB=off",
		// The module cache is read-only by default, which would leave
		// t.TempDir unable to remove it.
		"GOFLAGS=-mod=mod -modcacherw",
	}
	binB := buildIn(t, cfg, dirB, envB)
	// The second build really used its own caches, rather than the
	// environment above being overridden somewhere on the way.
	for _, own := range []string{
		filepath.Join(other, "modcache", "golang.org", "x"),
		filepath.Join(other, "gocache", "README"),
	} {
		if _, err := os.Stat(own); err != nil {
			t.Fatalf("the second build left no %s, so it did not build with its own caches: %v", own, err)
		}
	}

	digestA := programDigest(t, binA)
	digestB := programDigest(t, binB)
	if digestA != digestB {
		t.Errorf("the same source built to two digests:\n  %s in %s\n  %s in %s\nthe README's build line is not reproducible", digestA, dirA, digestB, dirB)
	}
	t.Logf("both builds: %s", digestA)

	plainA := plainBuild(t, dirA, nil)
	plainB := plainBuild(t, dirB, envB)
	if programDigest(t, plainA) == programDigest(t, plainB) {
		t.Error("control: a plain go build gave one digest in both directories too, so these two builds differ in nothing a reproducible build must remove, and the equal digests above prove nothing")
	}
}

// TestReadme_BuildLinesAreReproducible holds every build line the README
// gives, online and offline, to the settings that make it reproducible,
// with the Go release The Pleiades itself is built with. The release gates
// build with the local Go (buildIn), so this is what keeps the pinned
// release in the line an author copies.
func TestReadme_BuildLinesAreReproducible(t *testing.T) {
	files, err := externalscaffold.Generate(externalscaffold.Config{Name: "acme.motd.read"})
	if err != nil {
		t.Fatal(err)
	}
	var readme string
	for _, f := range files {
		if f.Path == "README.md" {
			readme = string(f.Content)
		}
	}
	want := "CGO_ENABLED=0 GOTOOLCHAIN=" + moduleToolchain(t) + " go build -trimpath -buildvcs=false -o acme-motd-read ."
	var builds int
	for _, line := range strings.Split(readme, "\n") {
		if !strings.Contains(line, "go build") {
			continue
		}
		builds++
		if line != want {
			t.Errorf("the README builds with %q, want %q", line, want)
		}
	}
	if builds != 2 {
		t.Errorf("the README has %d go build lines, want 2 (online and offline)", builds)
	}
}

// TestDocs_BuildLinesNameTheModuleToolchain holds every GOTOOLCHAIN the
// user guide and the example program's README give to The Pleiades's own
// toolchain line, so a toolchain bump that forgets them fails here rather
// than leaving authors building with a release nobody else uses.
func TestDocs_BuildLinesNameTheModuleToolchain(t *testing.T) {
	want := "GOTOOLCHAIN=" + moduleToolchain(t)
	for _, rel := range []string{"docs/11-extending-pleiades.md", "examples/external_collection/README.md"} {
		body, err := os.ReadFile(filepath.Join(repoRoot(t), rel))
		if err != nil {
			t.Fatal(err)
		}
		var found int
		for _, field := range strings.Fields(string(body)) {
			field = strings.Trim(field, "`|")
			if !strings.HasPrefix(field, "GOTOOLCHAIN=") {
				continue
			}
			found++
			if field != want {
				t.Errorf("%s says %s, want %s", rel, field, want)
			}
		}
		if found == 0 {
			t.Errorf("%s names no GOTOOLCHAIN, so its build line is not the reproducible one", rel)
		}
	}
}

// mkdirAll creates dir and its parents, owner-only, so loader.ProgramDigest's
// directory checks accept it.
func mkdirAll(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

// programDigest is the digest the approval list would pin for bin, through
// the loader's own ProgramDigest, with its directory and file checks.
func programDigest(t *testing.T, bin string) string {
	t.Helper()
	digest, err := loader.ProgramDigest(filepath.Dir(bin), filepath.Base(bin))
	if err != nil {
		t.Fatalf("ProgramDigest(%s): %v", bin, err)
	}
	return digest
}

// plainBuild runs `go build` with no settings at all in dir, under the same
// environment buildIn gave that directory, and returns the binary.
func plainBuild(t *testing.T, dir string, extraEnv []string) string {
	t.Helper()
	bin := filepath.Join(dir, "plain")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = dir
	cmd.Env = append(offlineEnv(), extraEnv...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("plain go build in %s: %v\n%s", dir, err, out)
	}
	return bin
}

// gitCommit makes dir a git repository holding one empty commit, with no
// configuration from this machine's user or system, and skips the test
// where git is not installed.
func gitCommit(t *testing.T, dir string) {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("needs git: ", err)
	}
	for _, args := range [][]string{
		{"init", "-q"},
		{"commit", "-q", "--allow-empty", "-m", "a repository around the program"},
	} {
		cmd := exec.Command(git, args...) // #nosec G204 -- fixed arguments
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.invalid",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.invalid")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

// goEnv is one value of `go env`, as this machine's Go reports it.
func goEnv(t *testing.T, name string) string {
	t.Helper()
	out, err := exec.Command("go", "env", name).Output()
	if err != nil {
		t.Fatalf("go env %s: %v", name, err)
	}
	v := strings.TrimSpace(string(out))
	if v == "" {
		t.Fatalf("go env %s is empty", name)
	}
	return v
}
