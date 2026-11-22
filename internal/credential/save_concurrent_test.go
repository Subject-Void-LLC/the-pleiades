// Tests that writers of the project's credential file started together in
// one process each keep their entry. Separate processes, which is what
// several pleiades commands run at once are, are in
// save_process_internal_test.go.
package credential_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// TestSave_WritersStartedTogetherEachKeepTheirEntry: every kind of writer of
// the credential file (a device's credential, a named secret, a registry
// account, a database connection) is started at once, many of each, and every
// entry is in the file afterwards. Each writer reads the file, changes one
// entry and renames a new file into place; with nothing held across those
// steps, two that read the same file each wrote it back without the other's
// entry, and the last rename won.
func TestSave_WritersStartedTogetherEachKeepTheirEntry(t *testing.T) {
	const each = 12
	key := testKey('L')
	for round := range 3 {
		dir := t.TempDir()
		start := make(chan struct{})
		var wg sync.WaitGroup
		errs := make(chan error, each*4)
		// launch starts one writer that waits for the others before saving.
		launch := func(save func() error) {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				if err := save(); err != nil {
					errs <- err
				}
			}()
		}
		for i := range each {
			launch(func() error {
				return credential.SaveFileStore(dir, key, fmt.Sprintf("web%02d", i), credential.Credential{Username: "u", Password: "s3cret-pw"})
			})
			launch(func() error {
				return credential.SaveSecretField(dir, key, fmt.Sprintf("ike%02d", i), "key", "a-shared-key-value")
			})
			launch(func() error {
				return credential.SaveRegistryCredential(dir, key, fmt.Sprintf("registry%02d.example.com", i), "u", "s3cret-pw")
			})
			launch(func() error {
				return credential.SaveDatabaseConnection(dir, key, wire.DatabaseConnection{
					Name: fmt.Sprintf("db%02d", i), Host: "db.internal", Port: 5432, User: "app", Password: "s3cret-pw",
				})
			})
		}
		close(start)
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Fatalf("round %d: a writer failed: %v", round, err)
		}

		devices, err := credential.ListCredentials(dir)
		if err != nil {
			t.Fatal(err)
		}
		secrets, err := credential.ListSecrets(dir)
		if err != nil {
			t.Fatal(err)
		}
		databases, err := credential.ListDatabaseConnections(dir)
		if err != nil {
			t.Fatal(err)
		}
		store, err := credential.NewFileStore(dir, key)
		if err != nil {
			t.Fatal(err)
		}
		registries := 0
		for i := range each {
			_, _, ok, err := store.(credential.RegistryStore).LookupRegistry(context.Background(), fmt.Sprintf("registry%02d.example.com", i))
			if err != nil {
				t.Fatal(err)
			}
			if ok {
				registries++
			}
		}
		if len(devices) != each || len(secrets) != each || len(databases) != each || registries != each {
			t.Fatalf("round %d: %d writers of each kind left %d devices, %d secrets, %d registries and %d databases; every one must be kept",
				round, each, len(devices), len(secrets), registries, len(databases))
		}
	}
}
