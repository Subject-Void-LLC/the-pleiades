// Tests of a device holding a fact as a Controller of a release from before
// facts were sealed stores one (sealed.go's errPlaintextRows): its key and
// value in plaintext, its token and keys left empty. The store refuses the
// device, read and write, with a reason of its own rather than
// ErrKeysChanged's advice to set the previous key, which cannot help, and
// errors.Is still reads that reason as ErrKeysChanged; the consumer drops a
// batch for it saying so; and a re-key pass counts it refused. Nothing
// changes in the database.
package entstore_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	entdevice "github.com/Subject-Void-LLC/the-pleiades/internal/ent/device"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts/entstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts/factstest"
)

// plantPlaintextFact inserts, for the device whose database ID is dev, a row
// as a Controller of a release before sealing writes one: the key in the key
// column, the value in plaintext JSON, a plain SHA-256 hash, and only the
// columns that release knows, so key_token and keyed_by take their defaults.
func plantPlaintextFact(t *testing.T, db *sql.DB, dev int) {
	t.Helper()
	const key, value = "package.openssh-server", "1:9.6p1-3ubuntu13.5"
	hash, err := facts.Hash(key, value)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC)
	if _, err := db.ExecContext(context.Background(),
		"INSERT INTO facts (created_at, updated_at, key, payload, hash, kind, observation_seq, device_facts) VALUES (?, ?, ?, ?, ?, 'stable', 0, ?)",
		at, at, key, `{"json":"\"`+value+`\""}`, hash, dev); err != nil {
		t.Fatalf("planting a plaintext row: %v", err)
	}
	// The control: the row names no token and no keys, as this test means.
	if got := rawQuery(t, db, "SELECT key_token, keyed_by FROM facts WHERE key = ?", key); got != "||\n" {
		t.Fatalf("the control failed: the planted row's key_token and keyed_by are %q, want both empty", got)
	}
}

// wantPlaintextRefusal fails t unless err is the plaintext rows' refusal:
// ErrKeysChanged to errors.Is, in words of its own that do not send the
// operator to the previous key.
func wantPlaintextRefusal(t *testing.T, what string, err error) {
	t.Helper()
	if !errors.Is(err, entstore.ErrKeysChanged) {
		t.Errorf("%s = %v, want a refusal errors.Is reads as ErrKeysChanged", what, err)
		return
	}
	if text := err.Error(); !strings.Contains(text, "name no facts key") || strings.Contains(text, "set PLEIADES_FACTS_DIGEST_KEY_PREVIOUS to") {
		t.Errorf("%s = %q, want the plaintext rows' own reason, not advice to set the previous key", what, text)
	}
}

func TestStore_RefusesADeviceHoldingPlaintextRowsWithItsOwnReason(t *testing.T) {
	ctx := context.Background()
	client, device, db, _ := sqliteClientDB(t)
	plantPlaintextFact(t, db, client.Device.Query().Where(entdevice.DeviceIDEQ(device)).OnlyX(ctx).ID)
	raw := rawFactTables(t, db)
	// With a previous key and without: neither re-keys plaintext.
	for name, store := range map[string]*entstore.Store{
		"the current key alone": entstore.New(client, factstest.Keys(), factstest.Envelope()),
		"beside a previous key": entstore.New(client, factstest.Keys(), factstest.Envelope(), entstore.WithPreviousKeys(newFactKeys(t, 2))),
	} {
		_, err := store.Current(ctx, device)
		wantPlaintextRefusal(t, name+": Current", err)
		_, err = store.History(ctx, device, "package.openssh-server")
		wantPlaintextRefusal(t, name+": History", err)
		err = recordGather(t, store, device, "run1", 10, rotationT0, map[string]any{"package.openssh-server": "9.9"}, nil)
		wantPlaintextRefusal(t, name+": Record", err)
	}

	// The consumer acknowledges a batch for the device and says why, in
	// words that do not send the operator to a key.
	var logged bytes.Buffer
	sub := entstore.NewSubscriber(entstore.New(client, factstest.Keys(), factstest.Envelope()), slog.New(slog.NewTextHandler(&logged, nil)), nil)
	batch := facts.Batch{JobID: "job-1", DeviceID: device, Attempt: 1, ObservedAt: time.Now(), Entries: prepared(t, map[string]any{"package.openssh-server": "9.9"})}
	if err := handle(t, sub, batch); err != nil {
		t.Errorf("the consumer returned %v, want the batch acknowledged: a redelivery is refused the same way", err)
	}
	if out := logged.String(); !strings.Contains(out, "level=ERROR") || !strings.Contains(out, "plaintext rows") || strings.Contains(out, "set PLEIADES_FACTS_DIGEST_KEY to the key") {
		t.Errorf("the consumer did not say the device holds plaintext rows, or sent the operator to a key:\n%s", out)
	}
	if got := rawFactTables(t, db); got != raw {
		t.Error("a refused device's stored facts changed")
	}
}

// TestRekey_PlaintextBesideThePreviousKeyIsRefused: a device under the
// previous keys that also holds a plaintext row is not re-keyed. A re-key
// pass counts it refused, names it and leaves it remaining, exactly as it
// was, and a read says why.
func TestRekey_PlaintextBesideThePreviousKeyIsRefused(t *testing.T) {
	ctx := context.Background()
	client, device, db, _ := sqliteClientDB(t)
	old := factstest.Keys()
	gatherRotationHistory(t, entstore.New(client, old, factstest.Envelope()), device)
	plantPlaintextFact(t, db, client.Device.Query().Where(entdevice.DeviceIDEQ(device)).OnlyX(ctx).ID)
	raw := rawFactTables(t, db)
	rotated := entstore.New(client, newFactKeys(t, 7), factstest.Envelope(), entstore.WithPreviousKeys(old))
	count, err := rotated.RekeyAll(ctx)
	if err != nil || count.Devices != 0 || count.Refused != 1 || count.Failed != 0 || len(count.RefusedDevices) != 1 || count.RefusedDevices[0] != device || count.Remaining != 1 {
		t.Errorf("RekeyAll = %+v, %v; want the device refused, named and still remaining", count, err)
	}
	_, err = rotated.Current(ctx, device)
	wantPlaintextRefusal(t, "Current", err)
	if got := rawFactTables(t, db); got != raw {
		t.Error("a refused re-key changed the database")
	}
}
