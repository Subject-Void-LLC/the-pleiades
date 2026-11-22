// Tests that writers of the project's credential file started together each
// keep their entry: goroutines in one process, and real separate processes,
// which is what several pleiades commands run at once are.
package credential_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"testing"
	"time"

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

// The environment a writer process is told its work through. A test binary
// started with saveProcessDir set is that writer, not a test run.
const (
	saveProcessDir  = "PLEIADES_TEST_SAVE_DIR"
	saveProcessName = "PLEIADES_TEST_SAVE_NAME"
	saveProcessAt   = "PLEIADES_TEST_SAVE_AT"
)

// TestHelperProcess_SavesOneCredential is the writer process
// TestSave_SeparateProcessesEachKeepTheirEntry starts: it waits for the
// moment it was given, saves one device's credential, and ends. Run as an
// ordinary test it does nothing.
func TestHelperProcess_SavesOneCredential(t *testing.T) {
	dir := os.Getenv(saveProcessDir)
	if dir == "" {
		t.Skip("a helper process for TestSave_SeparateProcessesEachKeepTheirEntry, not a test of its own")
	}
	at, err := strconv.ParseInt(os.Getenv(saveProcessAt), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	// Every writer sleeps until the same instant, so they read the file
	// together however unevenly the processes started.
	time.Sleep(time.Until(time.Unix(0, at)))
	if err := credential.SaveFileStore(dir, testKey('P'), os.Getenv(saveProcessName), credential.Credential{Username: "u", Password: "s3cret-pw"}); err != nil {
		t.Fatal(err)
	}
}

// TestSave_SeparateProcessesEachKeepTheirEntry: several processes each save a
// different device's credential at the same instant, as parallel
// add-credential commands do, and every device is in the file afterwards. A
// lock inside one process would pass the goroutine test above and still lose
// these.
func TestSave_SeparateProcessesEachKeepTheirEntry(t *testing.T) {
	const writers = 10
	for round := range 3 {
		dir := t.TempDir()
		at := time.Now().Add(1500 * time.Millisecond).UnixNano()
		cmds := make([]*exec.Cmd, writers)
		outs := make([]*bytes.Buffer, writers)
		for i := range writers {
			// #nosec G204 -- the test binary itself, re-run as a writer.
			cmds[i] = exec.Command(os.Args[0], "-test.run=^TestHelperProcess_SavesOneCredential$", "-test.count=1")
			cmds[i].Env = append(os.Environ(),
				saveProcessDir+"="+dir,
				saveProcessName+"="+fmt.Sprintf("web%02d", i),
				saveProcessAt+"="+strconv.FormatInt(at, 10),
			)
			outs[i] = &bytes.Buffer{}
			cmds[i].Stdout, cmds[i].Stderr = outs[i], outs[i]
		}
		for _, c := range cmds {
			if err := c.Start(); err != nil {
				t.Fatal(err)
			}
		}
		for i, c := range cmds {
			if err := c.Wait(); err != nil {
				t.Fatalf("round %d, writer %d: %v\n%s", round, i, err, outs[i])
			}
		}
		listed, err := credential.ListCredentials(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(listed) != writers {
			t.Fatalf("round %d: %d processes each saved a device and %d are stored: %v", round, writers, len(listed), listed)
		}
	}
}
