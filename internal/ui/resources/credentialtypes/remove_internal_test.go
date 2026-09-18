// This file covers the two document edits the row controls perform, which
// the router-level tests reach only through whatever the shared fixture
// happens to contain.
//
// The omit_empty rule below is the reason this file exists at all. It is not
// visible from the page, it is not exercised by any fixture, and getting it
// wrong produces a refusal naming a field the operator never touched.
package credentialtypes

import (
	"errors"
	"fmt"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// TestRemoveInjector_TakesAnEnvNameOutOfOmitEmptyToo is the coupling inside
// the injector document that a removal has to respect.
//
// omit_empty names environment variables this same document sets, and
// credtype refuses a document whose omit_empty names one it does not. A
// removal that dropped the env entry alone would therefore be refused by
// validation, with a message about omit_empty: a rule the operator never
// touched, naming neither the button they pressed nor the row it was on.
func TestRemoveInjector_TakesAnEnvNameOutOfOmitEmptyToo(t *testing.T) {
	inj := credtype.Injectors{
		Env:       map[string]string{"API_TOKEN": "{{ api_token }}", "API_URL": "{{ api_url }}"},
		OmitEmpty: []string{"API_TOKEN", "API_URL"},
	}

	if err := removeInjector(&inj, "env-API_TOKEN"); err != nil {
		t.Fatalf("removeInjector() = %v, want nil", err)
	}

	if _, still := inj.Env["API_TOKEN"]; still {
		t.Error("the env entry survived its own removal")
	}
	for _, name := range inj.OmitEmpty {
		if name == "API_TOKEN" {
			t.Error("omit_empty still names the removed variable, so the document no longer validates")
		}
	}
	// The other one is untouched, or this is a document being cleared
	// rather than an entry being taken out of it.
	if _, ok := inj.Env["API_URL"]; !ok {
		t.Error("removing one env injector took another with it")
	}
	if len(inj.OmitEmpty) != 1 || inj.OmitEmpty[0] != "API_URL" {
		t.Errorf("OmitEmpty = %v, want just API_URL", inj.OmitEmpty)
	}
}

// TestRemoveInjector_ReadsTheRowIDTheSectionBuilt pins the two halves of the
// row id against each other.
//
// injectorRows invents these ids ("env-NAME", "extra-KEY", "file-KEY") and
// this is the only thing that reads them back, so the two are a private
// contract with no type between them. The file case is the one worth having
// a test for: its key is credtype's "template.<label>" spelling, which
// contains a dot and no hyphen, and it has to survive being cut at the
// first hyphen.
func TestRemoveInjector_ReadsTheRowIDTheSectionBuilt(t *testing.T) {
	cases := []struct {
		name  string
		rowID string
		check func(*testing.T, credtype.Injectors)
	}{
		{
			name:  "an environment variable",
			rowID: "env-API_TOKEN",
			check: func(t *testing.T, inj credtype.Injectors) {
				if _, ok := inj.Env["API_TOKEN"]; ok {
					t.Error("the env entry survived")
				}
			},
		},
		{
			name:  "an extra variable whose key contains a hyphen",
			rowID: "extra-tower-region",
			check: func(t *testing.T, inj credtype.Injectors) {
				if _, ok := inj.ExtraVars["tower-region"]; ok {
					t.Error("the extra-var entry survived, so the id was cut at the wrong hyphen")
				}
			},
		},
		{
			name:  "a generated file in the multi-file spelling",
			rowID: "file-template.cert",
			check: func(t *testing.T, inj credtype.Injectors) {
				if _, ok := inj.File["template.cert"]; ok {
					t.Error("the file entry survived")
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			inj := credtype.Injectors{
				Env:       map[string]string{"API_TOKEN": "{{ api_token }}"},
				ExtraVars: map[string]any{"tower-region": "{{ region }}"},
				File:      map[string]string{"template.cert": "{{ cert }}"},
			}
			if err := removeInjector(&inj, tc.rowID); err != nil {
				t.Fatalf("removeInjector(%q) = %v, want nil", tc.rowID, err)
			}
			tc.check(t, inj)
		})
	}
}

// TestRemoveInput_DropsTheRequiredEntryWithTheField is the same coupling one
// document over.
//
// Required is a list of input ids, and credtype refuses a schema requiring
// an input it does not declare. A removal that took the field and left the
// requirement would be refused by validation for a reason the operator did
// not cause and cannot see.
func TestRemoveInput_DropsTheRequiredEntryWithTheField(t *testing.T) {
	schema := credtype.InputSchema{
		Fields: []credtype.InputField{
			{ID: "api_token", Label: "Token", Secret: true},
			{ID: "api_url", Label: "URL"},
		},
		Required: []string{"api_token", "api_url"},
	}

	if err := removeInput(&schema, "api_token"); err != nil {
		t.Fatalf("removeInput() = %v, want nil", err)
	}

	if len(schema.Fields) != 1 || schema.Fields[0].ID != "api_url" {
		t.Errorf("Fields = %v, want just api_url", schema.Fields)
	}
	if len(schema.Required) != 1 || schema.Required[0] != "api_url" {
		t.Errorf("Required = %v, want just api_url: a requirement outlived its field", schema.Required)
	}
}

// TestRemoveInput_RefusesAnIDTheSchemaDoesNotHave keeps a stale page from
// reading as a success.
//
// A no-op that redirected would render the tab again with the row absent,
// which is exactly what a successful removal looks like. On a page left open
// while somebody else edited the type, the operator would conclude their
// click did something.
func TestRemoveInput_RefusesAnIDTheSchemaDoesNotHave(t *testing.T) {
	schema := credtype.InputSchema{Fields: []credtype.InputField{{ID: "api_url", Label: "URL"}}}

	err := removeInput(&schema, "never_existed")
	var refused view.Refused
	if !errors.As(err, &refused) {
		t.Fatalf("removeInput() = %v, want a view.Refused", err)
	}
	if len(schema.Fields) != 1 {
		t.Error("a refused removal changed the schema anyway")
	}
}

// TestRemovalFault_SeparatesARefusalFromAFault is the decision that keeps a
// database outage off a page addressed to the operator.
//
// The two must not be answered the same way in either direction. A rule they
// can satisfy shown as "internal error" hides the fix; a driver error shown
// as a refusal blames them for it and puts whatever the failure was holding
// on the page.
func TestRemovalFault_SeparatesARefusalFromAFault(t *testing.T) {
	refusals := []error{
		// The sentinel itself, and the sentinel wrapped the way the store
		// actually returns it, because errors.Is is what the decision
		// turns on and a bare equality check would pass the first.
		credtype.ErrInvalidType,
		fmt.Errorf("%w: an injector references it", credtype.ErrInvalidType),
		fmt.Errorf("%w: %q is shipped by this platform", credstore.ErrManaged, "aws"),
	}

	for _, err := range refusals {
		var refused view.Refused
		if !errors.As(removalFault(err), &refused) {
			t.Errorf("removalFault(%v) is not a refusal, so the operator is told nothing", err)
		}
	}

	fault := errors.New("dial tcp: connection refused")
	var refused view.Refused
	if errors.As(removalFault(fault), &refused) {
		t.Error("removalFault turned a connection failure into a message addressed to the operator")
	}
}
