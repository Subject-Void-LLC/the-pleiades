// Tests for the key registry on SQLite and PostgreSQL, including replicas racing
// to register one key.
package keyregistry_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/testcontainers/testcontainers-go"
	testpg "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/Subject-Void-LLC/the-pleiades/internal/activity"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/keyregistry"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
)

// These tests run against real databases through ent.OpenDatabase, the
// path the controller and the setup command take, rather than enttest's
// Schema.Create: a registry whose table exists only in tests would pass
// here and be missing in every deployment.

// backend opens a migrated database for one dialect.
type backend struct {
	name string
	open func(t *testing.T) *ent.Client
}

// backends returns SQLite always, and PostgreSQL unless -short. PostgreSQL
// matters here for one reason: two controller replicas starting together
// both try to register their key, and whether the loser sees a constraint
// error ent recognises depends on the driver's error type.
func backends(t *testing.T) []backend {
	t.Helper()
	list := []backend{{name: "sqlite", open: func(t *testing.T) *ent.Client {
		return openDSN(t, "sqlite://"+filepath.Join(t.TempDir(), "registry.db"))
	}}}
	if !testing.Short() {
		list = append(list, backend{name: "postgres", open: func(t *testing.T) *ent.Client {
			ctx := context.Background()
			pg, err := testpg.Run(ctx, testsupport.PostgresImage,
				testpg.WithDatabase("pleiades"), testpg.WithUsername("pleiades"), testpg.WithPassword("pleiades"),
				testpg.BasicWaitStrategies())
			if err != nil {
				t.Fatalf("starting postgres: %v", err)
			}
			t.Cleanup(func() { _ = testcontainers.TerminateContainer(pg) })
			dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
			if err != nil {
				t.Fatalf("reading the postgres DSN: %v", err)
			}
			return openDSN(t, dsn)
		}})
	}
	return list
}

// openDSN opens and migrates dsn.
func openDSN(t *testing.T, dsn string) *ent.Client {
	t.Helper()
	client, err := ent.OpenDatabase(context.Background(), ent.Config{DSN: dsn})
	if err != nil {
		t.Fatalf("OpenDatabase() error = %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// fp is a fingerprint-shaped test value.
func fp(c string) string { return strings.Repeat(c, 64) }

// TestRegister_RecordsOnceAndReturnsTheExistingRecordAfter covers the
// store's contract on both dialects: the first call creates, later calls for
// the same key return the original row and create nothing, and many callers
// racing on one key produce exactly one row.
func TestRegister_RecordsOnceAndReturnsTheExistingRecordAfter(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			store := keyregistry.NewEntStore(b.open(t))

			first, created, err := store.Register(ctx, keyregistry.Record{
				Fingerprint: fp("a"), Version: "v1",
				Origin: keyregistry.OriginSetup, Possession: keyregistry.PossessionChecked,
			})
			if err != nil || !created || first.ID <= 0 {
				t.Fatalf("Register() = %+v, %v, %v; want a new record", first, created, err)
			}

			again, created, err := store.Register(ctx, keyregistry.Record{
				Fingerprint: fp("a"), Version: "v2",
				Origin: keyregistry.OriginFirstUse, Possession: keyregistry.PossessionNotApplicable,
			})
			if err != nil || created || again.ID != first.ID || again.Origin != keyregistry.OriginSetup {
				t.Fatalf("second Register() = %+v, %v, %v; want the original record unchanged", again, created, err)
			}

			if _, err := store.Get(ctx, fp("z")); !errors.Is(err, keyregistry.ErrNotRegistered) {
				t.Fatalf("Get(unknown) error = %v, want ErrNotRegistered", err)
			}

			// The replica race: many writers, one key, one row.
			const writers = 8
			var wg sync.WaitGroup
			var mu sync.Mutex
			createdCount := 0
			ids := map[int]bool{}
			for i := 0; i < writers; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					r, c, err := store.Register(ctx, keyregistry.Record{
						Fingerprint: fp("b"), Version: "v1",
						Origin: keyregistry.OriginFirstUse, Possession: keyregistry.PossessionNotApplicable,
					})
					if err != nil {
						t.Errorf("concurrent Register() error = %v", err)
						return
					}
					mu.Lock()
					defer mu.Unlock()
					if c {
						createdCount++
					}
					ids[r.ID] = true
				}()
			}
			wg.Wait()
			if createdCount != 1 || len(ids) != 1 {
				t.Fatalf("%d concurrent registrations created %d rows across ids %v; want exactly one", writers, createdCount, ids)
			}
		})
	}
}

