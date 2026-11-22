// This file scans the module: one gosec run over "./..." from the checkout,
// as always, unless some package's file list from the checkout is too long
// for go/packages to load whole (split.go). Then the run from the checkout
// leaves those packages out, a second run scans just them from a short path
// to the same checkout (shortpath.go), and the two reports are joined, with
// every path the second names put back under the checkout. Each package is
// scanned exactly once, by the same gosec with the same flags.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// scanner is how gosec-check runs gosec.
type scanner struct {
	// gosec is the gosec program, by name on PATH or by path.
	gosec string
	// shortBase is the directory the short path's link is made in.
	shortBase string
	// stop, once closed, stops the scan.
	stop <-chan struct{}
	// deadline is when the scan is stopped; zero never.
	deadline time.Time
	// log receives a line naming each package scanned from the short path,
	// and any warning about stopping gosec.
	log io.Writer
}

// excludeDirs are the -exclude-dir values every gosec run gets (exclude.go).
var excludeDirs = []string{excludeAgentDirs, excludeIgnoredDirs}

// gosecArgs returns gosec's arguments: every value in excludes as an
// -exclude-dir, then paths.
func gosecArgs(excludes []string, paths ...string) []string {
	args := []string{"-exclude-generated"}
	for _, value := range excludes {
		args = append(args, "-exclude-dir="+value)
	}
	args = append(args, "-fmt=json")
	return append(args, paths...)
}

// scan runs gosec over the module at root, the working directory, and returns
// the joined report.
func (s scanner) scan(root string) (*gosecReport, error) {
	long, err := longPackages(root, excludeDirs)
	if err != nil {
		return nil, err
	}
	if len(long) == 0 {
		return s.run(root, gosecArgs(excludeDirs, "./..."))
	}

	// Check the short path rescues every long package before making it.
	if err := fitsFrom(shortLinkPath(s.shortBase, root), root, long); err != nil {
		return nil, err
	}
	short, err := stableShortRoot(s.shortBase, root)
	if err != nil {
		return nil, fmt.Errorf("%s, and %w; scan from a checkout with a shorter path", describeLong(root, long), err)
	}

	// The run from the checkout leaves out exactly the long packages.
	excludes := append([]string(nil), excludeDirs...)
	var paths []string
	for _, p := range long {
		pattern, err := exactDirPattern(p.dir)
		if err != nil {
			return nil, err
		}
		excludes = append(excludes, pattern)
		paths = append(paths, "./"+filepath.ToSlash(p.dir))
		fmt.Fprintf(s.log, "gosec-check: %s has %d files, a list of %d characters from this checkout, over go/packages' %d; scanning it from %s\n",
			p.dir, p.files, p.length, goPackagesArgMax, short)
	}
	rest, err := s.run(root, gosecArgs(excludes, "./..."))
	if err != nil {
		return nil, err
	}
	rescued, err := s.run(short, gosecArgs(excludeDirs, paths...))
	if err != nil {
		return nil, err
	}
	rescued.rebase(short, root)
	rest.join(rescued)
	return rest, nil
}

// fitsFrom returns an error naming each long package whose file list from
// root would still be too long from short, which gosec-check cannot rescue,
// or nil. Every path in a list from short is the same path with root replaced
// by short, so the length is reckoned without short existing.
func fitsFrom(short, root string, long []longPackage) error {
	var still []string
	for _, p := range long {
		if length := p.length + p.files*(len(short)-len(root)); length > goPackagesArgMax {
			still = append(still, fmt.Sprintf("%s (%d characters from %s)", p.dir, length, short))
		}
	}
	if len(still) == 0 {
		return nil
	}
	return fmt.Errorf("go/packages loads a file list of at most %d characters whole, and gosec lists each file by its absolute path; "+
		"even from the short path %s these packages are over it: %s. Scan from a checkout with a shorter path, "+
		"or split the package; a gosec that loads a package by its directory would end this limit", goPackagesArgMax, short, strings.Join(still, ", "))
}

// describeLong names the long packages and why they need a short path, for an
// error that stops the scan before it starts.
func describeLong(root string, long []longPackage) string {
	names := make([]string, 0, len(long))
	for _, p := range long {
		names = append(names, fmt.Sprintf("%s (%d characters)", p.dir, p.length))
	}
	return fmt.Sprintf("from %s, gosec would load %s in pieces, since go/packages splits a file list over %d characters",
		root, strings.Join(names, ", "), goPackagesArgMax)
}

// run runs gosec with args from dir and decodes its report.
func (s scanner) run(dir string, args []string) (*gosecReport, error) {
	cmd := exec.Command(s.gosec, args...) // #nosec G204 -- the scanner's own fixed arguments
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := runGroup(cmd, s.stop, s.deadline, s.log)
	if errors.Is(runErr, errStopped) || errors.Is(runErr, errTimeLimit) {
		return nil, runErr
	}

	report, err := decodeReport(stdout.Bytes())
	if err != nil {
		// gosec exits non-zero whenever it finds anything at all, so a
		// non-zero exit alone is not an error; a body that will not parse
		// as its own JSON report is the real failure.
		return nil, fmt.Errorf("gosec did not produce valid JSON output (exit error: %v): %w\nstderr:\n%s", runErr, err, stderr.String())
	}
	return report, nil
}

// rebase rewrites every path in r under from to the same path under to.
func (r *gosecReport) rebase(from, to string) {
	move := func(path string) string {
		rel, err := filepath.Rel(from, path)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return path
		}
		return filepath.Join(to, rel)
	}
	for i := range r.Issues {
		r.Issues[i].File = move(r.Issues[i].File)
	}
	if len(r.GolangErrors) > 0 {
		moved := make(map[string][]gosecError, len(r.GolangErrors))
		for file, errs := range r.GolangErrors {
			if file != gosecGlobalKey {
				file = move(file)
			}
			moved[file] = append(moved[file], errs...)
		}
		r.GolangErrors = moved
	}
}

// join adds other's findings, errors and counts to r.
func (r *gosecReport) join(other *gosecReport) {
	r.Issues = append(r.Issues, other.Issues...)
	if len(other.GolangErrors) > 0 && r.GolangErrors == nil {
		r.GolangErrors = map[string][]gosecError{}
	}
	for file, errs := range other.GolangErrors {
		r.GolangErrors[file] = append(r.GolangErrors[file], errs...)
	}
	r.Stats.Files += other.Stats.Files
	r.Stats.Lines += other.Stats.Lines
	r.Stats.Nosec += other.Stats.Nosec
	r.Stats.Found += other.Stats.Found
}
