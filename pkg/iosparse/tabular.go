// An independent reimplementation of parsergen.oper_fill_tabular from
// Cisco's separate genie library, which two of the ported parsers call. It
// is written from that function's documentation and the fixtures; none of
// its code was copied.
package iosparse

// tabular reads a column-aligned table the way genie.parsergen's
// oper_fill_tabular documents it: the header line fixes each column's
// position, and every row below it is cut at those positions.
//
// parsergen ships compiled in Cisco's genie package, not in genieparser,
// so there is no source to port. This follows its documentation: a
// left-justified column starts at its header's first character and ends
// just before the next header; a right-justified column ends where its
// header ends and starts where the previous header ended. The details the
// documentation leaves open are decided as the fixtures require: a row
// of only dashes (the rule under the header) is skipped, the last column
// runs to the end of its row, each value is stripped, and the table ends
// at the first row the terminal pattern matches.
type tabular struct {
	// headers are the header field expressions, in column order. A field
	// may carry trailing spaces that fix how wide its column is.
	headers []string
	// labels name each column in an entry, one per header.
	labels []string
	// rightJustified cuts columns at header ends rather than starts.
	rightJustified bool
	// terminal ends the table; it is matched against each row with its
	// newline, so `^\n` matches an empty row.
	terminal *rx
}

// reDashRule matches the row of dashes under a table's header.
var reDashRule = re(`^\s*-[-\s]*$`)

// fill parses out and returns the entries: a dict keyed by each row's
// first column (stripped), holding that row's values by label. It returns
// an empty dict when no line carries every header.
func (t tabular) fill(out string) *dict {
	entries := newDict()
	lines := splitlines(out)
	starts, ends, at := t.header(lines)
	if at < 0 {
		return entries
	}
	for _, line := range lines[at+1:] {
		if t.terminal.match(line+"\n") != nil {
			break
		}
		if reDashRule.match(line) != nil {
			continue
		}
		runes := []rune(line)
		entry := newDict()
		for i, label := range t.labels {
			entry.set(label, strip(pySlice(runes, t.colStart(i, starts, ends), t.colEnd(i, starts, ends))))
		}
		entries.set(asString(entry.get(t.labels[0])), entry)
	}
	return entries
}

// header finds the first line carrying every header field in order and
// returns each field's start and end, counted in characters, with the
// line's index; the index is -1 when no line has them all.
func (t tabular) header(lines []string) (starts, ends []int, at int) {
	exprs := make([]*rx, len(t.headers))
	for i, h := range t.headers {
		exprs[i] = re(h)
	}
	for n, line := range lines {
		starts, ends = starts[:0], ends[:0]
		from := 0
		for _, e := range exprs {
			loc := e.any.FindStringIndex(line[from:])
			if loc == nil {
				break
			}
			starts = append(starts, runeCount(line[:from+loc[0]]))
			ends = append(ends, runeCount(line[:from+loc[1]]))
			from += loc[1]
		}
		if len(starts) == len(exprs) {
			return starts, ends, n
		}
	}
	return nil, nil, -1
}

// colStart is where column i begins in a row.
func (t tabular) colStart(i int, starts, ends []int) int {
	if !t.rightJustified {
		return starts[i]
	}
	if i == 0 {
		return 0
	}
	return ends[i-1]
}

// colEnd is where column i ends in a row; the last column runs to the end.
func (t tabular) colEnd(i int, starts, ends []int) int {
	if i == len(starts)-1 {
		return -1
	}
	if t.rightJustified {
		return ends[i]
	}
	return starts[i+1]
}

// runeCount is the length of s in characters, Python's len(s).
func runeCount(s string) int {
	return len([]rune(s))
}

// asString returns v as a string, "" when it is not one.
func asString(v any) string {
	s, _ := v.(string)
	return s
}
