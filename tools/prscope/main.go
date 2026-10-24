// Command prscope prints the packages a branch changes, one import path per
// line, for `make pr-check`: every package holding a file that differs from
// where the branch left its base, committed, staged, unstaged or untracked.
//
// A changed file belongs to the nearest directory at or above it that is a
// package, so a fixture under testdata, or a JSON file a package embeds
// from a subdirectory with no Go files of its own, names the package that
// reads it. A file no package sits above (the Makefile, go.mod, docs/,
// .github/) names nothing: pr-check's static checks and its always-run
// guards read those, and CI's own jobs test everything else.
//
// Some changes also name packages they do not touch, each said on standard
// error. Every package that imports a changed one directly, its tests
// included, runs too, since a consumer's assertion about the old behavior
// otherwise breaks only in CI (FAILURE_PATTERNS 631); importers of
// importers do not, since a change to a shared package reaches most of the
// module that way, and CI's own jobs test it all. And a change that can
// alter what validation decides about an existing runbook adds the
// packages that validate and run those runbooks, cmd/pleiades first
// (census.go).
//
// A rename names both its old path and its new one, so a file moved out of
// a package still selects that package, and paths are read NUL-separated,
// so one holding a space or a character git would quote still matches.
//
// Usage:
//
//	go run ./tools/prscope [-base origin/main]
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

func main() {
	os.Exit(realMain(".", os.Args[1:], os.Stdout, os.Stderr))
}

// realMain is the command run in the module at root, returning its exit
// status: 0 with the packages printed, 1 when they cannot be found, 2 for
// a flag it does not know.
func realMain(root string, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("prscope", flag.ContinueOnError)
	flags.SetOutput(stderr)
	base := flags.String("base", "origin/main", "the ref the branch will merge into; files are compared with its merge base")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	pkgs, importers, trigger, err := changedPackages(root, *base)
	if err != nil {
		fmt.Fprintln(stderr, "prscope:", err)
		return 1
	}
	if len(importers) > 0 {
		fmt.Fprintf(stderr, "prscope: %d package(s) import a changed one, so they run too (FAILURE_PATTERNS 631): %s\n", len(importers), strings.Join(importers, ", "))
	}
	if trigger != "" {
		fmt.Fprintf(stderr, "prscope: %s changes what validation decides, so %s run too (FAILURE_PATTERNS 559)\n", trigger, strings.Join(censusPackages, ", "))
	}
	for _, p := range pkgs {
		fmt.Fprintln(stdout, p)
	}
	return 0
}

// changedPackages is every package of the module at root holding a file
// that differs from the merge base of HEAD and base, every package that
// imports one of those directly, and the census packages when a changed
// file triggers them, sorted. The second result is the importers it added
// that no file of theirs changed, sorted, and the third names the file
// that triggered the census.
func changedPackages(root, base string) ([]string, []string, string, error) {
	mergeBase, err := run(root, "git", "merge-base", "HEAD", base)
	if err != nil {
		return nil, nil, "", fmt.Errorf("finding where HEAD left %s: %w", base, err)
	}
	// --no-renames prints a rename's old path beside its new one, and -z
	// prints each path raw, NUL-terminated, where git would otherwise quote
	// one holding a space or a non-ASCII character.
	tracked, err := run(root, "git", "diff", "--name-only", "--no-renames", "-z", strings.TrimSpace(mergeBase))
	if err != nil {
		return nil, nil, "", err
	}
	untracked, err := run(root, "git", "ls-files", "-z", "--others", "--exclude-standard")
	if err != nil {
		return nil, nil, "", err
	}
	packages, imports, err := modulePackages(root)
	if err != nil {
		return nil, nil, "", err
	}
	var files []string
	for _, file := range strings.Split(tracked+"\x00"+untracked, "\x00") {
		if file != "" {
			files = append(files, file)
		}
	}
	found := map[string]bool{}
	for _, file := range files {
		for dir := path.Dir(file); ; dir = path.Dir(dir) {
			if ip, ok := packages[dir]; ok {
				found[ip] = true
				break
			}
			if dir == "." || dir == "/" {
				break
			}
		}
	}
	importers := addImporters(found, imports)
	trigger, err := addCensus(files, packages, found)
	if err != nil {
		return nil, nil, "", err
	}
	out := make([]string, 0, len(found))
	for ip := range found {
		out = append(out, ip)
	}
	sort.Strings(out)
	return out, importers, trigger, nil
}

// addImporters adds to found every package that imports one already in it
// directly, in its own code or its tests (imports maps each package to
// those it imports), and returns the ones it added, sorted. Only the
// packages found before it ran are followed, so an importer's importers
// are not added.
func addImporters(found map[string]bool, imports map[string][]string) []string {
	var added []string
	for pkg, deps := range imports {
		if found[pkg] {
			continue
		}
		for _, dep := range deps {
			if dep != pkg && found[dep] {
				added = append(added, pkg)
				break
			}
		}
	}
	for _, pkg := range added {
		found[pkg] = true
	}
	sort.Strings(added)
	return added
}

// modulePackages maps each package directory of the module at root,
// slash-separated and relative to root, to its import path, and each
// package's import path to what it imports, its tests' imports included,
// under the integration tag CI's test tiers build with.
func modulePackages(root string) (map[string]string, map[string][]string, error) {
	out, err := run(root, "go", "list", "-tags", "integration", "-f",
		"{{.Dir}}\t{{.ImportPath}}\t{{join .Imports \" \"}} {{join .TestImports \" \"}} {{join .XTestImports \" \"}}", "./...")
	if err != nil {
		return nil, nil, err
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, nil, err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	packages := map[string]string{}
	imports := map[string][]string{}
	for _, line := range strings.Split(out, "\n") {
		// A package with no imports ends its line in a tab and spaces, which
		// trimming would take with it, so only the third field is optional.
		fields := strings.SplitN(line, "\t", 3)
		if len(fields) < 2 {
			continue
		}
		dir, ip := fields[0], fields[1]
		if len(fields) == 3 {
			imports[ip] = strings.Fields(fields[2])
		}
		if resolved, err := filepath.EvalSymlinks(dir); err == nil {
			dir = resolved
		}
		rel, err := filepath.Rel(abs, dir)
		if err != nil || strings.HasPrefix(rel, "..") {
			continue
		}
		packages[filepath.ToSlash(rel)] = ip
	}
	return packages, imports, nil
}

// run runs name with args in dir and returns its standard output; a
// failure carries its standard error.
func run(dir, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...) // #nosec G204 -- fixed programs (git, go) with arguments this command builds
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return "", fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
		}
		return "", fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return string(out), nil
}
