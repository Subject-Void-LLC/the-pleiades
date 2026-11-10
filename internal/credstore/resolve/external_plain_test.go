// This file holds dispatch to the rule that a value resolved from an
// external source lands only in an input the type marks secret. The
// masking ruleset is built from the type's secret fields, so a secret
// resolved into any other input reached the run in plain text.
package resolve_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credstore/resolve"
)

// TestAnExternalReferenceIntoAPlainInputIsRefusedAtDispatch seeds the
// reference a write made before the store refused it, through the ent
// client, and expects the dispatch to fail naming the input, without
// fetching the secret or injecting it.
func TestAnExternalReferenceIntoAPlainInputIsRefusedAtDispatch(t *testing.T) {
	const fileSecret = "a-secret-from-the-file-source"
	lookups := secretsDir(t, map[string]string{"edge_secret": fileSecret + "\n"})
	client, id := externalFixture(t, map[string]string{"api_token": "stored"}, nil)
	client.Credential.UpdateOneID(id).SetExternal(map[string]string{"region": "file:edge_secret"}).ExecX(context.Background())

	resolver := resolve.NewEntResolver(client, resolve.WithLookups(lookups))
	got, err := resolver.Resolve(context.Background(), []int{id})
	if err == nil {
		masked := false
		for _, v := range got[0].SecretValues() {
			masked = masked || v == fileSecret
		}
		t.Fatalf("Resolve() put %q into the plain input region, masked %v; want a refusal", got[0].Inputs["region"], masked)
	}
	if !strings.Contains(err.Error(), `"region"`) || strings.Contains(err.Error(), fileSecret) {
		t.Errorf("the refusal %q does not name the input, or repeats the secret", err)
	}
}
