// Package auditee is two fact-declaring built-ins whose test binary
// pkg/collection/facttest's own tests build and run, to read what
// facttest.Main does with a whole test binary: its exit code and its report.
// It lives under testdata, so no ./... pattern builds it, and no binary of
// the module links it.
package auditee

import (
	"context"
	"sort"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// The two methods, in the core facts group so facttest.Builtins finds them.
const (
	Alpha = "facts.auditee_alpha"
	Beta  = "facts.auditee_beta"
)

func init() {
	register(Alpha, invokeAlpha,
		collection.FactSpec{Key: "auditee.name", Kind: collection.FactStable, Type: collection.FactString, Description: "a name"},
		collection.FactSpec{Key: "auditee.count", Kind: collection.FactVolatile, Type: collection.FactInteger, Description: "a count"})
	register(Beta, invokeBeta,
		collection.FactSpec{Key: "auditee.flag", Kind: collection.FactStable, Type: collection.FactBoolean, Description: "a flag"})
}

// register registers a read-only built-in named name, run by invoke and
// declaring specs.
func register(name string, invoke collection.Method, specs ...collection.FactSpec) {
	collection.MustRegister(collection.Descriptor{Name: name, Invoke: invoke, Manifest: collection.Manifest{
		Status: collection.StatusImplemented, NoCheckReason: "a test fixture",
		Reversibility: collection.Reversibility{ReadOnly: true, Notes: "a test fixture"}, Facts: specs}})
}

// invokeAlpha is Alpha's Invoke, and invokeBeta Beta's: each emits every
// fact in params["emit"] through the run context, as a method would.
func invokeAlpha(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return emit(rc, params)
}

// invokeBeta is Beta's Invoke.
func invokeBeta(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return emit(rc, params)
}

// emit hands each fact in params["emit"] to rc, in key order.
func emit(rc sdk.RunbookContext, params map[string]any) (collection.Result, error) {
	facts, _ := params["emit"].(map[string]any)
	keys := make([]string, 0, len(facts))
	for k := range facts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if err := rc.EmitFact(k, facts[k]); err != nil {
			return collection.Result{}, err
		}
	}
	return collection.Result{}, nil
}
