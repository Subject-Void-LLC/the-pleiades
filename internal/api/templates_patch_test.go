// Package api_test holds, in this file, PATCH /templates/{id} to what PATCH
// means: a partial update. Each test goes through the real router at the
// real apispec pattern over the real ent store on SQLite, because the defect
// it guards against (FAILURE_PATTERNS 1056) lived in how the handler turned
// a body into the template the store saves, and a test below the router
// would not see it.
package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// patchedTemplate is every editable field read back off the wire. Its own
// declaration, for the reason decodedTemplate gives: asserting against the
// handler's DTO would pass even if a JSON tag were wrong.
type patchedTemplate struct {
	Name                 string         `json:"name"`
	Kind                 string         `json:"kind"`
	Definition           string         `json:"definition"`
	Inventory            int            `json:"inventory"`
	Description          string         `json:"description"`
	Defaults             map[string]any `json:"defaults"`
	Prompts              []string       `json:"prompts"`
	AllowSimultaneous    bool           `json:"allow_simultaneous"`
	RequiredCapabilities []string       `json:"required_capabilities"`
	Survey               *struct {
		Enabled   bool `json:"enabled"`
		Questions []struct {
			Variable string `json:"variable"`
			Type     string `json:"type"`
			Required bool   `json:"required"`
		} `json:"questions"`
	} `json:"survey"`
}

// fullTemplateBody is a template with every editable field set away from its
// zero value, so a PATCH that blanks any of them is visible.
func (f *templateFixture) fullTemplateBody(name string) string {
	return fmt.Sprintf(`{
		"name": %q,
		"description": "rolls a patch onto the edge",
		"kind": "runbook",
		"definition": "patch-edge",
		"inventory": %d,
		"defaults": {"limit": "edge-*", "forks": 5},
		"prompts": ["limit", "forks"],
		"survey": {"enabled": true, "questions": [
			{"variable": "version", "label": "VERSION", "type": "text", "required": true},
			{"variable": "reason", "label": "REASON", "type": "textarea"}
		]},
		"allow_simultaneous": true,
		"required_capabilities": ["ssh", "apt"]
	}`, name, f.invA)
}

// readTemplate GETs one template and decodes every editable field.
func (f *templateFixture) readTemplate(t *testing.T, id int) patchedTemplate {
	t.Helper()
	rec := doJSON(t, f.router, http.MethodGet, "/api/v1/templates/"+strconv.Itoa(id), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("reading template %d: status = %d, want 200: %s", id, rec.Code, rec.Body.String())
	}
	var got patchedTemplate
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding template %d: %v", id, err)
	}
	return got
}

// patchTemplate PATCHes a template and returns the response.
func (f *templateFixture) patchTemplate(t *testing.T, id int, body string) (int, string) {
	t.Helper()
	rec := doJSON(t, f.router, http.MethodPatch, "/api/v1/templates/"+strconv.Itoa(id), body)
	return rec.Code, rec.Body.String()
}

// surveyVariables lists a read-back survey's variables in order, or nil when
// the response carried no survey.
func surveyVariables(tmpl patchedTemplate) []string {
	if tmpl.Survey == nil {
		return nil
	}
	out := make([]string, 0, len(tmpl.Survey.Questions))
	for _, q := range tmpl.Survey.Questions {
		out = append(out, q.Variable)
	}
	return out
}

