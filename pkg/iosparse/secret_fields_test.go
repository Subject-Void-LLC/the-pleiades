// Every field genieparser's schemas name, held to netcfg's one table of
// secret fields: a field whose name reads like a secret is either one
// (netcfg.SecretField, so net.ios.parse masks it) or named here with why it is
// not. A parser ported later with a password in its schema then cannot reach
// a result unmasked by being missed.
package iosparse

import (
	"regexp"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcfg"
)

// secretLooking is a field name that may hold a secret.
var secretLooking = regexp.MustCompile(`(?i)(password|secret|key|community|psk|passphrase|auth)`)

// notSecretFields are fields whose names read like a secret's and are not,
// each with why.
var notSecretFields = map[string]string{
	// show bgp summary: how many community entries BGP holds, a count.
	"community_entries": "a count of BGP community entries, not a community",
}

// schemaKeys collects every literal key of s, at any depth.
func schemaKeys(s schema, into map[string]bool) {
	for _, f := range s {
		if !f.wild {
			into[f.key] = true
		}
		if sub, ok := f.val.(schema); ok {
			schemaKeys(sub, into)
		}
	}
}

// TestSchemas_SecretLookingFieldsAreDecided is the rule above, over every
// ported parser's schema.
func TestSchemas_SecretLookingFieldsAreDecided(t *testing.T) {
	keys := map[string]bool{}
	for _, f := range forms {
		schemaKeys(f.def.schema, keys)
	}
	if len(keys) < 50 {
		t.Fatalf("only %d schema keys found: the walk is not reaching the schemas", len(keys))
	}
	for k := range keys {
		if !secretLooking.MatchString(k) {
			continue
		}
		if _, cleared := notSecretFields[k]; !cleared && !netcfg.SecretField(k) {
			t.Errorf("schema field %q reads like a secret: add it to pkg/netcfg's secretFields, so net.ios.parse masks it, or to notSecretFields here with why it is not one", k)
		}
	}
	for k := range notSecretFields {
		if !keys[k] {
			t.Errorf("notSecretFields names %q, which no ported schema has: remove it", k)
		}
	}
}
