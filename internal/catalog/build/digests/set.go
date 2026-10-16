// build.digests.refresh's set parameter: exact rewrites, by file, line and
// the text there, which is also how its undo is written.
package digests

import (
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/imagepin"
)

// setEdit is one entry of set.
type setEdit struct {
	file     string
	line     int
	from, to string
}

// setParam reads set: nil when absent, refused when malformed.
func setParam(params map[string]any) ([]setEdit, error) {
	raw, present := params[paramSet]
	if !present || raw == nil {
		return nil, nil
	}
	list, ok := raw.([]any)
	if !ok || len(list) == 0 {
		return nil, fmt.Errorf("%s is a list of {file, line, from, to}", paramSet)
	}
	var out []setEdit
	for i, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s[%d] is not a mapping", paramSet, i)
		}
		e := setEdit{}
		e.file, _ = m["file"].(string)
		e.from, _ = m["from"].(string)
		e.to, _ = m["to"].(string)
		e.line = lineNumber(m["line"])
		if err := checkPath(paramSet, e.file); err != nil {
			return nil, err
		}
		if e.line < 1 || e.from == "" {
			return nil, fmt.Errorf("%s[%d] needs file, a line from 1, from and to", paramSet, i)
		}
		// Both ends must parse: the undo record writes from back, and its
		// replay is held to the same rule, so a from that does not parse
		// would make an undo that cannot run.
		if _, err := imagepin.Parse(e.from); err != nil {
			return nil, fmt.Errorf("%s[%d]: from: %w", paramSet, i, err)
		}
		if _, err := imagepin.Parse(e.to); err != nil {
			return nil, fmt.Errorf("%s[%d]: to: %w", paramSet, i, err)
		}
		out = append(out, e)
	}
	return out, nil
}

// setChanges finds each set entry's reference, by file, line and the text
// it holds now, refusing one that is not there.
func setChanges(files []*file, sets []setEdit) ([]change, error) {
	byPath := map[string]*file{}
	for _, f := range files {
		byPath[f.path] = f
	}
	var changes []change
	for _, s := range sets {
		f, ok := byPath[s.file]
		if !ok {
			return nil, fmt.Errorf("set names %s, which files does not", s.file)
		}
		found := false
		for _, ref := range f.refs {
			if ref.Line != s.line || ref.Unresolvable != "" {
				continue
			}
			if ref.Text == s.from {
				if s.from != s.to {
					changes = append(changes, change{file: f, ref: ref, from: s.from, to: s.to})
				}
				found = true
				break
			}
			// A line that already holds to needs nothing: a set run twice,
			// or an undo record kept by a run that failed before it wrote
			// this file.
			if ref.Text == s.to {
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("%s:%d holds no image reference %q, so it was not changed", s.file, s.line, s.from)
		}
	}
	return changes, nil
}

// maxLine bounds a line number: a file is read whole only up to
// imagepin.MaxFileBytes, so no line in one is past that many plus one.
const maxLine = imagepin.MaxFileBytes + 1

// lineNumber is v as a line number from 1 to maxLine, or 0 for anything
// else (a type that is not a number, a fraction, or a value out of range),
// which the caller refuses. Each kind is compared before it is converted,
// so no conversion can wrap.
func lineNumber(v any) int {
	switch n := v.(type) {
	case int:
		if n >= 1 && n <= maxLine {
			return n
		}
	case int64:
		if n >= 1 && n <= maxLine {
			return int(n)
		}
	case uint64:
		if n >= 1 && n <= maxLine {
			return int(n)
		}
	case float64:
		if n >= 1 && n <= maxLine && n == float64(int(n)) {
			return int(n)
		}
	}
	return 0
}
