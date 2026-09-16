// This file is the row half of the credential type's write path: removing
// one input from its schema, and one entry from its injector document.
//
// Until it existed both tabs were one-way doors. An input could be added
// and never taken out, an injector added and never taken out, and the only
// route back was the JSON API or the database. Adding without removing is
// the shape a control plane cannot actually be administered through,
// because the first typo is permanent.
//
// Both controls carry a confirmation, and both confirmations say what the
// removal costs rather than only asking whether you are sure. Removing an
// input that credentials of the type already store a value for leaves those
// credentials unsaveable, which is not obvious from the button and is not
// something this page can undo for you.
package credentialtypes

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/apispec"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// The row controls' URL segments. They share one namespace with the record
// actions beside them, which Register enforces.
const (
	removeInputName    = "remove-input"
	removeInjectorName = "remove-injector"
)

// removeInputAction takes one input out of a type's schema.
//
// It names the same endpoint the add control does, because it is the same
// API operation: set-inputs replaces the whole document either way, and the
// question "may this caller set this type's inputs" has one answer for both
// controls. A second endpoint here would be a relation invented to describe
// no separate operation.
func removeInputAction(store credstore.Store) view.RowAction {
	return view.RowAction{
		Name:     removeInputName,
		Label:    "Remove",
		Endpoint: &apispec.SetCredentialTypeInputs,
		Confirm: "Any credential of this type that stores a value for this input can no longer be saved, " +
			"because the value it holds will name an input the type no longer declares. " +
			"An injector that references it refuses the change outright.",
		Submit: func(ctx context.Context, parentID, rowID string) (string, error) {
			return withType(ctx, store, parentID, inputsTitle, func(ct *credstore.CredentialType) error {
				return removeInput(&ct.Inputs, rowID)
			})
		},
	}
}

// removeInjectorAction takes one entry out of a type's injector document.
//
// Removing is the only edit an injector has, which is deliberate: the
// package doc comment explains why the document is never a free-form field,
// and changing one in place is a remove and an add, both of which the store
// judges on the way through.
func removeInjectorAction(store credstore.Store) view.RowAction {
	return view.RowAction{
		Name:     removeInjectorName,
		Label:    "Remove",
		Endpoint: &apispec.SetCredentialTypeInjectors,
		Confirm: "Credentials of this type stop reaching a run through this injector. " +
			"Nothing about the credentials themselves changes, so adding it back restores the behaviour.",
		Submit: func(ctx context.Context, parentID, rowID string) (string, error) {
			return withType(ctx, store, parentID, injectorsTitle, func(ct *credstore.CredentialType) error {
				return removeInjector(&ct.Injectors, rowID)
			})
		},
	}
}

// withType is the read, edit, write both controls share.
//
// It reads the stored type so the document the control is not editing is
// carried forward unchanged, which is the same reason the add controls read
// it: set-inputs leaves the injectors alone and set-injectors leaves the
// schema alone, and a write built from the form alone would blank whichever
// it did not carry.
func withType(ctx context.Context, store credstore.Store, parentID, tab string,
	edit func(*credstore.CredentialType) error) (string, error) {

	numeric, err := strconv.Atoi(parentID)
	if err != nil {
		return "", credstore.ErrNotFound
	}
	ct, err := store.GetType(ctx, numeric)
	if err != nil {
		return "", err
	}

	if err := edit(&ct); err != nil {
		return "", err
	}

	if _, err := store.UpdateType(ctx, numeric, ct.CredentialType); err != nil {
		return "", removalFault(err)
	}
	return "/ui/" + Name + "/" + parentID + "?tab=" + view.TabSlug(tab), nil
}

// removalFault decides which of the store's refusals is the operator's to
// fix.
//
// Only two are. A schema an injector still depends on and a type this
// platform ships are both rules the person at the keyboard can satisfy, and
// both are shown in the store's own words. Anything else is a fault: the
// database was unreachable, a constraint nobody anticipated fired, and
// neither is something to put on a page as though it were their doing.
func removalFault(err error) error {
	switch {
	case errors.Is(err, credtype.ErrInvalidType), errors.Is(err, credstore.ErrManaged):
		return view.Refuse(err)
	default:
		return err
	}
}

// removeInput drops one input from the schema and from the required list.
//
// A row id that matches nothing is refused rather than treated as a
// successful no-op. The two are indistinguishable to whoever pressed the
// button, and "the input is gone" is the reading they would take from a
// redirect, which on a stale page is false.
func removeInput(schema *credtype.InputSchema, id string) error {
	kept := make([]credtype.InputField, 0, len(schema.Fields))
	for _, f := range schema.Fields {
		if f.ID != id {
			kept = append(kept, f)
		}
	}
	if len(kept) == len(schema.Fields) {
		return view.Refuse(fmt.Errorf("this credential type has no input %q, so nothing was removed", id))
	}
	schema.Fields = kept

	required := make([]string, 0, len(schema.Required))
	for _, name := range schema.Required {
		if name != id {
			required = append(required, name)
		}
	}
	schema.Required = required
	return nil
}

// removeInjector drops one entry from the injector document, by the same
// prefixed row id injectorRows built.
//
// An env entry also leaves omit_empty, which is not tidying: omit_empty
// names environment variables this same document sets, and credtype refuses
// a document whose omit_empty names one it does not. Leaving the name
// behind would make every removal of a listed env injector fail validation
// with a message about omit_empty, which names neither the button that was
// pressed nor the thing that was wrong.
func removeInjector(inj *credtype.Injectors, rowID string) error {
	target, name, found := strings.Cut(rowID, "-")
	if !found {
		return view.Refuse(fmt.Errorf("%q does not name an injector on this credential type", rowID))
	}

	switch target {
	case "env":
		if _, ok := inj.Env[name]; !ok {
			return missingInjector(rowID)
		}
		delete(inj.Env, name)
		kept := make([]string, 0, len(inj.OmitEmpty))
		for _, listed := range inj.OmitEmpty {
			if listed != name {
				kept = append(kept, listed)
			}
		}
		inj.OmitEmpty = kept
	case "extra":
		if _, ok := inj.ExtraVars[name]; !ok {
			return missingInjector(rowID)
		}
		delete(inj.ExtraVars, name)
	case "file":
		if _, ok := inj.File[name]; !ok {
			return missingInjector(rowID)
		}
		delete(inj.File, name)
	default:
		return missingInjector(rowID)
	}
	return nil
}

// missingInjector is the refusal for a row id naming nothing, kept in one
// place so all four branches say the same thing.
func missingInjector(rowID string) error {
	return view.Refuse(fmt.Errorf("this credential type has no injector %q, so nothing was removed", rowID))
}