// TestTemplateAPI_PatchKeepsEveryFieldItDoesNotName is the reproduction of
// FAILURE_PATTERNS 1056: a PATCH that names only one field used to write the
// zero value into every other one, deleting the survey's questions, the
// defaults, the prompts and the capability hint and turning
// allow_simultaneous off.
func TestTemplateAPI_PatchKeepsEveryFieldItDoesNotName(t *testing.T) {
	// Each case PATCHes one field and checks that every field it did not
	// name reads back exactly as it was created.
	cases := map[string]struct {
		body  string
		apply func(want *patchedTemplate)
	}{
		"only the name": {
			body:  `{"name": "renamed"}`,
			apply: func(want *patchedTemplate) { want.Name = "renamed" },
		},
		"only the description": {
			body:  `{"description": "a new purpose"}`,
			apply: func(want *patchedTemplate) { want.Description = "a new purpose" },
		},
		"only the defaults": {
			body:  `{"defaults": {"limit": "core-*"}}`,
			apply: func(want *patchedTemplate) { want.Defaults = map[string]any{"limit": "core-*"} },
		},
		"only the prompts": {
			body:  `{"prompts": ["limit"]}`,
			apply: func(want *patchedTemplate) { want.Prompts = []string{"limit"} },
		},
		"only allow_simultaneous": {
			body:  `{"allow_simultaneous": false}`,
			apply: func(want *patchedTemplate) { want.AllowSimultaneous = false },
		},
		"only the required capabilities": {
			body:  `{"required_capabilities": ["winrm"]}`,
			apply: func(want *patchedTemplate) { want.RequiredCapabilities = []string{"winrm"} },
		},
		"only the survey": {
			body: `{"survey": {"enabled": true, "questions": [{"variable": "ticket", "label": "TICKET", "type": "text"}]}}`,
			apply: func(want *patchedTemplate) {
				want.Survey.Questions = want.Survey.Questions[:1]
				want.Survey.Questions[0].Variable = "ticket"
				want.Survey.Questions[0].Required = false
			},
		},
		// The survey is two fields of its own, and turning it off is a real
		// edit: "a survey written and turned off" is a different state from
		// no survey, so naming only enabled must not delete the questions.
		"only the survey's enabled flag": {
			body:  `{"survey": {"enabled": false}}`,
			apply: func(want *patchedTemplate) { want.Survey.Enabled = false },
		},
		"only the survey's questions": {
			body: `{"survey": {"questions": [{"variable": "reason", "label": "REASON", "type": "textarea"}]}}`,
			apply: func(want *patchedTemplate) {
				want.Survey.Questions = want.Survey.Questions[1:]
			},
		},
		"an empty body": {
			body:  `{}`,
			apply: func(*patchedTemplate) {},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := newTemplateFixture(t)
			created := f.createTemplate(t, f.fullTemplateBody("patch"))
			want := f.readTemplate(t, created.ID)
			tc.apply(&want)

			status, body := f.patchTemplate(t, created.ID, tc.body)
			if status != http.StatusOK {
				t.Fatalf("PATCH %s: status = %d, want 200: %s", tc.body, status, body)
			}

			// Read back through GET rather than trusting the PATCH response,
			// so what is checked is what was stored.
			got := f.readTemplate(t, created.ID)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("after PATCH %s\n got  %+v\n want %+v\n survey got %v, want %v",
					tc.body, got, want, surveyVariables(got), surveyVariables(want))
			}
		})
	}
}

// TestTemplateAPI_PatchClearsAFieldSentEmpty holds the other half of partial:
// a field the body does name is applied, and an explicit empty value is how
// a caller clears one.
func TestTemplateAPI_PatchClearsAFieldSentEmpty(t *testing.T) {
	f := newTemplateFixture(t)
	created := f.createTemplate(t, f.fullTemplateBody("patch"))

	status, body := f.patchTemplate(t, created.ID, `{
		"description": "",
		"defaults": {},
		"prompts": [],
		"required_capabilities": [],
		"allow_simultaneous": false,
		"survey": {"enabled": false, "questions": []}
	}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", status, body)
	}

	got := f.readTemplate(t, created.ID)
	if got.Name != "patch" {
		t.Errorf("name = %q, want it kept, since the body did not name it", got.Name)
	}
	if got.Description != "" || len(got.Defaults) != 0 || len(got.Prompts) != 0 ||
		len(got.RequiredCapabilities) != 0 || got.AllowSimultaneous {
		t.Errorf("an explicit empty value did not clear its field: %+v", got)
	}
	if got.Survey == nil || got.Survey.Enabled || len(got.Survey.Questions) != 0 {
		t.Errorf("an explicit empty survey did not clear it: %+v", got.Survey)
	}
}

// TestTemplateAPI_PatchRefusesNull holds the null rule. A JSON null is
// refused for every field rather than read as "clear" or as "keep": under a
// partial update the first deletes data a client that serializes an unset
// value as null never meant to touch (a survey edited elsewhere, say), and
// the second silently keeps what a JSON Merge Patch client asked to remove,
// prompts among them, which decide what a launch may override. Refusing says
// which field, and an empty value or leaving the key out says the rest.
func TestTemplateAPI_PatchRefusesNull(t *testing.T) {
	fields := []string{
		"name", "description", "defaults", "prompts", "survey",
		"allow_simultaneous", "required_capabilities",
		"kind", "definition", "inventory",
	}
	for _, field := range fields {
		t.Run(field, func(t *testing.T) {
			f := newTemplateFixture(t)
			created := f.createTemplate(t, f.fullTemplateBody("patch"))
			before := f.readTemplate(t, created.ID)

			status, body := f.patchTemplate(t, created.ID, `{"description": "changed", "`+field+`": null}`)
			if status != http.StatusBadRequest {
				t.Fatalf("PATCH with %s null: status = %d, want 400: %s", field, status, body)
			}
			if !strings.Contains(body, `\"`+field+`\"`) && !strings.Contains(body, `"`+field+`"`) {
				t.Errorf("the refusal %s does not name the field %q", body, field)
			}

			// Refused as a whole: the description beside the null was not
			// applied either.
			if after := f.readTemplate(t, created.ID); !reflect.DeepEqual(after, before) {
				t.Errorf("a refused PATCH changed the template:\n before %+v\n after  %+v", before, after)
			}
		})
	}

	// Inside the survey too, since its two halves are partial on their own.
	for _, field := range []string{"enabled", "questions"} {
		t.Run("survey."+field, func(t *testing.T) {
			f := newTemplateFixture(t)
			created := f.createTemplate(t, f.fullTemplateBody("patch"))
			before := f.readTemplate(t, created.ID)

			status, body := f.patchTemplate(t, created.ID, `{"survey": {"`+field+`": null}}`)
			if status != http.StatusBadRequest {
				t.Fatalf("PATCH with survey.%s null: status = %d, want 400: %s", field, status, body)
			}
			if !strings.Contains(body, "survey."+field) {
				t.Errorf("the refusal %s does not name the field survey.%s", body, field)
			}
			if after := f.readTemplate(t, created.ID); !reflect.DeepEqual(after, before) {
				t.Errorf("a refused PATCH changed the template:\n before %+v\n after  %+v", before, after)
			}
		})
	}
}

