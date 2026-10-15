// Tests that a registry account a method obtains while it runs joins the
// run's masking set, on failure as on success.
package engine_test

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// registryMaskSentinel is the stored registry password. Shaped like no
// pattern rule, so only the account itself, registered as a secret, masks it.
const registryMaskSentinel = "regsentinel-7Hq2xW9p"

// TestCollectionAction_MasksTheRegistryAccountAMethodObtained: a method that
// asks the run for a registry account and builds the X-Registry-Auth header
// from it, then fails echoing both, has both in the run's masking set. Before
// sdk.SecretRegistrar the Crawl tier masked only what InjectSecrets handed
// out, which never holds a registry account.
func TestCollectionAction_MasksTheRegistryAccountAMethodObtained(t *testing.T) {
	dir := t.TempDir()
	key, err := credential.ResolveMasterKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := credential.SaveRegistryCredential(dir, key, "ghcr.io", "deployer", registryMaskSentinel); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(collection.SnapshotForTest())
	var header string
	const name = "enginetest.registryecho"
	if err := collection.Register(collection.Descriptor{
		Name: name,
		Manifest: collection.Manifest{
			Status:        collection.StatusImplemented,
			Reversibility: collection.Reversibility{Notes: "a test fixture that changes nothing"},
		},
		Invoke: func(_ context.Context, rc sdk.RunbookContext, _ inventory.InventoryItem, _ map[string]any) (collection.Result, error) {
			h, err := containerengine.RegistryAuth(rc, "ghcr.io/org/app:1")
			if err != nil {
				return collection.Result{}, err
			}
			header = h
			_, password, _ := rc.(sdk.RegistryCredentials).RegistryCredential("ghcr.io")
			return collection.Result{}, fmt.Errorf("pull refused for password %s with header %s", password, h)
		},
	}); err != nil {
		t.Fatal(err)
	}

	exec := engine.NewCollectionActionExecutor(&recordingFallback{}, engine.NewCredentialRunbookContext(credential.NewLazyFileStore(dir)))
	result, err := exec.Execute(context.Background(), &engine.Task{FQCN: name}, newNamedDevice("web1"))
	if err == nil {
		t.Fatal("the fixture's failure did not surface")
	}
	if header == "" {
		t.Fatal("the method built no header, so the test proves nothing")
	}
	for _, want := range []string{registryMaskSentinel, header} {
		if !slices.Contains(result.Secrets, want) {
			t.Errorf("the run's masking set lacks %q: %v", want, result.Secrets)
		}
	}
}
