// Package archtest: no built-in fact's name reads as a secret.
package archtest

import (
	"strings"
	"testing"

	_ "github.com/Subject-Void-LLC/the-pleiades/internal/catalog" // registers every built-in method
	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/catalogdata"
	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// TestNoBuiltInFactNameReadsAsASecret walks every built-in method's fact
// declarations through redact.SecretName, the check that refuses to store a
// fact named like a secret: a built-in name it matches (a segment such as
// key, token or password) would declare a fact the platform then never
// stores. pkg/collection cannot import internal/redact, so the rule is held
// here rather than at Register.
func TestNoBuiltInFactNameReadsAsASecret(t *testing.T) {
	checked := 0
	for _, cfg := range catalogdata.Collections {
		d, ok := collection.Lookup(cfg.Name)
		if !ok {
			continue
		}
		name := cfg.Name
		for _, s := range d.Manifest.Facts {
			checked++
			// A family's own name is its prefix without the trailing dot.
			key := strings.TrimSuffix(s.Key, ".")
			if redact.SecretName(key) {
				t.Errorf("%s declares fact %q, which the store refuses as named like a secret", name, s.Key)
			}
		}
	}
	// The control: the walk reached the catalog's declarations.
	if checked < 20 {
		t.Fatalf("checked %d fact declarations; the built-in catalog declares more, so the walk reached too little", checked)
	}
	if !redact.SecretName("hardware.api_token") {
		t.Fatal("the control name hardware.api_token does not read as a secret, so the check proves nothing")
	}
}
