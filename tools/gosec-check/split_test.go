// This file tests how gosec-check finds the packages gosec would load in
// pieces: the length go/packages counts, the directories gosec's walk finds,
// the exact pattern that leaves one out, and, against this checkout itself,
// that every package fits through the short path.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// writeModule writes a module at root holding package big, with n files whose
// first uses a type only its last declares and which holds a finding gosec
// reports (a command run from its arguments), package small, with one file
// and a finding of its own (a file read from its arguments), and a package of n files under .claude that gosec-check excludes.
// It returns the paths of the files gosec scans.
func writeModule(t *testing.T, root string, n int) []string {
	t.Helper()
	write := func(rel, body string) string {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	write("go.mod", "module example.com/deep\n\ngo 1.22\n")
	var scanned []string
	for i := 0; i < n; i++ {
		body := fmt.Sprintf("package big\n\n// T%03d is one of many types.\ntype T%03d struct{}\n", i, i)
		switch i {
		case 0:
			body += "\n// first can only be type-checked beside the last file.\nvar first Template\n"
		case n - 1:
			body += "\n// Template is declared in the last file only.\ntype Template struct{}\n" +
				"\n// Run runs the program its first argument names.\nfunc Run() error { return exec.Command(os.Args[1]).Run() }\n"
			body = strings.Replace(body, "package big\n", "package big\n\nimport (\n\t\"os\"\n\t\"os/exec\"\n)\n", 1)
		}
		scanned = append(scanned, write(fmt.Sprintf("big/a_file_named_at_some_length_%03d.go", i), body))
	}
	scanned = append(scanned, write("small/small.go",
		"package small\n\nimport \"os\"\n\n// Read reads the file its first argument names.\nfunc Read() ([]byte, error) { return os.ReadFile(os.Args[1]) }\n"))
	for i := 0; i < n; i++ {
		write(fmt.Sprintf(".claude/worktrees/other/big/a_file_named_at_some_length_%03d.go", i), "package big\n")
	}
	return scanned
}

// deepDir returns a directory under t.TempDir() whose path is longer than
// 250 characters, which no checkout of this module sits at, so a package of
// a few dozen files is over go/packages' limit from it.
func deepDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, c := range "defg" {
		dir = filepath.Join(dir, strings.Repeat(string(c), 60))
	}
	return dir
}

// TestOverLimit_SplitsWhereGoPackagesDoes: go/packages adds each pattern and a
// separator to a running total and starts a new `go list` call once the total
// passes 16,383, so a list totalling exactly that loads whole and one more
// character splits it.
func TestOverLimit_SplitsWhereGoPackagesDoes(t *testing.T) {
	// 127 patterns of 128 characters total 127 * 129 = 16,383.
	patterns := make([]string, 127)
	for i := range patterns {
		patterns[i] = strings.Repeat("x", 128)
	}
	if got := listLength(patterns); got != 16383 {
		t.Fatalf("listLength = %d, want 16383", got)
	}
	if overLimit(patterns) {
		t.Error("a list of exactly 16,383 characters is over the limit, want it loaded whole")
	}
	patterns[0] += "x"
	if !overLimit(patterns) {
		t.Error("a list of 16,384 characters is within the limit, want it split")
	}
}

// TestLongPackages_FindsAPackageOnlyFromADeepPath: from a deep directory, the
// package of 60 files is found over the limit, with its length and file count,
// and the one-file package and the excluded copy under .claude are not; from a
// short directory nothing is.
func TestLongPackages_FindsAPackageOnlyFromADeepPath(t *testing.T) {
	deep := deepDir(t)
	scanned := writeModule(t, deep, 60)
	long, err := longPackages(deep, excludeDirs)
	if err != nil {
		t.Fatal(err)
	}
	if len(long) != 1 || long[0].dir != "big" || long[0].files != 60 {
		t.Fatalf("long packages = %+v, want only big with 60 files", long)
	}
	if want := listLength(scanned[:60]); long[0].length != want {
		t.Errorf("big's length = %d, want %d, the length of its files' absolute paths", long[0].length, want)
	}

	short := t.TempDir()
	writeModule(t, short, 60)
	if long, err := longPackages(short, excludeDirs); err != nil || len(long) != 0 {
		t.Errorf("from %s: long packages = %+v, %v; want none", short, long, err)
	}
}

