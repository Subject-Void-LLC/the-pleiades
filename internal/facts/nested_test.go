// Tests that a secret named inside an object fact's value is not stored
// (FAILURE_PATTERNS 920), under the guard both tiers build from
// internal/redact.
package facts_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
)

// productionGuard is the guard the engine and the Controller's consumer
// build: redact's own checks, not stand-ins.
func productionGuard() facts.Guard {
	return facts.Guard{Shaped: redact.SecretShaped, SecretName: redact.SecretName}
}

// TestPrepare_ASecretNamedInsideAValueIsNotStored: the key alone was held to
// the secret-name rule, so an object fact carrying a password field at any
// depth was stored whole. Each such fact is now refused with a reason naming
// the field, never its value, and the facts around it are kept.
func TestPrepare_ASecretNamedInsideAValueIsNotStored(t *testing.T) {
	const secret = "hunter2-plain"
	observed := map[string]any{
		"ansible_kernel": "6.1",
		"service.web": map[string]any{
			"state": "running", "enabled": true,
		},
		"app_config": map[string]any{
			"db": map[string]any{"host": "db1", "password": secret},
		},
		"app_users": []any{
			map[string]any{"name": "a"},
			map[string]any{"name": "b", "api_token": secret},
		},
		"app_tls": map[string]any{"Private-Key": secret},
	}
	kept, problems, err := facts.Prepare(observed, productionGuard())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, e := range kept {
		got[e.Key] = true
		if canon, _ := facts.Canonical(e.Value); strings.Contains(string(canon), secret) {
			t.Errorf("%s was stored holding the secret", e.Key)
		}
	}
	if len(got) != 2 || !got["ansible_kernel"] || !got["service.web"] {
		t.Errorf("kept %v, want ansible_kernel and service.web only", got)
	}
	reasons := map[string]string{}
	for _, p := range problems {
		reasons[p.Key] = p.Reason
		if strings.Contains(p.Reason, secret) {
			t.Errorf("the reason for %s holds the secret: %s", p.Key, p.Reason)
		}
	}
	for key, field := range map[string]string{"app_config": "password", "app_users": "api_token", "app_tls": "Private-Key"} {
		if !strings.Contains(reasons[key], field) {
			t.Errorf("reason for %s = %q, want it to name the field %q", key, reasons[key], field)
		}
	}
}

// TestVerify_ASecretNamedInsideAPublishedValueIsDropped: the Controller holds
// what a Runner published to the same rule, so an older or altered Runner
// cannot store one either.
func TestVerify_ASecretNamedInsideAPublishedValueIsDropped(t *testing.T) {
	value := map[string]any{"auth": map[string]any{"credential": "x"}}
	hash, err := facts.Hash("app_config", value)
	if err != nil {
		t.Fatal(err)
	}
	ok, err := facts.Hash("ansible_kernel", "6.1")
	if err != nil {
		t.Fatal(err)
	}
	kept, refused, err := facts.Verify([]facts.Entry{
		{Key: "app_config", Value: value, Hash: hash},
		{Key: "ansible_kernel", Value: "6.1", Hash: ok},
	}, productionGuard())
	if err != nil || refused != 1 || len(kept) != 1 || kept[0].Key != "ansible_kernel" {
		t.Fatalf("kept %+v, refused %d, %v; want only ansible_kernel", kept, refused, err)
	}
}
