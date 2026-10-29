// Tests that a secret reference is never rendered by the engine: kept as
// written in a parameter whose method resolves it, refused anywhere else, and
// refused when rendering would make one from data.
package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
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

// TestSecretRefParams_AStatementRendersAndOneBesideAReferenceIsRefused, on
// fixtures declaring lines and prompts as net.ios.config and net.ios.command
// do (the real ones are not registered in this package's tests, whose
// journal fuzzing needs net.ios.config unknown; cmd/pleiades's
// TestCLI_SecretReferencesReachTheDeviceOnly sends both to a scripted
// router): a statement beside a secret
// reference is refused by secretref.Problems (a string holding a reference is
// passed to the method as written, so the statement would reach the device as
// its source), while a statement alone in a lines item or a prompts answer is
// checked like any template and renders, so the method is handed its output.
func TestSecretRefParams_AStatementRendersAndOneBesideAReferenceIsRefused(t *testing.T) {
	t.Cleanup(collection.SnapshotForTest())
	run := func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
		return collection.Result{}, nil
	}
	for name, params := range map[string][]collection.Param{
		"t5.config":  {{Name: "lines", Type: "list of string", Format: collection.ParamFormatCommand, SecretRefs: true}},
		"t5.command": {{Name: "command", Type: "string", Format: collection.ParamFormatCommand}, {Name: "prompts", Type: collection.ParamTypeListOfDict, SecretRefs: true}},
	} {
		if err := collection.Register(collection.Descriptor{Name: name, Invoke: run, Manifest: collection.Manifest{Status: collection.StatusImplemented,
			Reversibility: collection.Reversibility{ReadOnly: true, Notes: "a test fixture that changes nothing"},
			Doc:           collection.Doc{Params: params}}}); err != nil {
			t.Fatal(err)
		}
	}
	eng := render.New()
	refused := &Task{FQCN: "t5.config", Params: map[string]any{"lines": []any{"crypto ikev2 keyring K", "{% if vars.x %}pre-shared-key {{ secret.dmvpn.psk }}{% endif %}"}}}
	if err := checkTaskTemplates(eng, refused); err == nil || !strings.Contains(err.Error(), "no other template expression, block or comment") {
		t.Errorf("a statement beside a reference: err %v, want secretref's refusal", err)
	}

	ctx := map[string]any{TemplateRootVars: map[string]any{"trunk": true, "vlans": []any{"10", "20"}, "yes": true}}
	for _, c := range []struct {
		fqcn   string
		params map[string]any
		want   func(map[string]any) bool
	}{
		{"t5.config", map[string]any{"lines": []any{
			"interface Ethernet0/1",
			"{% if vars.trunk %}switchport mode trunk{% else %}switchport mode access{% endif %}",
			"switchport trunk allowed vlan {% for v in vars.vlans %}{{ v | cli_token }}{% if not loop.last %},{% endif %}{% endfor %}",
			"pre-shared-key {{ secret.dmvpn.psk }}",
		}}, func(out map[string]any) bool {
			lines, _ := out["lines"].([]any)
			return len(lines) == 4 && lines[1] == "switchport mode trunk" && lines[2] == "switchport trunk allowed vlan 10,20" && lines[3] == "pre-shared-key {{ secret.dmvpn.psk }}"
		}},
		{"t5.command", map[string]any{"command": "reload", "prompts": []any{
			map[string]any{"match": "Proceed with reload", "answer": "{% if vars.yes %}y{% else %}n{% endif %}"},
			map[string]any{"match": "Password:", "answer": "{{ secret.ca.passphrase }}"},
		}}, func(out map[string]any) bool {
			prompts, _ := out["prompts"].([]any)
			if len(prompts) != 2 {
				return false
			}
			first, _ := prompts[0].(map[string]any)
			second, _ := prompts[1].(map[string]any)
			return first["answer"] == "y" && second["answer"] == "{{ secret.ca.passphrase }}"
		}},
	} {
		task := &Task{FQCN: c.fqcn, Params: c.params}
		if err := checkTaskTemplates(eng, task); err != nil {
			t.Fatalf("%s: a statement alone was refused: %v", c.fqcn, err)
		}
		out, err := renderParams(eng, c.fqcn, c.params, ctx)
		if err != nil {
			t.Fatalf("%s: %v", c.fqcn, err)
		}
		if !c.want(out) {
			t.Errorf("%s rendered to %v", c.fqcn, out)
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
