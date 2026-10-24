// Annotating entries: a FAILURE_PATTERNS number and a note set on every
// entry naming one test, and optionally one commit, in both ledgers.
package main

import (
	"fmt"
	"strings"
)

// testPrefixes start a Go test function's name, which is how annotate's
// PKG.Test is split, since a package path holds dots too.
var testPrefixes = []string{".Test", ".Fuzz", ".Example", ".Benchmark"}

// splitTest splits PKG.Test at its test; with no test in it, the whole is a
// package, naming its package-level failures.
func splitTest(name string) (pkg, test string) {
	at := -1
	for _, p := range testPrefixes {
		if i := strings.LastIndex(name, p); i > at {
			at = i
		}
	}
	if at < 0 {
		return name, ""
	}
	return name[:at], name[at+1:]
}

// matches reports whether e is the entry PKG.Test names, by its package's
// full path or any tail of it ending at a slash, and at commit (a full SHA or
// a prefix of one) when commit is given.
func matches(e Entry, pkg, test, commit string) bool {
	if e.Kind != KindTest || e.Test != test {
		return false
	}
	if e.Package != pkg && !strings.HasSuffix(e.Package, "/"+pkg) {
		return false
	}
	return commit == "" || strings.HasPrefix(e.Commit, commit)
}

// annotate sets pattern and, when given, note on every matching entry in
// both ledgers under dir, rewriting each ledger it changes whole, and
// returns how many entries it set.
func annotate(dir, name, commit string, pattern int, note string) (int, error) {
	if pattern <= 0 {
		return 0, fmt.Errorf("-pattern is a FAILURE_PATTERNS number, 1 or more")
	}
	pkg, test := splitTest(name)
	note = scrub(note)
	total := 0
	for _, source := range KnownSources {
		path := ledgerFile(dir, source)
		entries, err := readLedger(path)
		if err != nil {
			return total, err
		}
		changed := 0
		for i := range entries {
			if matches(entries[i], pkg, test, commit) {
				entries[i].Pattern = pattern
				if note != "" {
					entries[i].Note = note
				}
				changed++
			}
		}
		if changed > 0 {
			if err := writeLedger(path, entries); err != nil {
				return total, err
			}
		}
		total += changed
	}
	if total == 0 {
		return 0, fmt.Errorf("no entry names %s%s", name, atCommit(commit))
	}
	return total, nil
}

// atCommit is " at COMMIT", or nothing.
func atCommit(commit string) string {
	if commit == "" {
		return ""
	}
	return " at " + commit
}