// TestPackageDirs_IsGosecsWalk: every directory holding a .go file is found,
// the root as ".", and the excluded ones, gosec's own defaults (vendor, .git)
// among them, are not.
func TestPackageDirs_IsGosecsWalk(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{"main.go", "a/a.go", "a/b/b.go", "vendor/v/v.go", ".git/hooks/h.go", ".claude/w/x.go", ".IGNORE/y.go", "notes/readme.md"} {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("package x\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	dirs, err := packageDirs(root, append(append([]string(nil), gosecDefaultExcludes...), excludeDirs...))
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(dirs)
	got := strings.Join(dirs, " ")
	if want := ". a " + filepath.Join("a", "b"); got != want {
		t.Errorf("package dirs = %q, want %q", got, want)
	}
}

// TestExactDirPattern_MatchesThatDirectoryOnly: compiled as gosec compiles an
// -exclude-dir value, the pattern for internal/ent matches it and not its
// subdirectories, a longer name, or a name whose dot is any other character,
// and a name it cannot match exactly is refused.
func TestExactDirPattern_MatchesThatDirectoryOnly(t *testing.T) {
	pattern, err := exactDirPattern(filepath.Join("internal", "ent.v2"))
	if err != nil {
		t.Fatal(err)
	}
	re, err := gosecDirRegexp(pattern)
	if err != nil {
		t.Fatal(err)
	}
	if !re.MatchString("internal/ent.v2") {
		t.Errorf("%s does not match internal/ent.v2", re)
	}
	for _, other := range []string{"internal/ent.v2/schema", "internal/ent.v22", "xinternal/ent.v2", "internal/entXv2", "a/internal/ent.v2"} {
		if re.MatchString(other) {
			t.Errorf("%s matches %s, want only internal/ent.v2", re, other)
		}
	}
	if _, err := exactDirPattern("a+b"); err == nil {
		t.Error("a directory named a+b was given a pattern, want it refused")
	}
}

// TestThisCheckout_FitsThroughTheShortPath is the guard: from this checkout,
// every package gosec would load in pieces is one gosec-check finds (so the
// workaround is on for it), internal/ent first among them when it is over,
// and every package fits through the short path the scan would use. It
// fails as soon as a package grows past what the short path can rescue,
// before gosec itself fails anywhere.
func TestThisCheckout_FitsThroughTheShortPath(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	ent := gosecFiles(root, filepath.Join("internal", "ent"))
	if len(ent) == 0 {
		t.Fatalf("no files found in %s/internal/ent", root)
	}
	long, err := longPackages(root, excludeDirs)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, p := range long {
		found[p.dir] = true
	}
	if overLimit(ent) && !found[filepath.Join("internal", "ent")] {
		t.Errorf("internal/ent's list is %d characters from %s, over %d, and gosec-check would not rescue it", listLength(ent), root, goPackagesArgMax)
	}

	base, err := defaultShortBase()
	if err != nil {
		t.Fatal(err)
	}
	short := shortLinkPath(base, root)
	shortEnt := gosecFiles(root, filepath.Join("internal", "ent"))
	for i, f := range shortEnt {
		shortEnt[i] = filepath.Join(short, strings.TrimPrefix(f, root))
	}
	t.Logf("internal/ent: %d files, %d characters from %s and %d from %s, of %d", len(ent), listLength(ent), root, listLength(shortEnt), short, goPackagesArgMax)
	if overLimit(shortEnt) {
		t.Errorf("internal/ent's list is %d characters even from the short path %s, over %d: gosec-check can no longer rescue it", listLength(shortEnt), short, goPackagesArgMax)
	}
	if err := fitsFrom(short, root, long); err != nil {
		t.Error(err)
	}
}
