// Tests that a Crawl run answers a method's request for a registry account
// from the project's store, for the registry asked about and no other.
package engine

import (
	"context"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// TestCredentialRunbookContext_AnswersForRegistries: on the Crawl tier a
// task's run answers for the registry accounts the project stores, for the
// host a method names and no other, whether or not the device has a
// credential of its own.
func TestCredentialRunbookContext_AnswersForRegistries(t *testing.T) {
	dir := t.TempDir()
	key, err := credential.ResolveMasterKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := credential.SaveRegistryCredential(dir, key, "ghcr.io", "deployer", "registry-secret"); err != nil {
		t.Fatal(err)
	}
	contextFor := NewCredentialRunbookContext(credential.NewLazyFileStore(dir))
	for _, device := range []*inventorytest.Stub{{StubName: "no-credential"}, nil} {
		var item any = device
		if device == nil {
			item = nil
		}
		var rc sdk.RunbookContext
		if item == nil {
			rc, err = contextFor(context.Background(), nil)
		} else {
			rc, err = contextFor(context.Background(), device)
		}
		if err != nil {
			t.Fatal(err)
		}
		regs, ok := rc.(sdk.RegistryCredentials)
		if !ok {
			t.Fatalf("the run for %v cannot answer for registries", item)
		}
		if u, p, ok := regs.RegistryCredential("GHCR.io"); !ok || u != "deployer" || p != "registry-secret" {
			t.Errorf("own registry = %q, %q, %v", u, p, ok)
		}
		if _, _, ok := regs.RegistryCredential("quay.io"); ok {
			t.Error("another registry got the account")
		}
		if secrets := rc.InjectSecrets(); secrets["password"] == "registry-secret" {
			t.Error("the registry password reached the device secrets")
		}
	}
}
