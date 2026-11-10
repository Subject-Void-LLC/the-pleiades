// This file holds the resolver to a credential whose type no longer
// declares inputs it stores for, which the rollback window leaves.
package resolve_test

import (
	"context"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
)

// droppedValue is the value a credential stores for an input its type
// stopped declaring.
const droppedValue = "sk-live-DROPPED-CANARY-1e2d3c4b5a69"

// TestACredentialWhoseTypeDroppedInputsStillResolves is the rollback window
// at dispatch: a newer build ships three secret inputs, a credential stores a
// value for one, a reference for another and a source binding for the third,
// and an older build's reconcile leaves all three out and keeps what the
// credential holds for them. Nothing the type declares reads them, the masking
// ruleset built from its schema does not cover them, and the injector refuses
// an input the type does not declare, so the resolver hands none of them to
// the run, and the credential still injects.
func TestACredentialWhoseTypeDroppedInputsStillResolves(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	resolver, store, _, orgID, typeID := fixture(t)

	source, err := store.CreateCredential(ctx, orgID, typeID, "source", "", map[string]string{"api_token": theSecret}, nil)
	if err != nil {
		t.Fatal(err)
	}
	older := credtype.CredentialType{
		Name:      "Dropped API",
		Kind:      credtype.KindCloud,
		Namespace: "dropped_api",
		Inputs: credtype.InputSchema{
			Fields:   []credtype.InputField{{ID: "api_token", Label: "Token", Secret: true}},
			Required: []string{"api_token"},
		},
		Injectors: credtype.Injectors{Env: map[string]string{"API_TOKEN": "{{ api_token }}"}},
	}
	newer := older
	newer.Inputs.Fields = append([]credtype.InputField{}, older.Inputs.Fields...)
	for _, id := range []string{"session", "backup", "linked"} {
		newer.Inputs.Fields = append(newer.Inputs.Fields, credtype.InputField{ID: id, Label: id, Secret: true})
	}
	shipped, err := store.EnsureManagedType(ctx, newer)
	if err != nil {
		t.Fatal(err)
	}
	cred, err := store.CreateCredential(ctx, orgID, shipped.ID, "dropped", "",
		map[string]string{"api_token": theSecret, "session": droppedValue},
		map[string]string{"backup": "file:dropped_backup"},
		credstore.WithInputSources([]credstore.InputSourceBinding{{
			InputID: "linked", SourceCredentialID: source.ID,
			Metadata: map[string]string{credtype.SourceFieldMetadataKey: "api_token"},
		}}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.EnsureManagedType(ctx, older); err != nil {
		t.Fatal(err)
	}

	got, err := resolver.Resolve(ctx, []int{cred.ID})
	if err != nil {
		t.Fatalf("Resolve() of a credential whose type dropped inputs = %v", err)
	}
	for _, id := range []string{"session", "backup", "linked"} {
		if _, held := got[0].Inputs[id]; held {
			t.Errorf("the resolver handed the run input %q, which the type does not declare", id)
		}
		if _, held := got[0].External[id]; held {
			t.Errorf("the resolver kept the reference of input %q, which the type does not declare", id)
		}
	}
	injector, err := credtype.NewInjector(render.New())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := injector.InjectOne(got[0], nil); err != nil {
		t.Errorf("InjectOne() = %v", err)
	}
}
