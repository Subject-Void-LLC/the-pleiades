// Finding the one line of a test's output that says why it failed, from
// the end of its output a record or a log keeps.
package main

import (
	"regexp"
	"strings"
)

var (
	// failWordsRE matches a line that states a failure, rather than one a
	// test logged on its way there.
	failWordsRE = regexp.MustCompile(`(?i)\b(want|wanted|got|expected|fail|failed|failure|error|panic|timed out|timeout|deadline|eof|refused|refuses|over the|not|never|unexpected|mismatch|missing)\b`)
	// testSaidRE matches a line a test wrote through its testing.T, which go
	// test prefixes with the file and line that wrote it.
	testSaidRE = regexp.MustCompile(`^[A-Za-z0-9_.-]+\.go:\d+: `)
	// goLineRE matches go test's own framing, which says nothing about why.
	goLineRE = regexp.MustCompile(`^(=== (RUN|PAUSE|CONT|NAME)|--- (PASS|SKIP)|PASS$|ok\s)`)
)

// firstError is the line of output that says why its test failed: within
// the first run that failed (output after the last === RUN before the first
// --- FAIL), the first line stating a failure, else the last line there,
// else a panic line, else the last line that is not go test's framing. It is
// scrubbed and capped like any free text a ledger stores.
func firstError(output string) string {
	var lines []string
	for _, l := range strings.Split(output, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	failAt := -1
	for i, l := range lines {
		if strings.HasPrefix(l, "--- FAIL") {
			failAt = i
			break
		}
	}
	if failAt >= 0 {
		start := 0
		for i := failAt - 1; i >= 0; i-- {
			if strings.HasPrefix(lines[i], "=== RUN") {
				start = i + 1
				break
			}
		}
		// What the test said through t.Log or t.Error comes first, before
		// what anything else it ran printed.
		var told, said []string
		for _, l := range lines[start:failAt] {
			switch {
			case testSaidRE.MatchString(l):
				told = append(told, l)
			case !goLineRE.MatchString(l):
				said = append(said, l)
			}
		}
		for _, group := range [][]string{told, said} {
			for _, l := range group {
				if failWordsRE.MatchString(l) {
					return scrub(l)
				}
			}
			if len(group) > 0 {
				return scrub(group[len(group)-1])
			}
		}
	}
	for _, l := range lines {
		if strings.HasPrefix(l, "panic:") {
			return scrub(l)
		}
	}
	for i := len(lines) - 1; i >= 0; i-- {
		if !goLineRE.MatchString(lines[i]) && !strings.HasPrefix(lines[i], "--- FAIL") {
			return scrub(lines[i])
		}
	}
	return ""
}
