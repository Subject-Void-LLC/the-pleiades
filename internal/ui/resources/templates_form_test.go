package resources_test

import (
	"net/http"
	"strings"
	"testing"
)

// This file covers the Templates CREATE form, the authoring half the
// launch-form file (templates_test.go) does not touch.
//
// The defining property of a template form is that you choose what to run
// from what the platform knows exists: AWX's own form auto-populates its
// Playbook field from a scan of the project base path. This form shipped
// inverted, with a free-text control asking for "the runbook id or
// playbook path", a select asking the operator to declare the kind, and no
// validation that either named anything, so a typo saved fine and failed
// three stages later as a failed job. These tests pin the corrected shape.

func TestTemplatesView_TheCreateFormOffersTheCatalogRatherThanAskingForIt(t *testing.T) {
	h := newHarness(t, adminIdentity)

	w := h.get(t, "/ui/templates/new")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /ui/templates/new = %d, want 200", w.Code)
	}
	body := w.Body.String()

	// One picker over both kinds, each option labelled with what it is.
	// The value carries the kind, so an inconsistent pair (kind runbook,
	// definition site) is not a submittable state and no separate KIND
	// control exists to answer.
	for _, option := range []string{`value="runbook:conformance"`, `value="playbook:tripplite_python/tripplite_config.yml"`, "conformance (Runbook)", "tripplite_python/tripplite_config.yml (Playbook)"} {
		if !strings.Contains(body, option) {
			t.Errorf("the create form does not offer %s", option)
		}
	}
	if strings.Contains(body, `name="kind"`) {
		t.Error("the create form asks for the kind, which is the router's job pushed onto the operator: it is derived from what they chose to run")
	}
}

func TestTemplatesView_WhatRunsIsChosenOnceAndNeverEdited(t *testing.T) {
	h := newHarness(t, adminIdentity)

	w := h.get(t, "/ui/templates/1/edit")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /ui/templates/1/edit = %d, want 200", w.Code)
	}
	if strings.Contains(w.Body.String(), `name="definition"`) {
		t.Error("the edit form offers the definition, but re-pointing a saved definition at different code is a copy, not an edit")
	}
}

func TestTemplatesView_RefusesADefinitionTheCatalogNeverOffered(t *testing.T) {
	h := newHarness(t, adminIdentity)

	// A well-formed value naming a runbook that does not exist. Under the
	// free-text control this saved with a redirect and produced a template
	// whose every launch would fail at fan-out; now it is refused at the
	// control, because the option set is the catalog.
	w := h.post(t, "/ui/templates", map[string]string{
		"name":       "ghost",
		"definition": "runbook:no-such-runbook",
		"inventory":  "1",
	})
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("POST with an uncatalogued definition = %d, want 422: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "not a valid choice") {
		t.Errorf("the refusal does not point at the definition control")
	}
}
