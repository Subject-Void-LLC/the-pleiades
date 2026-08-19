package file_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/file"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// file.template is DECLARED, not implemented, and these tests pin the
// three things that make that honest rather than merely unfinished: the
// registry says declared, nothing is wired for the dispatcher to call,
// and a direct call returns an error that names what is actually
// blocking it.
//
// There is no SSH harness here because there is nothing to run against.
// Adding one would only prove the harness works.
//
// Every identifier is prefixed "template" because the sibling methods in
// this package share one test package and Go has no file-level scope.

// TestTemplate_Registered proves the method is registered as declared,
// and that the declaration is internally consistent.
//
// Invoke being nil is the assertion that matters alongside the status.
// pkg/collection.Register refuses an implemented method with no
// implementation, but nothing refuses the reverse, and a declared
// manifest carrying a function would claim a dispatchable method that its
// own Status contradicts.
func TestTemplate_Registered(t *testing.T) {
	d, ok := collection.Lookup("file.template")
	if !ok {
		t.Fatalf("collection.Lookup(%q) found nothing; did this package's init() run?", "file.template")
	}
	if d.Manifest.Status != collection.StatusDeclared {
		t.Errorf("Manifest.Status = %v, want %v: nothing here renders a template yet", d.Manifest.Status, collection.StatusDeclared)
	}
	if d.Invoke != nil {
		t.Error("Invoke is set on a declared method, so the manifest claims something dispatchable that its Status denies")
	}
	if d.Manifest.ExecutionContext.RequiresElevation {
		t.Error("ExecutionContext.RequiresElevation = true, want false: writing a file needs whatever the target path needs, not root unconditionally")
	}
}

// TestTemplate_CarriesASummaryAndNothingElse pins pkg/collection.Doc's
// convention for a declared method.
//
// A Param or Return table here would describe an argument spec nobody has
// written, and an Example would be a paste-ready runbook task that cannot
// run: both read as documentation of working behavior. The Summary is the
// one thing a namespace index legitimately needs from a method that does
// not exist yet.
func TestTemplate_CarriesASummaryAndNothingElse(t *testing.T) {
	d, ok := collection.Lookup("file.template")
	if !ok {
		t.Fatalf("collection.Lookup(%q) found nothing", "file.template")
	}
	if d.Manifest.Doc.Summary == "" {
		t.Error("Doc.Summary is empty, so a namespace index has nothing to show beside the FQCN")
	}
	if n := len(d.Manifest.Doc.Params); n != 0 {
		t.Errorf("Doc.Params has %d entr(ies), want none: a declared method has no argument spec to document", n)
	}
	if n := len(d.Manifest.Doc.Returns); n != 0 {
		t.Errorf("Doc.Returns has %d entr(ies), want none: nothing emits anything yet", n)
	}
	if n := len(d.Manifest.Doc.Examples); n != 0 {
		t.Errorf("Doc.Examples has %d entr(ies), want none: an example here would be a task that cannot run", n)
	}
}

// TestTemplate_DeclaredButBlocked is the "declared is not implemented"
// guardrail: the stub must return an explicit error, never success.
//
// The message's content is asserted, not just its existence. "Not
// implemented" alone sends a reader looking for unwritten logic, when the
// real obstacle is a layering rule: the only renderer this platform has
// lives under internal/, and a Collection package may not import it. A
// message that lost that would turn a decision into an oversight.
func TestTemplate_DeclaredButBlocked(t *testing.T) {
	tests := []struct {
		name   string
		params map[string]any
	}{
		{name: "no params", params: nil},
		{name: "a fully formed task", params: map[string]any{"dest": "/etc/app.conf", "src": "app.conf.j2", "mode": "0644"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := file.Template(context.Background(), nil, nil, tt.params)
			if err == nil {
				t.Fatal("expected an explicit not-implemented error, got nil")
			}
			if result.Changed {
				t.Error("a method that did nothing reported a change")
			}
			for _, phrase := range []string{
				"declared but not implemented",
				"internal/render",
				"may not import",
				"file.copy",
			} {
				if !strings.Contains(err.Error(), phrase) {
					t.Errorf("error = %q, want it to mention %q", err.Error(), phrase)
				}
			}
		})
	}
}
