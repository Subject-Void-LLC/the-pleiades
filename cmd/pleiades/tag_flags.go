// Package main: the --tags and --skip-tags flags `pleiades run` and
// `pleiades validate` share.
package main

import (
	"errors"
	"flag"
	"slices"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
)

// tagFlags registers --tags and --skip-tags on fs and returns the filter
// they fill. Each flag takes a comma-separated list and may be repeated;
// the names keep Ansible's meanings, special ones included (all, tagged,
// untagged, always, never), and a name no task carries is refused when
// the filter is applied (engine.Select), so a misspelled --skip-tags
// cannot quietly run what it was meant to hold back.
func tagFlags(fs *flag.FlagSet) *engine.TagFilter {
	f := &engine.TagFilter{}
	fs.Func("tags", "run only the tasks carrying one of these tags (comma-separated, repeatable)", func(v string) error {
		return appendTagNames(&f.Tags, v)
	})
	fs.Func("skip-tags", "leave out the tasks carrying one of these tags, even ones --tags selects (comma-separated, repeatable)", func(v string) error {
		return appendTagNames(&f.SkipTags, v)
	})
	return f
}

// appendTagNames adds each comma-separated name in v to dst once,
// refusing an empty one.
func appendTagNames(dst *[]string, v string) error {
	for _, name := range strings.Split(v, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			return errors.New("needs a tag name")
		}
		if !slices.Contains(*dst, name) {
			*dst = append(*dst, name)
		}
	}
	return nil
}

// describeSelection renders f as the flags that produced it, for the
// plan header, or "" for the default selection.
func describeSelection(f engine.TagFilter) string {
	var parts []string
	if len(f.Tags) > 0 {
		parts = append(parts, "--tags "+strings.Join(f.Tags, ","))
	}
	if len(f.SkipTags) > 0 {
		parts = append(parts, "--skip-tags "+strings.Join(f.SkipTags, ","))
	}
	return strings.Join(parts, " ")
}
