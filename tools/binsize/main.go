// Command binsize builds the pleiades binary for each platform it ships on,
// records each one's size in a local ledger, and prints how each changed
// since the last commit it recorded, so the binary's growth is tracked run
// after run. make pr-check runs it as its sizes step; a platform that does
// not build fails it, which is also a compile check for platforms no other
// step builds.
//
// Every build uses a release build's flags (buildFlags, with cgo off, as a
// static release binary is built), and the flags are recorded with each size, so two sizes
// are compared only when they were built the same way. The ledger is
// gitignored (.IGNORE/ci), since make pr-check writing a tracked file would
// dirty the tree its receipt needs clean.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Schema is an entry's major version.
const Schema = 1

// buildFlags are the go build flags every size is measured with: a release
// build's, stripped of the symbol table and DWARF (-s -w), which a panic's
// stack trace does not need (it reads Go's own line tables, which stay), so
// the size tracked is the size a user downloads. Stripping took a full
// linux/amd64 pleiades from 133.8 MiB to 98.6 MiB on 2026-10-24.
var buildFlags = []string{"-trimpath", "-ldflags=-s -w"}

// Flags is buildFlags as one string, recorded with each size, so two sizes
// are compared only when they were built the same way.
var Flags = strings.Join(buildFlags, " ")

// DefaultPlatforms are the GOOS/GOARCH pairs measured when -platforms is not
// given: the systems The Pleiades runs on, on both architectures a user is
// likely to run it on.
const DefaultPlatforms = "linux/amd64,linux/arm64,darwin/amd64,darwin/arm64,windows/amd64,freebsd/amd64"

// Entry is one platform's size at one commit, one JSON line of the ledger.
type Entry struct {
	// Schema is the entry's major version (Schema).
	Schema int `json:"schema"`
	// Commit is the full SHA the binary was built from.
	Commit string `json:"commit"`
	// Dirty says the working tree had changes the commit does not hold.
	Dirty bool `json:"dirty,omitempty"`
	// RecordedAt is when the size was measured.
	RecordedAt time.Time `json:"recorded_at"`
	// Package is the main package built.
	Package string `json:"package"`
	// Platform is GOOS/GOARCH.
	Platform string `json:"platform"`
	// GoVersion is the toolchain that built it.
	GoVersion string `json:"go_version"`
	// Flags are the build flags it was built with.
	Flags string `json:"flags"`
	// Bytes is the binary's size.
	Bytes int64 `json:"bytes"`
}

// comparable reports whether e and o were built the same way, of the same
// package for the same platform, so their sizes can be compared.
func (e Entry) comparable(o Entry) bool {
	return e.Package == o.Package && e.Platform == o.Platform && e.GoVersion == o.GoVersion && e.Flags == o.Flags
}

// main runs the command and exits with its status.
func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run is the command: 0 when every platform built, 1 when one did not or
// the ledger could not be read or written, 2 for a usage error.
func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("binsize", flag.ContinueOnError)
	fs.SetOutput(stderr)
	platforms := fs.String("platforms", DefaultPlatforms, "comma-separated GOOS/GOARCH pairs to build for")
	pkg := fs.String("pkg", "./cmd/pleiades", "the main package to build")
	ledger := fs.String("ledger", ".IGNORE/ci/binsize.jsonl", "the ledger each size is appended to")
	asJSON := fs.Bool("json", false, "print this run's entries as JSON instead of a table")
	at := fs.String("commit", "", "the commit to record the sizes under (default: HEAD, read with git, and whether the tree is dirty)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(stderr, "binsize: unexpected argument %q\n", fs.Arg(0))
		return 2
	}
	list, err := parsePlatforms(*platforms)
	if err != nil {
		fmt.Fprintf(stderr, "binsize: %v\n", err)
		return 2
	}
	commit, dirty := *at, false
	if commit == "" {
		if commit, dirty, err = headCommit(); err != nil {
			fmt.Fprintf(stderr, "binsize: %v\n", err)
			return 1
		}
	}
	have, err := readLedger(*ledger)
	if err != nil {
		fmt.Fprintf(stderr, "binsize: %v\n", err)
		return 1
	}
	dir, err := os.MkdirTemp("", "binsize-")
	if err != nil {
		fmt.Fprintf(stderr, "binsize: %v\n", err)
		return 1
	}
	defer func() { _ = os.RemoveAll(dir) }()
	entries, failed := measure(*pkg, list, dir, commit, dirty, stderr)
	if err := appendLedger(*ledger, entries); err != nil {
		fmt.Fprintf(stderr, "binsize: %v\n", err)
		failed = true
	}
	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(entries)
	} else {
		printTable(stdout, entries, have)
	}
	if failed {
		return 1
	}
	return 0
}

// measure builds pkg for each platform into dir, returning an entry for each
// that built and whether any did not, each failure said on stderr.
func measure(pkg string, platforms []string, dir, commit string, dirty bool, stderr io.Writer) ([]Entry, bool) {
	var entries []Entry
	failed := false
	for _, p := range platforms {
		size, err := build(pkg, p, dir)
		if err != nil {
			fmt.Fprintf(stderr, "binsize: %s does not build: %v\n", p, err)
			failed = true
			continue
		}
		entries = append(entries, Entry{Schema: Schema, Commit: commit, Dirty: dirty, RecordedAt: time.Now().UTC(),
			Package: pkg, Platform: p, GoVersion: runtime.Version(), Flags: Flags, Bytes: size})
	}
	return entries, failed
}

