// Package main: the rule about the repository's record archives.
//
// FAILURE_PATTERNS_ARCHIVE.md and LESSONS_LEARNED_ARCHIVE.md hold every
// entry in full, and AGENTS.md's rule for them is to append, never to
// renumber. A commit once replaced FAILURE_PATTERNS_ARCHIVE.md's 12826
// lines with the four entries it meant to add (FAILURE_PATTERNS 513), and
// nothing noticed: the index still listed every title. So an archive that
// a commit leaves shorter, or deletes, fails; one it edits without
// shrinking (a correction to an old entry) warns, since that is sometimes
// right and a reader should see it.
package main

import (
	"fmt"
	"strconv"
	"strings"
)

// archiveChange is what a commit does to one record archive, as git diff
// --numstat counts it.
type archiveChange struct {
	Path           string
	Added, Removed int
}

// archiveChanges reads what this commit does to every record archive at
// the repository's top level, a deleted one included.
func archiveChanges() ([]archiveChange, error) {
	out, err := git("diff", "--cached", "--numstat", baseRef(), "--", ":(glob)*_ARCHIVE.md")
	if err != nil {
		return nil, err
	}
	return parseNumstat(string(out))
}

// parseNumstat reads git diff --numstat lines: added, removed and path,
// separated by tabs. A binary file's "-" counts are refused, since an
// archive is text.
func parseNumstat(out string) ([]archiveChange, error) {
	var changes []archiveChange
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		fields := strings.SplitN(line, "\t", 3)
		if len(fields) != 3 {
			return nil, fmt.Errorf("git diff --numstat printed %q, not added, removed and a path", line)
		}
		added, errA := strconv.Atoi(fields[0])
		removed, errR := strconv.Atoi(fields[1])
		if errA != nil || errR != nil {
			return nil, fmt.Errorf("git diff --numstat counted %s as binary, and a record archive is text", fields[2])
		}
		changes = append(changes, archiveChange{Path: fields[2], Added: added, Removed: removed})
	}
	return changes, nil
}

// checkArchives fails an archive a commit leaves shorter and warns about
// one it edits.
func checkArchives(changes []archiveChange) []Finding {
	var findings []Finding
	for _, c := range changes {
		switch {
		case c.Removed > c.Added:
			findings = append(findings, Finding{Severity: SeverityFail, Rule: "a record archive is only appended to", File: c.Path,
				Detail: fmt.Sprintf("this commit leaves it %d line(s) shorter (%d removed, %d added); add new entries at its end and keep every earlier one, and if it was rewritten from an excerpt, restore it from the last commit first", c.Removed-c.Added, c.Removed, c.Added)})
		case c.Removed > 0:
			findings = append(findings, Finding{Severity: SeverityWarn, Rule: "a record archive is only appended to", File: c.Path,
				Detail: fmt.Sprintf("this commit changes %d existing line(s); fine for a correction to an old entry, never for a renumbering", c.Removed)})
		}
	}
	return findings
}
