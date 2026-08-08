package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// mdEscape escapes the handful of characters that break a Markdown table
// cell if left raw: a pipe would end the cell early, and a literal
// newline would end the row. Backticked code spans are passed through
// untouched on purpose, so a Param's Type column can still read `int`
// rather than a mangled escape.
func mdEscape(s string) string {
	s = strings.ReplaceAll(s, "|", "\\|")
	s = strings.ReplaceAll(s, "\n", " ")
	return s
}

// code wraps s in a Markdown inline code span, or renders "-" for an
// empty value so a table cell is never blank in a way that reads as a
// rendering bug.
func code(s string) string {
	if s == "" {
		return "-"
	}
	return "`" + s + "`"
}

// yesNo renders a bool as the word a reference page reads better with
// than a bare "true"/"false".
func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// table renders a Markdown table from a header row and any number of data
// rows, padding no columns: GitHub-flavored Markdown does not require
// aligned pipes, and hand-aligning 75 generated tables is not this
// generator's job.
func table(header []string, rows [][]string) string {
	var b strings.Builder
	b.WriteString("| " + strings.Join(header, " | ") + " |\n")
	b.WriteString("|" + strings.Repeat(" --- |", len(header)) + "\n")
	for _, row := range rows {
		escaped := make([]string, len(row))
		for i, cell := range row {
			escaped[i] = mdEscape(cell)
		}
		b.WriteString("| " + strings.Join(escaped, " | ") + " |\n")
	}
	return b.String()
}

// frontMatter renders the YAML front matter every generated page starts
// with: just the status badge today, a fixed field every hand-written
// page under docs/ also carries (see docs-lint's own scan list).
func frontMatter(status string) string {
	return fmt.Sprintf("---\nstatus: %s\n---\n\n", status)
}

// sortedKeys returns m's keys sorted, so every generated index is
// byte-stable across regeneration and a diff shows only real changes.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// quoteList renders a slice of names as a comma-separated list of code
// spans, or "-" for an empty slice.
func quoteList(names []string) string {
	if len(names) == 0 {
		return "-"
	}
	parts := make([]string, len(names))
	for i, n := range names {
		parts[i] = code(n)
	}
	return strings.Join(parts, ", ")
}

// itoa is strconv.Itoa, aliased so callers reads as plain prose instead
// of reaching for the strconv package name inline everywhere a count is
// rendered into a table cell.
func itoa(n int) string {
	return strconv.Itoa(n)
}
