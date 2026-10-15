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
		switch line := m["line"].(type) {
		case int:
			e.line = line
		case int64:
			e.line = int(line)
		case float64:
			e.line = int(line)
		case uint64:
			e.line = int(line)
		}
		if err := checkPath(paramSet, e.file); err != nil {
			return nil, err
		}
		if e.line < 1 || e.from == "" {
			return nil, fmt.Errorf("%s[%d] needs file, a line from 1, from and to", paramSet, i)
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
			if ref.Line == s.line && ref.Text == s.from && ref.Unresolvable == "" {
				if s.from != s.to {
					changes = append(changes, change{file: f, ref: ref, from: s.from, to: s.to})
				}
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