// TestTemplateAPI_PatchRefusesABodyThatIsNotAnObjectOfKnownFields holds the
// strict decode the custom unmarshalers repeat: decodeJSON's own
// DisallowUnknownFields does not reach inside one, and a bare null would
// otherwise read as an empty patch and answer 200 having changed nothing.
func TestTemplateAPI_PatchRefusesABodyThatIsNotAnObjectOfKnownFields(t *testing.T) {
	bodies := map[string]string{
		"a bare null":                      `null`,
		"an array":                         `[]`,
		"an unknown field":                 `{"name": "renamed", "owner": "ada"}`,
		"an unknown survey field":          `{"survey": {"enabled": true, "title": "x"}}`,
		"an unknown question field":        `{"survey": {"questions": [{"variable": "v", "label": "V", "type": "text", "colour": "red"}]}}`,
		"a survey that is not an object":   `{"survey": "on"}`,
		"a survey field of the wrong type": `{"survey": {"enabled": "yes"}}`,
		"a field of the wrong type":        `{"allow_simultaneous": "yes"}`,
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			f := newTemplateFixture(t)
			created := f.createTemplate(t, f.fullTemplateBody("patch"))
			before := f.readTemplate(t, created.ID)

			status, resp := f.patchTemplate(t, created.ID, body)
			if status != http.StatusBadRequest {
				t.Fatalf("PATCH %s: status = %d, want 400: %s", body, status, resp)
			}
			// The body's own refusal, not a later rule that happens to refuse
			// the same body: every question here is otherwise valid, so a
			// strict decode that stopped refusing would be seen.
			if !strings.Contains(resp, "malformed JSON body") {
				t.Errorf("PATCH %s was refused with %s, want the malformed-body refusal", body, resp)
			}
			if after := f.readTemplate(t, created.ID); !reflect.DeepEqual(after, before) {
				t.Errorf("a refused PATCH changed the template:\n before %+v\n after  %+v", before, after)
			}
		})
	}
}

