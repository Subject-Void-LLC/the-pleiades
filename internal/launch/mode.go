// Package launch: the launch field that makes a run a check, and how it
// resolves.
package launch

import (
	"errors"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// ModeField is the launch field that says whether a run changes anything:
// "execute" runs it for real, "check" asks every task what it would change
// and changes nothing (collection.ModeCheck). A kind that can run as a
// check declares it as a TypeChoice field; one that cannot, does not.
const ModeField = "mode"

// ErrMode is every refusal of a run's mode: a value that is not a mode, a
// request that would turn a check back into a real run, and a check of a
// kind that cannot run as one. Each is refused outright rather than
// reported as ignored, because every one of them, ignored, would run
// something for real that somebody asked only to check.
var ErrMode = errors.New("launch mode refused")

// modeLayer is one layer's say on the mode, in resolution order.
type modeLayer struct {
	name   string
	fields Fields
}

// resolveMode settles a run's mode from every layer that set one, least
// specific first: the template's defaults, a saved configuration (a
// schedule, a workflow node, a relaunch) and this launch.
//
// It does not use the generic fold, because the generic rule (the most
// specific layer wins, and a field the template did not open is ignored)
// is exactly wrong for this field. A check NARROWS: at any layer it wins,
// and no layer needs the template's permission to ask for one, since it
// can only make a run change less. A real run beneath a check is refused
// rather than applied, and a value that is not a mode is refused rather
// than ignored, because ignoring either falls back to a real run. This is
// the one stated exception to the most-specific-wins rule for job
// settings, decided 2026-09-18.
//
// The device's own simulate lock still applies beneath all of this: a
// real run never reaches a simulate-locked device whatever the mode says
// (engine.LifecycleAdmitsIn).
func resolveMode(d Descriptor, layers []modeLayer) (collection.Mode, error) {
	_, accepted := d.Field(ModeField)
	mode := collection.ModeExecute
	checkedBy := ""
	for _, l := range layers {
		value, set := l.fields[ModeField]
		if !set {
			continue
		}
		s, ok := value.(string)
		if !ok || (s != string(collection.ModeExecute) && s != string(collection.ModeCheck)) {
			return "", fmt.Errorf("%w: the %s asks for mode %v; a mode is %q or %q", ErrMode, l.name, value, collection.ModeExecute, collection.ModeCheck)
		}
		switch collection.Mode(s) {
		case collection.ModeCheck:
			if mode != collection.ModeCheck {
				mode, checkedBy = collection.ModeCheck, l.name
			}
		case collection.ModeExecute:
			if mode == collection.ModeCheck {
				return "", fmt.Errorf("%w: the %s asks for a real run, but the %s makes this a check, and a check can be asked for at any level but never turned back into a real run",
					ErrMode, l.name, checkedBy)
			}
		}
	}
	if mode == collection.ModeCheck && !accepted {
		return "", fmt.Errorf("%w: a %s run cannot be a check (the %s asked for one)", ErrMode, d.Label, checkedBy)
	}
	return mode, nil
}

// CheckSavedMode refuses a saved configuration (a schedule's, a workflow
// node's) whose mode could never launch against this template, by the
// same rule a launch is resolved with (resolveMode): a value that is not
// a mode, a real run beneath the template's check, or a check of a kind
// that cannot run as one. Checked when the configuration is saved, so a
// schedule finds out then, not every time it fires.
func (t Template) CheckSavedMode(saved Fields) error {
	d, err := t.Descriptor()
	if err != nil {
		return err
	}
	_, err = resolveMode(d, []modeLayer{
		{name: LayerTemplate, fields: t.Defaults},
		{name: LayerSaved, fields: saved},
	})
	return err
}

// withoutMode returns f without ModeField, for the generic fold, which
// must never see it.
func withoutMode(f Fields) Fields {
	if !f.Has(ModeField) {
		return f
	}
	out := make(Fields, len(f))
	for k, v := range f {
		if k != ModeField {
			out[k] = v
		}
	}
	return out
}
