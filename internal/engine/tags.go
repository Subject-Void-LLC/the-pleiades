// Package engine: the tags key a runbook, a block or a task may carry,
// and the selection rule `--tags` and `--skip-tags` apply to it.
//
// The rule is Ansible's own (Taggable.evaluate_tags), because a runbook
// converted from a playbook must select the same tasks the playbook did.
// Its one surprise is kept on purpose: a run with no --tags behaves as
// --tags all, and all excludes a task tagged never, so such a task runs
// only when a run names one of its tags. Playbooks rely on that to keep a
// destructive task (a teardown, a wipe) from running by accident, so the
// builder applies it to every runbook, on every tier, whether or not a
// filter was given.
package engine

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/Subject-Void-LLC/the-pleiades/internal/termsafe"
)

// The tag names Ansible gives a meaning of its own.
const (
	// TagAll, as a filter, selects every task not tagged never.
	TagAll = "all"
	// TagAlways on a task runs it whatever --tags says, unless --skip-tags
	// names always itself.
	TagAlways = "always"
	// TagNever on a task keeps it from running unless --tags names never
	// or another of its tags.
	TagNever = "never"
	// TagTagged, as a filter, selects every task with at least one tag.
	TagTagged = "tagged"
	// TagUntagged, as a filter, selects every task with no tag. An untagged
	// task is treated as carrying it, which is how Ansible matches it.
	TagUntagged = "untagged"
)

// filterOnlyTags are the special names that mean something only in a
// filter, so a task may not carry one: a task tagged all would be
// selected by a filter meant to exclude it.
var filterOnlyTags = []string{TagAll, TagTagged, TagUntagged}

// TagList is the tags key: a list of names, or one string that Ansible
// splits on commas. A tag that could not be matched as written is
// refused when the runbook is read: an empty one, a template (no run
// renders it), one that a terminal would act on, and the filter-only
// names all, tagged and untagged.
type TagList []string

// UnmarshalYAML accepts a scalar (split on commas) or a list of scalars.
func (t *TagList) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		return t.set(strings.Split(node.Value, ","))
	case yaml.SequenceNode:
		raw := make([]string, 0, len(node.Content))
		for _, item := range node.Content {
			if item.Kind != yaml.ScalarNode {
				return fmt.Errorf("tags must be a string or a list of strings, got a YAML %s inside the list", yamlKindName(item.Kind))
			}
			raw = append(raw, item.Value)
		}
		return t.set(raw)
	default:
		return fmt.Errorf("tags must be a string or a list of strings, got a YAML %s", yamlKindName(node.Kind))
	}
}

// UnmarshalJSON accepts a string (split on commas) or an array of strings
// and numbers, as UnmarshalYAML does.
func (t *TagList) UnmarshalJSON(data []byte) error {
	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		return t.set(strings.Split(single, ","))
	}
	var list []json.RawMessage
	if err := json.Unmarshal(data, &list); err != nil {
		return fmt.Errorf("tags must be a string or a list of strings")
	}
	raw := make([]string, 0, len(list))
	for _, item := range list {
		var s string
		if err := json.Unmarshal(item, &s); err == nil {
			raw = append(raw, s)
			continue
		}
		var n json.Number
		if err := json.Unmarshal(item, &n); err != nil {
			return fmt.Errorf("tags must be a string or a list of strings")
		}
		raw = append(raw, n.String())
	}
	return t.set(raw)
}

// set checks raw's names and stores them trimmed, in order, without
// repeats.
func (t *TagList) set(raw []string) error {
	var out TagList
	for _, name := range raw {
		name = strings.TrimSpace(name)
		if err := checkTagName(name); err != nil {
			return err
		}
		if slices.Contains(filterOnlyTags, name) {
			return fmt.Errorf("tag %q only has meaning in --tags and --skip-tags, so a task may not carry it", name)
		}
		if !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	*t = out
	return nil
}

// checkTagName refuses a tag name that could not be matched as written.
// It is shared by the runbook's tags key and the --tags/--skip-tags
// values, so a name one accepts the other can match.
func checkTagName(name string) error {
	switch {
	case name == "":
		return fmt.Errorf("a tag may not be empty")
	case strings.Contains(name, "{{") || strings.Contains(name, "{%"):
		return fmt.Errorf("tag %q is a template, and nothing renders a tag: write the name itself", name)
	case termsafe.Check(name) != nil:
		return fmt.Errorf("tag %q holds a control or text-direction character", termsafe.EscapeLine(name))
	}
	return nil
}

// TagFilter is a run's selection: Tags is --tags and SkipTags is
// --skip-tags. The zero value is Ansible's default, --tags all.
type TagFilter struct {
	Tags     []string `json:"tags,omitempty"`
	SkipTags []string `json:"skip_tags,omitempty"`
}

// IsZero reports whether f is the default selection.
func (f TagFilter) IsZero() bool {
	return len(f.Tags) == 0 && len(f.SkipTags) == 0
}

// Runs reports whether a task carrying tags (its own and every tag it
// inherits) is selected by f. It is Ansible's Taggable.evaluate_tags,
// line for line, with an empty Tags read as all.
func (f TagFilter) Runs(tags []string) bool {
	if len(tags) == 0 {
		tags = []string{TagUntagged}
	}
	has := func(set []string, name string) bool { return slices.Contains(set, name) }
	overlaps := func(set []string) bool {
		for _, t := range tags {
			if slices.Contains(set, t) {
				return true
			}
		}
		return false
	}
	untagged := len(tags) == 1 && tags[0] == TagUntagged

	only := f.Tags
	if len(only) == 0 {
		only = []string{TagAll}
	}
	run := false
	switch {
	case has(tags, TagAlways):
		run = true
	case has(only, TagAll) && !has(tags, TagNever):
		run = true
	case overlaps(only):
		run = true
	case has(only, TagTagged) && !untagged && !has(tags, TagNever):
		run = true
	}
	if !run || len(f.SkipTags) == 0 {
		return run
	}
	switch {
	case has(f.SkipTags, TagAll):
		return has(tags, TagAlways) && !has(f.SkipTags, TagAlways)
	case overlaps(f.SkipTags):
		return false
	case has(f.SkipTags, TagTagged) && !untagged:
		return false
	}
	return true
}

// propagateTags pushes every tags key down to the tasks it covers: a
// runbook's to every task, and a block's or parallel group's to its
// children (a block's to its rescue and always tasks too), at any depth,
// so each task carries every tag that selects it, which is how Ansible
// reads a tag on a block or a play. It runs after resolveImportTasks, so
// an imported file's tasks inherit the tags of the task that imported
// them.
func propagateTags(def *WorkflowDef) {
	for _, list := range [][]Task{def.PreTasks, def.Tasks, def.PostTasks} {
		propagateTagsInto(list, def.Tags)
	}
}

// propagateTagsInto adds inherited to each task in tasks, then recurses
// with each task's own full set.
func propagateTagsInto(tasks []Task, inherited TagList) {
	for i := range tasks {
		t := &tasks[i]
		for _, name := range inherited {
			if !slices.Contains(t.Tags, name) {
				t.Tags = append(t.Tags, name)
			}
		}
		for _, sub := range [][]Task{t.Block, t.Rescue, t.Always, t.Parallel} {
			propagateTagsInto(sub, t.Tags)
		}
	}
}
