// Package rollback: holding a journaled undo to what its method declares in
// this build, before anything compares the declarations it was recorded
// under (skew.go).
package rollback

import (
	"fmt"
	"slices"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/fragment"
	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// checkRecorded holds a journaled undo to what its method declares,
// rather than trusting the journal: a file an operator's account can
// write, or a row a Runner published. The identity it records must be one
// a build writes (checkIdentity); the method must be a built-in one this
// build knows, run by no external program, declaring an undo through the
// recorded method; every recorded value must sit under a key it declares
// recordable and the target reads, be a value the journal's own projection
// could have written, and be of the type the target declares. It returns
// the declaration and the target's manifest the undo was held to. No flag
// relaxes any of it: --allow-skew relaxes only judgeSkew's comparison,
// which runs after.
func checkRecorded(e engine.JournalEntry) (*sdk.InverseSpec, collection.Manifest, error) {
	if err := checkIdentity(e.FQCN, e.Collection, e.ProviderProgram); err != nil {
		return nil, collection.Manifest{}, err
	}
	// An external Collection's undo is never replayed, whatever the name
	// reads as here: a Controller judges by its own catalog, and a method
	// one build ran from a program could be built in to another
	// (FAILURE_PATTERNS 841's review). The Runner holds every step again.
	if e.ProviderProgram != "" {
		return nil, collection.Manifest{}, fmt.Errorf("the journal records %s as run by the external program %q, whose recorded undo is never replayed", e.FQCN, e.ProviderProgram)
	}
	at := versions(e.Collection)
	spec, target, err := declaredUndo(e.FQCN, e.InverseFQCN)
	if err != nil {
		return nil, collection.Manifest{}, fmt.Errorf("%w%s", err, at)
	}
	if e.InversePartial && !spec.MayBePartial {
		return nil, collection.Manifest{}, fmt.Errorf("the journal marks %s's undo through %s partial, which %s never declares it can be, so it is not trusted%s", e.FQCN, e.InverseFQCN, e.FQCN, at)
	}
	declared := fragment.ByNameIn(target)
	seen := map[string]bool{}
	for _, p := range e.InverseParams {
		param, isDeclared := declared[p.Key]
		switch {
		case seen[p.Key]:
			return nil, collection.Manifest{}, fmt.Errorf("the journal records undo parameter %q twice", p.Key)
		case collection.IsReservedParam(p.Key) || !slices.Contains(spec.Record, p.Key) || !isDeclared:
			return nil, collection.Manifest{}, fmt.Errorf("the journal records a value for undo parameter %q, which %s does not declare recordable for %s, so it is not trusted%s", p.Key, e.FQCN, e.InverseFQCN, at)
		}
		seen[p.Key] = true
		if err := checkValue(p, engine.ListParam(e.InverseFQCN, p.Key)); err != nil {
			return nil, collection.Manifest{}, fmt.Errorf("undo parameter %q: %w%s", p.Key, err, at)
		}
		value, _ := p.Value()
		if err := checkKind(p.Key, value, param, e.InverseFQCN); err != nil {
			return nil, collection.Manifest{}, fmt.Errorf("%w%s", err, at)
		}
	}
	return spec, target, nil
}

// checkValue holds one recorded value to the projection's own rules: a
// list only for a parameter the undo method declares one (asList).
func checkValue(p engine.InverseParam, asList bool) error {
	set := 0
	for _, isSet := range []bool{p.Text != nil, p.Number != nil, p.Bool != nil, p.Texts != nil} {
		if isSet {
			set++
		}
	}
	if set != 1 {
		return fmt.Errorf("holds %d values, want one", set)
	}
	if p.Text != nil && !engine.RecordableText(*p.Text) {
		return fmt.Errorf("holds text the journal would never record")
	}
	if p.Texts != nil {
		if _, ok := engine.RecordableTexts(p.Texts); !ok || !asList {
			return fmt.Errorf("holds a list the journal would never record")
		}
	}
	if _, ok := p.Value(); !ok {
		return fmt.Errorf("holds a number that is not one")
	}
	return nil
}

// checkIdentity refuses a recorded identity no build's projection writes:
// one that fails Shape (never Valid; skew.go's package text says why), a program's
// identity on an entry no program ran, and a first-party group's on one a
// program ran. The rule is the projection's, which records a program's
// identity only for a method a program provided, so it holds across
// releases. program is the entry's ProviderProgram.
func checkIdentity(fqcn string, id wire.CollectionIdentity, program string) error {
	if id.IsZero() {
		return nil
	}
	if err := id.Shape(); err != nil {
		return fmt.Errorf("the journal records a Collection identity for %s that no build writes (%w), so its undo is not trusted", fqcn, err)
	}
	switch {
	case id.Program && program == "":
		return fmt.Errorf("the journal records %s's Collection as the external program %q and names no program that ran it, which no build writes, so its undo is not trusted", fqcn, id.Group)
	case !id.Program && program != "":
		return fmt.Errorf("the journal records %s as run by the external program %q under the first-party %q Collection's identity, which no build writes, so its undo is not trusted", fqcn, program, id.Group)
	}
	return nil
}

// checkKind refuses a recorded value of another type than this build's
// undo method declares for its parameter (collection.ParamFits, the rule
// validation and the engine hold every value to): replaying it would hand
// the method a value its author never meant, and no flag accepts that.
func checkKind(key string, value any, param collection.Param, method string) error {
	fits, err := collection.ParamFits(param.Type, value)
	switch {
	case err != nil:
		return fmt.Errorf("undo parameter %q: %s declares it of a type this build does not know: %w", key, method, err)
	case !fits:
		return fmt.Errorf("the journal records undo parameter %q as %s, and %s declares it %s, so it is not trusted", key, collection.ValueKind(value), method, param.Type)
	}
	return nil
}
