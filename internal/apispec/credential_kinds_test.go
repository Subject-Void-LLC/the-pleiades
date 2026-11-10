// This file holds the credential type documents' kind descriptions to the
// closed vocabulary itself, so a kind added to credtype cannot be left out
// of the published API and a count written into the text cannot go stale.
package apispec

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
)

// TestCredentialKindDescriptionsNameEveryKind checks each schema's kind
// property names every kind credtype.Kinds returns.
func TestCredentialKindDescriptionsNameEveryKind(t *testing.T) {
	schemas := map[string]map[string]any{
		"the credential type read":        credentialTypeSchema,
		"the credential type create body": CreateCredentialType.RequestSchema,
		"the credential type PATCH body":  credentialTypePatchSchema,
	}
	for name, schema := range schemas {
		kind, ok := schema["properties"].(map[string]any)["kind"].(map[string]any)
		if !ok {
			t.Fatalf("%s has no kind property", name)
		}
		description, _ := kind["description"].(string)
		for _, k := range credtype.Kinds() {
			if !strings.Contains(description, string(k)) {
				t.Errorf("%s's kind description %q does not name %q", name, description, k)
			}
		}
	}
}