// recordingRecorder captures activity entries.
type recordingRecorder struct {
	entries []activity.Entry
	fail    bool
}

// Record captures e, or fails when told to.
func (r *recordingRecorder) Record(_ context.Context, e activity.Entry) error {
	if r.fail {
		return errors.New("the activity stream is down")
	}
	r.entries = append(r.entries, e)
	return nil
}

// TestAuditedStore_RecordsAKeyOnceAndNeverItsValue proves the activity entry
// is written when a key is first registered, not again, and names the key by
// short fingerprint only.
func TestAuditedStore_RecordsAKeyOnceAndNeverItsValue(t *testing.T) {
	ctx := context.Background()
	rec := &recordingRecorder{}
	store := keyregistry.NewAuditedStore(
		keyregistry.NewEntStore(openDSN(t, "sqlite://"+filepath.Join(t.TempDir(), "audited.db"))),
		rec, func(context.Context) string { return "controller-setup" }, nil)

	r := keyregistry.Record{Fingerprint: fp("c"), Version: "v1", Origin: keyregistry.OriginSetup, Possession: keyregistry.PossessionChecked}
	stored, _, err := store.Register(ctx, r)
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if _, _, err := store.Register(ctx, r); err != nil {
		t.Fatalf("second Register() error = %v", err)
	}

	if len(rec.entries) != 1 {
		t.Fatalf("recorded %d entries, want one for the one key", len(rec.entries))
	}
	got := rec.entries[0]
	want := activity.Entry{
		Actor: "controller-setup", Action: activity.ActionCreated,
		ObjectKind: activity.KindEncryptionKey, ObjectID: stored.ID,
		ObjectName: "cccc-cccc (generated by setup, possession checked)",
	}
	if got != want {
		t.Fatalf("entry = %+v, want %+v", got, want)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("the entry would be refused by the activity store: %v", err)
	}
}

// TestAuditedStore_AStreamFailureDoesNotUndoTheKey proves the registration
// stands, and the failure is logged, when the activity stream refuses.
func TestAuditedStore_AStreamFailureDoesNotUndoTheKey(t *testing.T) {
	var logs bytes.Buffer
	store := keyregistry.NewAuditedStore(
		keyregistry.NewEntStore(openDSN(t, "sqlite://"+filepath.Join(t.TempDir(), "failing.db"))),
		&recordingRecorder{fail: true}, func(context.Context) string { return "controller" },
		slog.New(slog.NewTextHandler(&logs, nil)))

	_, created, err := store.Register(context.Background(), keyregistry.Record{
		Fingerprint: fp("d"), Version: "v1", Origin: keyregistry.OriginFirstUse, Possession: keyregistry.PossessionNotApplicable,
	})
	if err != nil || !created {
		t.Fatalf("Register() = %v, %v; want the key registered despite the stream", created, err)
	}
	if !strings.Contains(logs.String(), "failed to record an encryption key in the activity stream") {
		t.Fatalf("the stream failure was not logged:\n%s", logs.String())
	}
}

// TestRecordDescribe pins the three descriptions an operator reads in the
// activity stream.
func TestRecordDescribe(t *testing.T) {
	cases := map[string]keyregistry.Record{
		"3f9a-c21b (generated by setup, possession checked)":     {Fingerprint: "3f9ac21b" + fp("0")[8:], Origin: keyregistry.OriginSetup, Possession: keyregistry.PossessionChecked},
		"3f9a-c21b (generated by setup, possession not checked)": {Fingerprint: "3f9ac21b" + fp("0")[8:], Origin: keyregistry.OriginSetup, Possession: keyregistry.PossessionNotChecked},
		"3f9a-c21b (first used by this database)":                {Fingerprint: "3f9ac21b" + fp("0")[8:], Origin: keyregistry.OriginFirstUse, Possession: keyregistry.PossessionNotApplicable},
	}
	for want, r := range cases {
		if got := r.Describe(); got != want {
			t.Errorf("Describe() = %q, want %q", got, want)
		}
	}
	if got := keyregistry.Short("abc"); got != "abc" {
		t.Errorf("Short(too short) = %q, want it unchanged", got)
	}
	if _, _, err := keyregistry.NewEntStore(nil).Register(context.Background(), keyregistry.Record{}); err == nil {
		t.Error("Register accepted a record with no fingerprint")
	}
}
