package viewscaffold

// viewTemplateSource is the generated view resource.
//
// It emits a StatusDeclared descriptor, deliberately. A generated view that
// claimed to be implemented would be a view whose handlers are stubs, and a
// stub that silently succeeds is indistinguishable from a working
// implementation with nothing to do -- the failure this repository has
// shipped twice and now refuses at registration. Declared is the honest
// starting state, and the shared template renders a panel that says so.
//
// The commented-out block is the whole implementation path, written out
// rather than described, because the moment somebody needs it is the moment
// they are least inclined to go and read how another resource did it.
const viewTemplateSource = `// Package {{.PackageName}} is the {{.Title}} view resource.
//
// This is what a view costs: a field declaration, an adapter over a port
// that already exists, a projector, and a registration. No handler, no
// template, no route, no nav entry, no CSS, no accessibility work -- all of
// that is shared, and the conformance suite in internal/ui/resources will
// hold this view to the same standard as every other one the moment it is
// registered.
//
// It is reachable only because internal/ui/resources/registrars.go names
// it. An entry point nothing calls never runs, and a package that compiles
// and passes its own tests while being invisible to the running binary is
// the failure recorded as FAILURE_PATTERNS.md #52.
package {{.PackageName}}

import (
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// Name is this view's registration key and URL segment.
const Name = {{quote .Name}}

// fields is the one declaration driving the table, the form, the detail
// list, server-side validation, and the mobile card layout.
//
// Every consumer reads this slice, so a column added here appears in all
// six places at once -- and a label left blank is refused at process start
// rather than shipping as a table header nothing announces.
var fields = []view.Field{
	{
		Name: "name", Label: "NAME", Kind: view.KindText,
		Required: true, MaxLen: 253, Autocomplete: "off",
		Help:          "Replace this with the field that identifies a record.",
		InList:        true,
		InForm:        true,
		MobilePrimary: true,
	},
}

// Register adds the {{.Title}} view.
//
// It is registered declared: the shape is real, and nothing backs it yet.
// The shared template renders an honest "declared, not implemented" panel
// from Status alone, so there is nothing to write for that state.
//
// To implement it:
//
//  1. Give this function the port it needs as a parameter, and pass it
//     through from internal/ui/resources/registrars.go.
//  2. Write a reader adapting that port to view.Reader, and -- only if the
//     resource is genuinely writable -- a writer for view.Writer, or a
//     creator for view.Creator when records are made but never edited.
//  3. Write a view.Projector with Row (and Form and Bind if writable).
//  4. Set Status to view.StatusImplemented, name the apispec endpoints in
//     Ops, and set Handlers to view.MustBind(reader, writer, projector).
//
// Register will refuse any half-finished combination of those, which is the
// point: a view cannot claim to be implemented while carrying no handlers,
// and cannot list records while naming no endpoint to open one with.
func Register() error {
	return view.Register(view.Descriptor{
		Name:     Name,
		Title:    {{quote .Title}},
		NavLabel: {{quote .ResolvedNavLabel}},
		NavOrder: {{.NavOrder}},
		Summary:  {{quote .Summary}},
		Status:   view.StatusDeclared,
		IDField:  "name",
		Fields:   fields,
		// No Ops while declared. Naming an endpoint that does not exist is
		// refused at registration, and naming one that does would advertise
		// an operation nothing here serves.
	})
}
`

// viewTestTemplateSource is the generated starter test.
//
// It asserts the two things that are true of a declared view and would
// otherwise be discovered much later: that it registers at all, and that it
// does not secretly claim to work. The conformance suite covers everything
// else once the view is wired in, which is exactly why this file is short --
// a generated test that restated the shared assertions would be a second
// copy of them to keep in agreement.
const viewTestTemplateSource = `package {{.PackageName}}_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/resources/{{.PackageName}}"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// TestRegister proves the descriptor satisfies every rule view.Register
// enforces, and that it is honest about not being implemented yet.
//
// Both assertions live in one test function because the registry refuses a
// duplicate name by design, so Register can only be called once per process.
// Splitting them would mean the second call failing and being tolerated by
// some "already registered?" helper -- and a helper that swallows one error
// swallows the rest of them too.
//
// The rules being checked are not ceremony: a blank label is a table header
// nothing announces, an invented autocomplete token is silently useless to
// assistive technology, and an endpoint that does not exist is a button
// rendered for a route nobody mounted.
func TestRegister(t *testing.T) {
	if err := {{.PackageName}}.Register(); err != nil {
		t.Fatalf("Register() = %v, want nil", err)
	}

	d, ok := view.Lookup({{.PackageName}}.Name)
	if !ok {
		t.Fatalf("view %q is not in the registry after Register()", {{.PackageName}}.Name)
	}

	if d.Title == "" || d.NavLabel == "" {
		t.Error("the descriptor has no title or no nav label, so it has no accessible name")
	}

	// Delete this half when the view is implemented -- and only then. The
	// real coverage arrives at that point anyway: once this view is named
	// in internal/ui/resources/registrars.go, the conformance suite there
	// drives it through the same assertions as every other view, including
	// the accessibility gate.
	if d.Implemented() {
		t.Error("this view claims to be implemented; if that is now true, delete this check")
	}
	if d.Handlers != nil {
		t.Error("a declared view carries handlers, so it is not declared")
	}
}
`

// reminderSource is what the CLI prints last, because it is the one step no
// generator can perform and the one whose omission is silent.
const reminderSource = `
Generated %s.

One step remains, and nothing will fail without it:

    Add this view to internal/ui/resources/registrars.go

        func(Deps) error { return %s.Register() },

    and import %q.

A view package that nothing imports registers nothing. It compiles, its
tests pass, and it is invisible to the running binary -- no nav entry, no
route, no error. That exact failure is recorded in this repository twice
(FAILURE_PATTERNS.md #52, LESSONS_LEARNED.md #56), which is why this
reminder is the last thing printed rather than a line in a document.
`
