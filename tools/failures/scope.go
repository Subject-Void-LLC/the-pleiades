// What each pr-check run tested, beside the ledgers: the union of its
// passes' packages, one line per run. The pr-check ledger holds failures
// only, so a run that passed leaves no entry there, and that is exactly the
// run whose scope the report needs: a test that then failed only in
// Actions, in a package pr-check never ran at that commit, is a hole in
// pr-check's scope (FAILURE_PATTERNS 631: a consumer of a changed package),
// not this machine's luck. Its own file and its own versioned shape, so
// the ledgers' entries keep theirs.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/tools/internal/prreport"
)

// ScopeSchema is a scope line's major version, under EntrySchema's rules.
const ScopeSchema = 1

// ScopeKind is the kind every scope line carries.
const ScopeKind = "pr-check-scope"

// scopeName is the scope file's name in the ledgers' directory.
const scopeName = "pr-check-scope.jsonl"

// Scope is what one pr-check run tested on this machine.
type Scope struct {
	// Schema is the line's major version (ScopeSchema).
	Schema int `json:"schema"`
	// Kind is always pr-check-scope.
	Kind string `json:"kind"`
	// Run is the run's id, as its ledger entries name it.
	Run string `json:"run"`
	// Commit is the full SHA the run checked.
	Commit string `json:"commit"`
	// SeenAt is when the run began.
	SeenAt time.Time `json:"seen_at"`
	// RecordedAt is when this line was written.
	RecordedAt time.Time `json:"recorded_at"`
	// Packages are the import paths the run's passes tested, sorted.
	Packages []string `json:"packages"`
}

// scopeFile is the scope file under dir.
func scopeFile(dir string) string {
	return filepath.Join(dir, scopeName)
}

// scopeOf is what r's run tested: the union of its passes' packages. ok is
// false for a run whose scope says nothing reliable: one that tested no
// package, or one interrupted or still running, whose missing passes would
// make packages it meant to test look out of its scope.
func scopeOf(r prreport.Report, now time.Time) (Scope, bool) {
	if r.Verdict == prreport.VerdictInterrupted || r.Verdict == prreport.VerdictRunning || r.Commit == "" {
		return Scope{}, false
	}
	var packages []string
	for _, p := range r.Passes {
		for _, pkg := range p.Run.Packages {
			packages = addString(packages, pkg)
		}
	}
	if len(packages) == 0 {
		return Scope{}, false
	}
	seen := r.Generated
	if r.Started != nil {
		seen = *r.Started
	}
	return Scope{Schema: ScopeSchema, Kind: ScopeKind, Run: runID(seen, r.Commit), Commit: r.Commit, SeenAt: seen.UTC(),
		RecordedAt: now.UTC(), Packages: packages}, true
}

// readScopes reads every line of the scope file at path, refusing one this
// reader cannot read, with its line number. A file that does not exist yet
// holds no scope.
func readScopes(path string) ([]Scope, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- the scope file under the directory this tool was pointed at
	if errors.Is(err, os.ErrNotExist) {
		return nil, absentUnderDirectories(path)
	}
	if err != nil {
		return nil, err
	}
	var out []Scope
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for n := 1; scanner.Scan(); n++ {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var s Scope
		if err := json.Unmarshal(line, &s); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, n, err)
		}
		if s.Schema != ScopeSchema || s.Kind != ScopeKind {
			return nil, fmt.Errorf("%s:%d: a %q line of schema %d, where this reader knows %q of %d", path, n, s.Kind, s.Schema, ScopeKind, ScopeSchema)
		}
		out = append(out, s)
	}
	return out, scanner.Err()
}

// appendScope adds s to the scope file at path unless a line for its run is
// there already, and reports whether it added it.
func appendScope(path string, s Scope) (bool, error) {
	have, err := readScopes(path)
	if err != nil {
		return false, err
	}
	if slices.ContainsFunc(have, func(h Scope) bool { return h.Run == s.Run }) {
		return false, nil
	}
	return true, writeLines(path, append(have, s))
}

// scopesByCommit is, for each commit any pr-check run checked, the packages
// those runs tested between them, each by its path in the module, so a
// package an Actions log names either way compares equal.
func scopesByCommit(scopes []Scope) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	for _, s := range scopes {
		if out[s.Commit] == nil {
			out[s.Commit] = map[string]bool{}
		}
		for _, pkg := range s.Packages {
			out[s.Commit][short(pkg)] = true
		}
	}
	return out
}

// outsideScope is each commit t failed at where pr-check ran and did not
// test t's package, sorted. It is asked only of a test that failed only in
// Actions, so its commits are its Actions failures. A commit pr-check never
// ran at says nothing either way and is left out.
func outsideScope(t TestSummary, scopes map[string]map[string]bool) []string {
	var out []string
	for _, commit := range t.Commits {
		tested, ran := scopes[commit]
		if ran && !tested[short(t.Package)] {
			out = append(out, commit)
		}
	}
	sort.Strings(out)
	return out
}

// outsideScopeNote is the text view's line for a test outside pr-check's
// scope at commits.
func outsideScopeNote(commits []string) string {
	shorts := make([]string, 0, len(commits))
	for _, c := range commits {
		shorts = append(shorts, shortSHA(c))
	}
	return "outside pr-check's scope at " + strings.Join(shorts, " ") + " (the FAILURE_PATTERNS 631 shape): pr-check ran there and did not test this package"
}
