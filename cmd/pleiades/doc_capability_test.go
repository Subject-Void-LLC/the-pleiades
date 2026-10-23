// Tests that pleiades doc reads a loaded program's capability by its name,
// and names it beside a method that requires it.
package main

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// TestDoc_RendersAProgramsCapability: the definition is printed by name,
// with how a device comes to hold it, and a method requiring it points
// there.
func TestDoc_RendersAProgramsCapability(t *testing.T) {
	def := capability.DataDefinition{
		Name: "doctest.widget", Provider: "doctest", Doc: "A widget this account may turn.",
		Requires:   []capability.Name{capability.NameSSHTransport},
		Properties: []capability.DataProperty{{Name: "model", Type: "string", Required: true, Description: "The widget model."}},
		Probe:      "doctest.widget.probe",
	}
	if err := capability.RegisterData(def); err != nil {
		t.Fatal(err)
	}
	var runErr error
	out := captureStdout(t, func() { runErr = runDoc([]string{"doctest.widget"}) })
	if runErr != nil {
		t.Fatal(runErr)
	}
	for _, want := range []string{"doctest.widget\n==============", "A widget this account may turn.", "defined by: doctest", "SSHTransportCapable", "its probe doctest.widget.probe", "pleiades grant-capability <device> doctest.widget", "model", "string, required"} {
		if !strings.Contains(out, want) {
			t.Errorf("doc printed no %q:\n%s", want, out)
		}
	}
	entries := []catalogEntry{{FQCN: "doctest.widget.turn", Origin: "external Collection doctest", Manifest: collection.Manifest{
		Status: collection.StatusImplemented, RequiredCapabilities: []capability.Name{"doctest.widget"},
	}}}
	out = captureStdout(t, func() { runErr = printDocEntry(entries, "doctest.widget.turn") })
	if runErr != nil || !strings.Contains(out, "doctest.widget is defined by doctest, not by The Pleiades: pleiades doc doctest.widget") {
		t.Errorf("a method requiring it: %v\n%s", runErr, out)
	}
	if err := runDoc([]string{"doctest.nothing"}); err == nil || !strings.Contains(err.Error(), "unknown fqcn") {
		t.Errorf("an unknown name: %v", err)
	}
}
