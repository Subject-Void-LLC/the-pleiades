// Tests that a secret reference is never rendered by the engine: kept as
// written in a parameter whose method resolves it, refused anywhere else, and
// refused when rendering would make one from data.
package engine

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
)

// TestSecretRefProblems_OnlyWhereTheMethodResolves: a reference is judged
// apart from the renderer, accepted in a parameter that takes references when
// it stands alone and exactly written, and refused in one that does not.
func TestSecretRefProblems_OnlyWhereTheMethodResolves(t *testing.T) {
	for _, c := range []struct {
		source  string
		takes   bool
		isRef   bool
		refused string
	}{
		{"pre-shared-key {{ secret.dmvpn.psk }}", true, true, ""},
		{"pre-shared-key {{ secret.dmvpn.psk }}", false, true, "only in a parameter its method resolves"},
		{"{{ secret.dmvpn.psk | quote }}", true, true, "no filter"},
		{"{{ vars.x }} {{ secret.dmvpn.psk }}", true, true, "no other template expression"},
		{"{{ vars.site }}", true, false, ""},
		{"plain text", false, false, ""},
	} {
		isRef, problems := SecretRefProblems(c.source, c.takes)
		switch {
		case isRef != c.isRef:
			t.Errorf("%q (takes %v): isRef %v, want %v", c.source, c.takes, isRef, c.isRef)
		case c.refused == "" && len(problems) > 0:
			t.Errorf("%q (takes %v) was refused: %v", c.source, c.takes, problems)
		case c.refused != "" && (len(problems) == 0 || !strings.Contains(strings.Join(problems, "; "), c.refused)):
			t.Errorf("%q (takes %v): problems %v, want one saying %q", c.source, c.takes, problems, c.refused)
		}
	}
}

// TestRenderKeepingRefs_PassesReferencesAndRefusesMadeOnes: a string holding
// a reference is passed on as written, at any depth; other templates render;
// and a template whose value would put a reference in front of the method is
// refused, so only the runbook's author writes one.
func TestRenderKeepingRefs_PassesReferencesAndRefusesMadeOnes(t *testing.T) {
	eng := render.New()
	ctx := map[string]any{TemplateRootVars: map[string]any{"site": "hq", "evil": "{{ secret.dmvpn.psk }}"}}

	got, err := renderKeepingRefs(eng, []any{"key {{ secret.dmvpn.psk }}", "site {{ vars.site }}"}, ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	list := got.([]any)
	if list[0] != "key {{ secret.dmvpn.psk }}" || list[1] != "site hq" {
		t.Fatalf("rendered %v", list)
	}

	nested, err := renderKeepingRefs(eng, []any{map[string]any{"match": "Password:", "answer": "{{ secret.ca.passphrase }}"}}, ctx, false)
	if err != nil || nested.([]any)[0].(map[string]any)["answer"] != "{{ secret.ca.passphrase }}" {
		t.Fatalf("a nested reference: %v, %v", nested, err)
	}

	ctx[TemplateRootVars].(map[string]any)["prompts"] = []any{map[string]any{"match": "Password:", "answer": "{{ secret.ca.passphrase }}"}}
	for name, v := range map[string]any{
		"in text":        "answer {{ vars.evil }}",
		"in a list item": []any{"{{ vars.evil }}"},
		"in a map":       []any{map[string]any{"answer": "{{ vars.evil }}"}},
		// A one-expression template keeps its value's type: a variable
		// holding the whole prompts list (the review's F2).
		"a whole list from a variable": "{{ vars.prompts }}",
	} {
		if _, err := renderKeepingRefs(eng, v, ctx, false); err == nil || !strings.Contains(err.Error(), "only the runbook's author") {
			t.Errorf("%s: a reference made by rendering was not refused: %v", name, err)
		}
	}
}
