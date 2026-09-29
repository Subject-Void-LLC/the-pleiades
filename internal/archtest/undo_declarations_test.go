// This file holds the registry-wide rules for undo declarations
// (collection.Reversibility.Inverses and ReadOnly, Phase 40).
//
// Registration checks each declaration alone (collection.ValidateReversibility).
// What only the whole registry can answer lives here: that every undo a
// method declares calls a real, implemented method, that each parameter it
// records or withholds is one that method reads, that every reversible
// built-in declared its undo at all, and that a generic dispatcher lists
// every undo its concrete methods can emit. A declaration naming a
// parameter its target does not read would journal a value nothing uses;
// one missing a parameter the target does read would leave the undo
// unreplayable while looking declared.
package archtest

import (
	"slices"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/fragment"
	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/catalogdata"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// builtinMethods returns every implemented method compiled into this
// binary, by the catalog's own list, failing when it finds none.
func builtinMethods(t *testing.T) []collection.Descriptor {
	t.Helper()
	var out []collection.Descriptor
	for _, cfg := range catalogdata.Collections {
		desc, ok := collection.Lookup(cfg.Name)
		if !ok || desc.Manifest.Status != collection.StatusImplemented || desc.Provider != nil {
			continue
		}
		out = append(out, desc)
	}
	if len(out) == 0 {
		t.Fatal("no implemented built-in method was found, so this test proved nothing")
	}
	return out
}

// TestEveryReversibleBuiltinDeclaresItsUndo: a reversible built-in with no
// Inverses is journaled by key name only, so no rollback could replay it,
// which is a declaration nobody meant.
func TestEveryReversibleBuiltinDeclaresItsUndo(t *testing.T) {
	var reversible int
	for _, desc := range builtinMethods(t) {
		if !desc.Manifest.Reversibility.Reversible {
			continue
		}
		reversible++
		if len(desc.Manifest.Reversibility.Inverses) == 0 {
			t.Errorf("%s is reversible and declares no undo method (Reversibility.Inverses), so no rollback can replay it", desc.Name)
		}
	}
	if reversible == 0 {
		t.Fatal("no reversible method was examined")
	}
	t.Logf("%d reversible built-in method(s) declare their undo", reversible)
}

// TestEveryUndoDeclarationNamesARealMethodAndItsParams holds each
// declaration to its target: registered, implemented, and reading every
// parameter the declaration records or withholds.
func TestEveryUndoDeclarationNamesARealMethodAndItsParams(t *testing.T) {
	var specs int
	for _, desc := range builtinMethods(t) {
		for _, spec := range desc.Manifest.Reversibility.Inverses {
			specs++
			target, ok := collection.Lookup(spec.FQCN)
			if !ok || target.Manifest.Status != collection.StatusImplemented {
				t.Errorf("%s's undo calls %s, which is not an implemented method", desc.Name, spec.FQCN)
				continue
			}
			declared := fragment.Declared(target)
			for _, name := range append(slices.Clone(spec.Record), spec.Withhold...) {
				if !declared[name] {
					t.Errorf("%s's undo through %s names %q, which %s does not read", desc.Name, spec.FQCN, name, spec.FQCN)
				}
			}
		}
	}
	if specs == 0 {
		t.Fatal("no undo declaration was examined")
	}
}

// TestEveryGenericDispatcherDeclaresItsManagersUndos: pkg.install runs
// pkg.apt.install or pkg.dnf.install, whose undo it then emits, so its
// own declaration (which the journal reads, since the task's method is
// pkg.install) must cover every one of theirs. Found by naming: a method
// X.verb and its concrete siblings X.<manager>.verb.
func TestEveryGenericDispatcherDeclaresItsManagersUndos(t *testing.T) {
	methods := builtinMethods(t)
	byName := map[string]collection.Descriptor{}
	for _, d := range methods {
		byName[d.Name] = d
	}
	var generics int
	for _, generic := range methods {
		namespace, verb, ok := strings.Cut(generic.Name, ".")
		if !ok || strings.Contains(verb, ".") || !generic.Manifest.Reversibility.Reversible {
			continue
		}
		for name, concrete := range byName {
			parts := strings.Split(name, ".")
			if len(parts) != 3 || parts[0] != namespace || parts[2] != verb {
				continue
			}
			generics++
			for _, want := range concrete.Manifest.Reversibility.Inverses {
				if !declaresSpec(generic.Manifest.Reversibility.Inverses, want) {
					t.Errorf("%s dispatches to %s, whose undo through %s (recording %v) it does not declare", generic.Name, name, want.FQCN, want.Record)
				}
			}
		}
	}
	if generics == 0 {
		t.Fatal("no generic dispatcher was examined")
	}
}

// declaresSpec reports whether specs holds want's target with exactly its
// recorded and withheld parameters and its partial flag.
func declaresSpec(specs []sdk.InverseSpec, want sdk.InverseSpec) bool {
	for _, s := range specs {
		if s.FQCN != want.FQCN || s.MayBePartial != want.MayBePartial {
			continue
		}
		if sameSet(s.Record, want.Record) && sameSet(s.Withhold, want.Withhold) {
			return true
		}
	}
	return false
}

// sameSet reports whether a and b hold the same names, in any order.
func sameSet(a, b []string) bool {
	x, y := slices.Clone(a), slices.Clone(b)
	slices.Sort(x)
	slices.Sort(y)
	return slices.Equal(x, y)
}

// TestAMethodThatOnlyReadsSaysSo catches a read-only method that forgot
// ReadOnly: the catalog's convention is to open such a method's Notes with
// "Reading is not changing", and a method saying that in prose has to say
// it in the field a rollback and validation read.
func TestAMethodThatOnlyReadsSaysSo(t *testing.T) {
	var readOnly int
	for _, desc := range builtinMethods(t) {
		r := desc.Manifest.Reversibility
		if r.ReadOnly {
			readOnly++
		}
		if strings.HasPrefix(r.Notes, "Reading is not changing") && !r.ReadOnly {
			t.Errorf("%s's notes say it only reads, and it does not declare ReadOnly", desc.Name)
		}
	}
	if readOnly == 0 {
		t.Fatal("no read-only method was found")
	}
}
