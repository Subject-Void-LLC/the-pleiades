// Tests for named secrets through the run's context.
package engine_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// TestNamedSecrets_FromTheProjectStore: a run's context over the project's
// credential file answers a stored field, joins it to the run's masking set
// as it answers, and answers a missing one as missing.
func TestNamedSecrets_FromTheProjectStore(t *testing.T) {
	dir := t.TempDir()
	key, err := credential.ResolveMasterKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := credential.SaveSecretField(dir, key, "dmvpn", "psk", "Stored-Psk-4321"); err != nil {
		t.Fatal(err)
	}
	rc, err := engine.NewCredentialRunbookContext(credential.NewLazyFileStore(dir))(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	ns := rc.(sdk.NamedSecrets)
	if v, ok, err := ns.NamedSecret("dmvpn", "psk"); err != nil || !ok || v != "Stored-Psk-4321" {
		t.Fatalf("NamedSecret = %q, %v, %v", v, ok, err)
	}
	if got := rc.(interface{ RegisteredSecrets() []string }).RegisteredSecrets(); len(got) != 1 || got[0] != "Stored-Psk-4321" {
		t.Fatalf("registered %v, want the value handed out", got)
	}
	if _, ok, err := ns.NamedSecret("dmvpn", "absent"); ok || err != nil {
		t.Fatalf("an absent field: %v, %v", ok, err)
	}
}

// TestWithKnownSecrets_AreInEveryRunsMaskingSet: a value given to the
// executor is in a run's masking set although no task resolved it.
func TestWithKnownSecrets_AreInEveryRunsMaskingSet(t *testing.T) {
	dag := yamlRunbook(t, "id: k\ntasks:\n  - name: nothing\n    noop:\n      changed: false\n")
	x := engine.NewExecutor(mapResolver{}, engine.NewBuiltinActionExecutor(), lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0,
		engine.WithKnownSecrets([]string{"Known-Secret-1", ""}))
	result, err := x.Run(context.Background(), dag)
	if err != nil || result.HasErrors() {
		t.Fatalf("Run: %v, %+v", err, result.Nodes)
	}
	if len(result.Secrets) != 1 || result.Secrets[0] != "Known-Secret-1" {
		t.Fatalf("Secrets = %v, want the known one alone", result.Secrets)
	}
}

// TestNamedSecrets_AContextWithNoStoreSaysWhy: a context with no credential
// store (a Runner's) answers a reference with the reason it cannot resolve
// one, never with "no such secret", which would send a person to add one.
func TestNamedSecrets_AContextWithNoStoreSaysWhy(t *testing.T) {
	rc := engine.NewRunbookContext(nil)
	if _, _, err := sdk.ResolveSecretRefs(rc, "key {{ secret.dmvpn.psk }}"); !errors.Is(err, sdk.ErrNoNamedSecrets) {
		t.Fatalf("err = %v, want ErrNoNamedSecrets", err)
	}
	if got, _, err := sdk.ResolveSecretRefs(rc, "hostname r1"); err != nil || got != "hostname r1" {
		t.Fatalf("text with no reference: %q, %v", got, err)
	}
}