// TestTemplateAPI_PatchValidatesTheMergedTemplate holds that a partial PATCH
// runs every check a full update runs, against the template as it would be
// saved rather than only against the fields it names.
func TestTemplateAPI_PatchValidatesTheMergedTemplate(t *testing.T) {
	t.Run("a stored default the kind no longer declares", func(t *testing.T) {
		f := newTemplateFixture(t)
		created := f.createTemplate(t, f.fullTemplateBody("patch"))

		// A kind that drops a field after a template was saved with it
		// leaves exactly this behind. Written under the store, since the
		// store refuses to write it.
		ctx := context.Background()
		f.client.Template.UpdateOneID(created.ID).
			SetDefaults(map[string]any{"limit": "edge-*", "become_password": "x"}).
			ExecX(ctx)

		// The PATCH names only the name, and the name is fine. What makes it
		// refusable is the default it keeps, which a full update carrying
		// the same default would be refused for.
		status, body := f.patchTemplate(t, created.ID, `{"name": "renamed"}`)
		if status != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400: %s", status, body)
		}
		if !strings.Contains(body, "become_password") {
			t.Errorf("the refusal %s does not name the kept default it refused", body)
		}
		if got := f.readTemplate(t, created.ID); got.Name != "patch" {
			t.Errorf("name = %q, want the refused rename not applied", got.Name)
		}
	})

	t.Run("a survey nobody could answer", func(t *testing.T) {
		f := newTemplateFixture(t)
		created := f.createTemplate(t, f.fullTemplateBody("patch"))
		before := f.readTemplate(t, created.ID)

		status, body := f.patchTemplate(t, created.ID, `{"survey": {"questions": [
			{"variable": "version", "label": "VERSION", "type": "text"},
			{"variable": "version", "label": "VERSION AGAIN", "type": "text"}
		]}}`)
		if status != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400: %s", status, body)
		}
		// The duplicate rule's own refusal, with both questions labelled, so
		// no other survey rule can stand in for it.
		if !strings.Contains(body, "both write to") {
			t.Errorf("the refusal %s is not the duplicate-variable rule's", body)
		}
		if after := f.readTemplate(t, created.ID); !reflect.DeepEqual(after, before) {
			t.Errorf("a refused PATCH changed the template:\n before %+v\n after  %+v", before, after)
		}
	})

	t.Run("a prompt for a field the kind has not got", func(t *testing.T) {
		f := newTemplateFixture(t)
		created := f.createTemplate(t, f.fullTemplateBody("patch"))

		status, body := f.patchTemplate(t, created.ID, `{"prompts": ["become_password"]}`)
		if status != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400: %s", status, body)
		}
	})

	t.Run("a name another template in the organization holds", func(t *testing.T) {
		f := newTemplateFixture(t)
		f.createTemplate(t, f.fullTemplateBody("taken"))
		created := f.createTemplate(t, f.fullTemplateBody("patch"))

		status, body := f.patchTemplate(t, created.ID, `{"name": "taken"}`)
		if status != http.StatusConflict {
			t.Fatalf("status = %d, want 409: %s", status, body)
		}
	})

	t.Run("an empty name", func(t *testing.T) {
		f := newTemplateFixture(t)
		created := f.createTemplate(t, f.fullTemplateBody("patch"))

		status, body := f.patchTemplate(t, created.ID, `{"name": "  "}`)
		if status != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400: %s", status, body)
		}
	})
}

// TestTemplateAPI_PatchRefusesAChangeToAFixedField holds the device PATCH's
// fixed-field rule on a template's kind, definition and inventory, none of
// which an update can change: the store carries all three forward. They
// used to be accepted and ignored with a 200, so a client asking to
// re-point a template was told it had, and nothing changed. A different
// value is now refused, naming the field. The stored value is accepted, so
// a client that sends back the fields a create takes, one of them edited,
// still works.
func TestTemplateAPI_PatchRefusesAChangeToAFixedField(t *testing.T) {
	t.Run("an echo of the stored values", func(t *testing.T) {
		f := newTemplateFixture(t)
		created := f.createTemplate(t, f.fullTemplateBody("patch"))
		want := f.readTemplate(t, created.ID)
		want.Name = "renamed"

		// The definition is compared trimmed, as it is stored trimmed.
		status, body := f.patchTemplate(t, created.ID, fmt.Sprintf(
			`{"name": "renamed", "kind": %q, "definition": %q, "inventory": %d}`,
			want.Kind, "  "+want.Definition+" ", want.Inventory))
		if status != http.StatusOK {
			t.Fatalf("an echo of the stored kind, definition and inventory: status = %d, want 200: %s", status, body)
		}
		if got := f.readTemplate(t, created.ID); !reflect.DeepEqual(got, want) {
			t.Errorf("after the echo\n got  %+v\n want %+v", got, want)
		}
	})

	cases := map[string]func(f *templateFixture) string{
		"kind":       func(*templateFixture) string { return `"kind": "playbook"` },
		"definition": func(*templateFixture) string { return `"definition": "other"` },
		"inventory":  func(f *templateFixture) string { return fmt.Sprintf(`"inventory": %d`, f.invB) },
	}
	for field, value := range cases {
		t.Run("a different "+field, func(t *testing.T) {
			f := newTemplateFixture(t)
			created := f.createTemplate(t, f.fullTemplateBody("patch"))
			before := f.readTemplate(t, created.ID)

			status, body := f.patchTemplate(t, created.ID, `{"description": "changed", `+value(f)+`}`)
			if status != http.StatusBadRequest {
				t.Fatalf("PATCH changing %s: status = %d, want 400: %s", field, status, body)
			}
			if !strings.Contains(body, field+" cannot be changed") {
				t.Errorf("the refusal %s does not name %s", body, field)
			}
			// Refused whole: the description beside it was not applied.
			if after := f.readTemplate(t, created.ID); !reflect.DeepEqual(after, before) {
				t.Errorf("a refused PATCH changed the template:\n before %+v\n after  %+v", before, after)
			}
		})
	}
}
