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
// One change also names a package it does not touch: a change that can
// alter what validation decides about an existing runbook adds
// cmd/pleiades, which validates every one of them (census.go), and says
// so on standard error.
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
	pkgs, trigger, err := changedPackages(root, *base)
	if err != nil {
		fmt.Fprintln(stderr, "prscope:", err)
		return 1
	}
	if trigger != "" {
		fmt.Fprintf(stderr, "prscope: %s changes what validation decides, so %s runs too (FAILURE_PATTERNS 559)\n", trigger, censusPackage)
	}
	for _, p := range pkgs {
		fmt.Fprintln(stdout, p)
	}
	return 0
}

// changedPackages is every package of the module at root holding a file
// that differs from the merge base of HEAD and base, sorted, plus the
// census package when a changed file triggers it; the second result names
// that file.
func changedPackages(root, base string) ([]string, string, error) {
	mergeBase, err := run(root, "git", "merge-base", "HEAD", base)
	if err != nil {
		return nil, "", fmt.Errorf("finding where HEAD left %s: %w", base, err)
	}
	tracked, err := run(root, "git", "diff", "--name-only", strings.TrimSpace(mergeBase))
	if err != nil {
		return nil, "", err
	}
	untracked, err := run(root, "git", "ls-files", "--others", "--exclude-standard")
	if err != nil {
		return nil, "", err
	}
	packages, err := modulePackages(root)
	if err != nil {
		return nil, "", err
	}
	files := strings.Fields(tracked + "\n" + untracked)
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
	trigger, err := addCensus(files, packages, found)
	if err != nil {
		return nil, "", err
	}
	out := make([]string, 0, len(found))
	for ip := range found {
		out = append(out, ip)
	}
	sort.Strings(out)
	return out, trigger, nil
}

// modulePackages maps each package directory of the module at root,
// slash-separated and relative to root, to its import path, under the
// integration tag CI's test tiers build with.
func modulePackages(root string) (map[string]string, error) {
	out, err := run(root, "go", "list", "-tags", "integration", "-f", "{{.Dir}}\t{{.ImportPath}}", "./...")
	if err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	packages := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		dir, ip, ok := strings.Cut(line, "\t")
		if !ok {
			continue
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
	return packages, nil
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