// parsePlatforms reads a comma-separated list of GOOS/GOARCH pairs, refusing
// one that is not a pair of plain names, since each reaches go build's
// environment.
func parsePlatforms(s string) ([]string, error) {
	var out []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		goos, goarch, ok := strings.Cut(p, "/")
		if !ok || !plainName(goos) || !plainName(goarch) {
			return nil, fmt.Errorf("%q is not a GOOS/GOARCH pair", p)
		}
		out = append(out, p)
	}
	return out, nil
}

// plainName reports whether s is a non-empty run of lowercase letters and
// digits, as every GOOS and GOARCH is.
func plainName(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

// headCommit returns HEAD's full SHA and whether the working tree has
// changes it does not hold.
func headCommit() (string, bool, error) {
	out, err := exec.Command("git", "rev-parse", "HEAD").Output()
	if err != nil {
		return "", false, fmt.Errorf("reading HEAD: %v", err)
	}
	status, err := exec.Command("git", "status", "--porcelain").Output()
	if err != nil {
		return "", false, fmt.Errorf("reading the working tree's status: %v", err)
	}
	return strings.TrimSpace(string(out)), len(bytes.TrimSpace(status)) > 0, nil
}

// build builds pkg for platform into dir with Flags and cgo off, returning
// the binary's size; the binary is removed once measured.
func build(pkg, platform, dir string) (int64, error) {
	goos, goarch, _ := strings.Cut(platform, "/")
	out := filepath.Join(dir, "bin-"+goos+"-"+goarch)
	defer func() { _ = os.Remove(out) }()
	args := append(append([]string{"build"}, buildFlags...), "-o", out, pkg)
	cmd := exec.Command("go", args...) // #nosec G204 -- fixed flags; the package is the caller's own flag
	cmd.Env = append(os.Environ(), "GOOS="+goos, "GOARCH="+goarch, "CGO_ENABLED=0")
	if msg, err := cmd.CombinedOutput(); err != nil {
		return 0, fmt.Errorf("%v: %s", err, strings.TrimSpace(string(msg)))
	}
	info, err := os.Stat(out)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

// readLedger reads every entry in the ledger at path; one that does not
// exist yet is empty, and a line of another major version, or not JSON, is
// refused with its line number.
func readLedger(path string) ([]Entry, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- the ledger the caller named
	if errors.Is(err, os.ErrNotExist) {
		// Windows reports a path under a file as not found too
		// (FAILURE_PATTERNS 628, 635): confirm the directory is one.
		if info, derr := os.Stat(filepath.Dir(path)); derr == nil && !info.IsDir() {
			return nil, fmt.Errorf("reading %s: %s is not a directory", path, filepath.Dir(path))
		}
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Entry
	for n, line := range bytes.Split(data, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var e Entry
		if err := json.Unmarshal(line, &e); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, n+1, err)
		}
		if e.Schema != Schema {
			return nil, fmt.Errorf("%s:%d: an entry of schema %d, where this reader knows %d", path, n+1, e.Schema, Schema)
		}
		out = append(out, e)
	}
	return out, nil
}

// appendLedger appends entries to the ledger at path, creating it and its
// directory private to this user.
func appendLedger(path string, entries []Entry) error {
	if len(entries) == 0 {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) // #nosec G304 -- the ledger the caller named
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	for _, e := range entries {
		if err := enc.Encode(e); err != nil {
			_ = f.Close()
			return err
		}
	}
	return f.Close()
}

// previous returns the latest entry in have built the same way as e at
// another commit, the size e is compared with.
func previous(e Entry, have []Entry) (Entry, bool) {
	for i := len(have) - 1; i >= 0; i-- {
		if have[i].Commit != e.Commit && have[i].comparable(e) {
			return have[i], true
		}
	}
	return Entry{}, false
}

// printTable prints each entry's size and its change since the last commit
// recorded the same way.
func printTable(w io.Writer, entries, have []Entry) {
	if len(entries) == 0 {
		return
	}
	fmt.Fprintf(w, "binsize: %s at %s, %s %s, cgo off\n", entries[0].Package, short(entries[0].Commit), entries[0].GoVersion, Flags)
	for _, e := range entries {
		line := fmt.Sprintf("  %-15s %8.2f MiB", e.Platform, mib(e.Bytes))
		if p, ok := previous(e, have); ok {
			d := e.Bytes - p.Bytes
			line += fmt.Sprintf("  %+8.2f MiB (%+.1f%%) since %s", mib(d), 100*float64(d)/float64(p.Bytes), short(p.Commit))
		} else {
			line += "  (first recorded)"
		}
		fmt.Fprintln(w, line)
	}
}

// mib is n bytes in mebibytes.
func mib(n int64) float64 { return float64(n) / (1 << 20) }

// short is a commit's first twelve characters.
func short(commit string) string {
	if len(commit) > 12 {
		return commit[:12]
	}
	return commit
}
