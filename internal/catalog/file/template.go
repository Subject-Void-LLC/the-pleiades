package file

import (
	"context"
	"errors"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// This file holds "file.template", and it is DECLARED RATHER THAN
// IMPLEMENTED because of a layering fact, not because nobody got to it.
//
// # What it would be
//
// ansible.builtin.template is ansible.builtin.copy with the content run
// through a renderer first. That is the whole difference, and file.copy
// in this same package already does every other part of it: the checksum
// comparison that decides whether to write, the atomic write, the
// attribute pass, the diff, the emitted inverse. Implementing this method
// once a renderer is reachable is wrapping one call around all of that.
//
// # Why it cannot be written today
//
// This platform has exactly one template engine, internal/render, and a
// Collection package may import pkg/, the standard library and
// third-party modules only. internal/archtest enforces that as a real
// test rather than a convention, and the rule is not incidental: Part X
// intends a Collection to arrive from outside this binary, and anything
// it may import has to be importable from outside this module. So
// internal/render is unreachable from here, and there is no pkg/
// equivalent to reach instead.
//
// The two ways to make this method work today are both worse than
// leaving it declared. Writing a second template engine inside this
// package would give the platform two renderers that disagree about
// filters, whitespace and error messages, and a runbook's template would
// then behave differently depending on whether it was rendered for a
// credential injector or for this method. Rendering on the controller
// and shipping the result would work on the Walk tier and fail on the
// Crawl tier, where a method runs in a per-task container that has
// neither the runbook's directory nor the run's variables, which is the
// same reason file.copy refuses src.
//
// What unblocks it is promoting internal/render into pkg/. Until then
// Template below returns an error naming that dependency, which is what
// docs/hephaestus.md's "declared is not implemented" guardrail requires:
// an explicit refusal, never a silent success.

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: "file.template",
		Manifest: collection.Manifest{
			SupportedTransports: []string{
				"ssh",
			},
			RequiredCapabilities: []capability.Name{
				capability.NamePOSIXFileSystem,
			},
			ExecutionContext: collection.ExecutionContext{
				RequiresElevation: false,
			},
			PlatformTargets: nil,
			EngineVersion:   ">=1.0.0",
			Status:          collection.StatusDeclared,
			// Reversibility is deliberately left at its zero value, which
			// pkg/collection's registration check permits for a declared
			// method and refuses for an implemented one. There is no
			// behavior here to undo, and answering the question about code
			// nobody has written would produce a guess that reads like an
			// answer. It will say what file.copy's says, for the same
			// reasons, the day this gains an implementation.
			//
			// Invoke is nil for the same reason: a declared stub is
			// short-circuited by the dispatcher before anything is called,
			// and carrying a function here would make the manifest claim a
			// dispatchable method that the Status contradicts.
			Doc: templateDoc(),
		},
	})
}

// templateDoc is this method's reference documentation, kept out of the
// registration above so the manifest fields stay readable.
//
// It carries a Summary and nothing else, which is pkg/collection.Doc's
// stated convention for a declared method: there is no reachable behavior
// for Params or Returns to describe, and an Example here would be a
// paste-ready runbook task that cannot run. What is missing and why is
// said in this file's own comment and in the error Template returns, both
// of which reach a reader at the moment they need it.
//
// It is duplicated into internal/forge/catalogdata, which is the source
// the scaffolder is driven from, and internal/archtest's
// TestCatalogDataDocsMatchTheRegistry compares the two for equality so
// the copies cannot drift.
func templateDoc() collection.Doc {
	return collection.Doc{
		Summary: "Renders a template and writes the result to the target.",
	}
}

// Template implements the "file.template" collection method, and today
// returns an explicit declared-but-blocked error naming what it is
// waiting on.
//
// A runbook task naming this FQCN does not reach here: the dispatcher
// reads the Manifest's StatusDeclared and refuses first. This body is
// what a direct caller gets, and it exists so the refusal says something
// useful rather than panicking on a nil function.
//
// The error names the missing dependency by package, because "not
// implemented" alone sends a reader looking for unwritten logic when the
// real obstacle is that the renderer this method needs lives somewhere a
// Collection is not allowed to import from. It also names file.copy,
// since a runbook that only needs to place a file whose content the run
// already holds does not need a template at all.
func Template(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return collection.Result{}, errors.New(
		"file.template: declared but not implemented: it needs a template renderer, and the only one this platform has is internal/render, " +
			"which a Collection package may not import (a Collection may import pkg/, the standard library and third-party modules only, " +
			"enforced by internal/archtest). There is no pkg/ renderer to use instead, and writing a second engine here would give the " +
			"platform two that disagree. Promoting internal/render into pkg/ is what unblocks this. Until then use file.copy, which writes " +
			"content the runbook already holds")
}
