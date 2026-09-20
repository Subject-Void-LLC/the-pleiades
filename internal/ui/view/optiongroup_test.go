// Package view_test's option grouping: how a picker that offers more than one
// sort of thing says which is which.
package view_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// TestGroupOptions records the two properties a caller depends on: the order
// they gave is kept, and consecutive options sharing a group become one run.
//
// Order is the load-bearing one. Gathering ungrouped options to the front would
// silently reorder a picker whose first option was deliberately first, and
// regrouping non-adjacent options would produce headings a caller did not ask
// for.
func TestGroupOptions(t *testing.T) {
	cases := []struct {
		name    string
		options []view.Option
		want    []view.OptionGroup
	}{
		{
			// A field whose options carry no group at all, which is every
			// existing picker: one unlabelled run, rendering exactly as a flat
			// list did.
			name:    "options with no group are one unlabelled run",
			options: []view.Option{{Value: "1", Label: "one"}, {Value: "2", Label: "two"}},
			want: []view.OptionGroup{{
				Options: []view.Option{{Value: "1", Label: "one"}, {Value: "2", Label: "two"}},
			}},
		},
		{
			name: "consecutive options sharing a group become one group",
			options: []view.Option{
				{Value: "1", Label: "a", Group: "Job template"},
				{Value: "2", Label: "b", Group: "Job template"},
				{Value: "3", Label: "c", Group: "Project sync"},
			},
			want: []view.OptionGroup{
				{Label: "Job template", Options: []view.Option{
					{Value: "1", Label: "a", Group: "Job template"},
					{Value: "2", Label: "b", Group: "Job template"},
				}},
				{Label: "Project sync", Options: []view.Option{
					{Value: "3", Label: "c", Group: "Project sync"},
				}},
			},
		},
		{
			// Interleaved groups get the headings the caller asked for rather
			// than a silent regrouping, and an ungrouped option keeps its
			// position relative to them.
			name: "interleaved groups are not gathered together",
			options: []view.Option{
				{Value: "0", Label: "any"},
				{Value: "1", Label: "a", Group: "A"},
				{Value: "2", Label: "b", Group: "B"},
				{Value: "3", Label: "c", Group: "A"},
			},
			want: []view.OptionGroup{
				{Options: []view.Option{{Value: "0", Label: "any"}}},
				{Label: "A", Options: []view.Option{{Value: "1", Label: "a", Group: "A"}}},
				{Label: "B", Options: []view.Option{{Value: "2", Label: "b", Group: "B"}}},
				{Label: "A", Options: []view.Option{{Value: "3", Label: "c", Group: "A"}}},
			},
		},
		{
			name:    "no options is no groups",
			options: nil,
			want:    []view.OptionGroup{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := view.GroupOptions(tc.options)
			if len(got) != len(tc.want) {
				t.Fatalf("GroupOptions() returned %d groups, want %d: %+v", len(got), len(tc.want), got)
			}
			for i := range got {
				if got[i].Label != tc.want[i].Label {
					t.Errorf("group %d is labelled %q, want %q", i, got[i].Label, tc.want[i].Label)
				}
				if len(got[i].Options) != len(tc.want[i].Options) {
					t.Fatalf("group %d holds %d options, want %d", i, len(got[i].Options), len(tc.want[i].Options))
				}
				for j := range got[i].Options {
					if got[i].Options[j].Value != tc.want[i].Options[j].Value {
						t.Errorf("group %d option %d is %q, want %q",
							i, j, got[i].Options[j].Value, tc.want[i].Options[j].Value)
					}
				}
			}
		})
	}
}

// TestFormModel_ChoiceGroups proves the form model reads a field's own options
// through the grouping, which is what the select template renders.
func TestFormModel_ChoiceGroups(t *testing.T) {
	field := view.Field{Name: "runs", Kind: view.KindSelect}
	model := view.FormModel{
		Options: map[string][]view.Option{
			"runs": {
				{Value: "1", Label: "patch the edge", Group: "Job template"},
				{Value: "2", Label: "automation", Group: "Project sync"},
			},
		},
	}

	groups := model.ChoiceGroups(field)
	if len(groups) != 2 {
		t.Fatalf("ChoiceGroups() returned %d groups, want 2: %+v", len(groups), groups)
	}
	if groups[0].Label != "Job template" || groups[1].Label != "Project sync" {
		t.Errorf("groups are labelled %q and %q", groups[0].Label, groups[1].Label)
	}

	// A field with no options at all is no groups rather than one empty group,
	// so a template iterating them draws nothing.
	if got := model.ChoiceGroups(view.Field{Name: "absent"}); len(got) != 0 {
		t.Errorf("ChoiceGroups() of a field with no options = %+v, want none", got)
	}
}
