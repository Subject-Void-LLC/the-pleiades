// This file is the RUNS picker: what a schedule may be pointed at.
//
// It offers launchables rather than templates, which is the whole of what this
// view gained when schedules stopped being template-only: a project whose run
// is a sync appears beside a job template, labelled by what it is, and
// choosing either produces the same kind of schedule.
//
// It offers only what the person looking at it may actually launch, filtered
// through the same launchable.Reach the store checks on submit. Two separate
// reasons, and both matter. A control must not offer a choice that can only
// fail, and a rule that lived only in the chooser would not be enforced at
// all, since the same field is accepted over the API where there are no
// options to validate against. One predicate, asked twice.
package schedules

import (
	"context"
	"fmt"
	"strconv"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launchable"
	"github.com/Subject-Void-LLC/the-pleiades/internal/schedule"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// pickerLimit bounds how many launchables the picker offers.
//
// A select is not a listing: past a screenful it stops being a control
// somebody can use, and a deployment with more launchables than this needs a
// search rather than a longer list. The cap is stated here rather than left to
// the store's default so that raising it is a deliberate edit to a picker.
const pickerLimit = 200

// Launchables is the slice of the launchable store this picker needs.
type Launchables interface {
	List(ctx context.Context, q launchable.Query) ([]launchable.Target, error)
}

// runsOptions builds the RUNS picker, filtered to what the viewer may launch.
func runsOptions(launchables Launchables) func(context.Context) ([]view.Option, error) {
	return func(ctx context.Context) ([]view.Option, error) {
		if launchables == nil {
			return nil, nil
		}

		found, err := launchables.List(ctx, launchable.Query{Limit: pickerLimit})
		if err != nil {
			return nil, err
		}

		// The viewer's own reach. An unauthenticated context offers nothing
		// rather than everything, which is the refusing direction: the zero
		// Reach admits no type.
		identity, _ := api.IdentityFromContext(ctx)
		reach := launchable.ReachOf(identity, 0)

		opts := make([]view.Option, 0, len(found))
		for _, t := range found {
			if err := reach.Admits(t); err != nil {
				continue
			}
			opts = append(opts, view.Option{
				Value: strconv.Itoa(t.ID),
				Label: optionLabel(t),
				Group: groupLabel(t),
			})
		}
		return opts, nil
	}
}

// optionLabel is what one launchable reads as in the picker: its own name.
// What sort of thing it is comes from the group it sits in, so the name is not
// repeated with its type in every row.
func optionLabel(t launchable.Target) string { return t.Name }

// groupLabel names the group a launchable belongs to: its type's own label,
// falling back to the raw key for a type this build does not know, which can
// only happen for a row written by a newer version.
func groupLabel(t launchable.Target) string {
	if d, err := launchable.Describe(t); err == nil {
		return d.Label
	}
	return t.Type
}

// targetLabel names what a schedule runs, for a list cell and a detail row.
//
// It carries the type because a list mixes them: "nightly rebuild" alone does
// not say whether tonight brings a playbook run or a git fetch. A schedule
// whose target was not loaded renders its reference rather than an empty cell,
// so a reader can still get from the row to the thing.
func targetLabel(s schedule.Schedule) string {
	switch {
	case s.Launchable.Name == "" && s.LaunchableID == 0:
		return ""
	case s.Launchable.Name == "":
		return fmt.Sprintf("#%d", s.LaunchableID)
	default:
		return fmt.Sprintf("%s (%s)", s.Launchable.Name, groupLabel(s.Launchable))
	}
}
