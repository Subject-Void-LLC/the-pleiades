// This file is how the inventory edit form's submission is merged with what
// is stored, rather than written over it.
//
// The form used to replace the whole inventory with what it submitted. An
// operator who opened it, and saved a description fix after someone else had
// removed a device, put the device back: on a shared inventory that re-grants
// it to every team the inventory is shared with, with nothing on either page
// saying so. Now the form carries what it was opened with (view.Field's
// KeepsOriginal), and the save is a fold run under the inventory's row lock
// (SetStore.UpdateWith) that applies only what this edit changed:
//
//   - a member list keeps the stored list, less what this edit deselected,
//     plus what it selected: stored - (original - submitted) + (submitted -
//     original). A member someone else removed stays removed unless this
//     edit selects it again, and one they added stays;
//   - a text field this edit left as it was keeps the stored value; one it
//     changed is applied when the stored value is still the original, and
//     refused with a field error when someone else changed it too. The error
//     names neither value, and the redisplay carries the same originals, so
//     submitting it again is refused again: the operator opens the form anew.
package inventories

import (
	"errors"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// changedUnderneathText is the field error for a text field this edit and
// another both changed. It is fixed, so it repeats neither value.
const changedUnderneathText = "Someone else changed this after you opened the form. " +
	"Open the edit form again to see the current value, then make your change there."

// missingOriginalText is the field error for a submission that does not say
// what the form was opened with, so what it changed cannot be told apart from
// what someone else changed since.
const missingOriginalText = "This form does not say what it was opened with, so saving it could undo " +
	"someone else's change. Open the edit form again and make your change there."

// record is what this view reads, renders and writes: an inventory and, on
// an edit submission, what the form showed when it was opened.
type record struct {
	inventory.Set

	// opened is nil except on an edit submission.
	opened *opened
}

// opened is what an edit form showed when it was opened.
type opened struct {
	name, description   string
	groupIDs, deviceIDs []int

	// missing names, in declaration order, each field whose original the
	// submission did not carry.
	missing []string
}

// readOpened reads an edit submission's originals.
func readOpened(v view.Values) *opened {
	o := &opened{}
	var ok bool
	if o.name, ok = v.Original("name"); !ok {
		o.missing = append(o.missing, "name")
	}
	if o.description, ok = v.Original("description"); !ok {
		o.missing = append(o.missing, "description")
	}
	groups, ok := v.OriginalSelected("groups")
	if !ok {
		o.missing = append(o.missing, "groups")
	}
	devices, ok := v.OriginalSelected("devices")
	if !ok {
		o.missing = append(o.missing, "devices")
	}
	o.groupIDs, o.deviceIDs = parseIDs(groups), parseIDs(devices)
	return o
}

// mergeFold is the fold the writer hands SetStore.UpdateWith: the submission
// merged with the stored inventory, as this file's comment describes. The
// organization, id and owner are the store's to keep.
func mergeFold(submitted record) func(inventory.Set) (inventory.Set, error) {
	o := submitted.opened
	return func(stored inventory.Set) (inventory.Set, error) {
		if o == nil {
			return inventory.Set{}, view.FieldFault{Field: "name", Message: missingOriginalText}
		}
		if len(o.missing) > 0 {
			return inventory.Set{}, view.FieldFault{Field: o.missing[0], Message: missingOriginalText}
		}
		name, err := mergeText("name", view.KindText, stored.Name, o.name, submitted.Name)
		if err != nil {
			return inventory.Set{}, err
		}
		description, err := mergeText("description", view.KindLongText, stored.Description, o.description, submitted.Description)
		if err != nil {
			return inventory.Set{}, err
		}
		stored.Name, stored.Description = name, description
		stored.GroupIDs = mergeIDs(stored.GroupIDs, o.groupIDs, submitted.GroupIDs)
		stored.DeviceIDs = mergeIDs(stored.DeviceIDs, o.deviceIDs, submitted.DeviceIDs)
		return stored, nil
	}
}

// mergeText is one text field's three-way merge: the stored value when this
// edit left it as the form showed it, the submitted one when the stored value
// is still what the form showed, and a field error naming field otherwise.
// Each side is compared as a browser sends it back from a control of kind
// (view.Comparable), since the browser rewrites line breaks: a stored LF
// would otherwise never equal the CRLF the form posts, and every edit of the
// field would be refused as a change made underneath.
func mergeText(field string, kind view.FieldKind, stored, original, submitted string) (string, error) {
	same := func(a, b string) bool { return view.Comparable(kind, a) == view.Comparable(kind, b) }
	switch {
	case same(submitted, original):
		return stored, nil
	case same(stored, original):
		return submitted, nil
	default:
		return "", view.FieldFault{Field: field, Message: changedUnderneathText}
	}
}

// mergeIDs is one member list's three-way merge: stored, less what this edit
// deselected (in original, not in submitted), plus what it selected (in
// submitted, not in original), in stored order and then submitted order.
func mergeIDs(stored, original, submitted []int) []int {
	inOriginal, inSubmitted := idSet(original), idSet(submitted)
	var out []int
	kept := map[int]bool{}
	for _, id := range stored {
		if inOriginal[id] && !inSubmitted[id] {
			continue // this edit deselected it
		}
		if !kept[id] {
			kept[id] = true
			out = append(out, id)
		}
	}
	for _, id := range submitted {
		if !inOriginal[id] && !kept[id] {
			kept[id] = true
			out = append(out, id) // this edit selected it
		}
	}
	return out
}

// idSet is ids as a set.
func idSet(ids []int) map[int]bool {
	out := make(map[int]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out
}

// memberRefusalText is the field error for a member the store will not take:
// one that does not exist (a device deleted after the form opened, among
// them) or that belongs to another organization. One sentence for both, as
// the API answers both (owner decision 2026-11-10), so the form cannot tell
// which ids are another tenant's.
const memberRefusalText = "One or more of the selected groups or devices do not exist or cannot be added to this inventory."

// nameTakenText is the field error for a name another inventory of the same
// organization already has.
const nameTakenText = "Another inventory in this organization already has this name."

// asFault turns a store refusal the submitter can act on into the field
// error the form shows beside the control, where it was a 500. Any other
// error, nil included, is returned as it is.
func asFault(err error) error {
	var nul *inventory.NULError
	switch {
	case err == nil:
		return nil
	case errors.Is(err, inventory.ErrUnknownMember), errors.Is(err, inventory.ErrCrossTenantMember):
		return view.FieldFault{Field: "devices", Message: memberRefusalText}
	case errors.Is(err, inventory.ErrSetExists):
		return view.FieldFault{Field: "name", Message: nameTakenText}
	case errors.As(err, &nul):
		return view.FieldFault{Field: nul.Field, Message: "This may not contain a NUL character."}
	default:
		return err
	}
}
