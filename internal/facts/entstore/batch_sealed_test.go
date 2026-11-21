// Tests of sealed fact batches on the Controller (digests.go's openBatch),
// through the real consumer and a real SQLite store: entries a Runner sealed
// to this Controller are opened and stored, and sealed entries that are
// another batch's, sealed to another key, sealed for another purpose, or
// reaching a Controller with no key are stored as nothing.
package entstore_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts/entstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts/factstest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/sealed"
)

// sealBatch is batch as a Runner publishes it: its entries sealed to pub for
// purpose, bound to the batch as it is now, and gone from the plaintext.
func sealBatch(t *testing.T, pub sealed.PublicKey, purpose string, batch facts.Batch) facts.Batch {
	t.Helper()
	plaintext, err := facts.SealPlaintextOf(batch)
	if err != nil {
		t.Fatal(err)
	}
	file, err := sealed.SealToKey(pub, purpose, plaintext)
	if err != nil {
		t.Fatal(err)
	}
	batch.Entries, batch.Sealed = nil, string(file)
	return batch
}

func TestSubscriber_OpensABatchARunnerSealedAndStoresItsFacts(t *testing.T) {
	client, device := open(t)
	store := entstore.New(client, factstest.Keys(), factstest.Envelope())
	priv, keyer, err := entstore.DigestKeys(bytes.Repeat([]byte{6}, 32))
	if err != nil {
		t.Fatal(err)
	}
	sub := entstore.NewSubscriber(store, nil, nil, entstore.WithDigestKeys(priv, keyer))
	batch := facts.Batch{JobID: "job-1", DeviceID: device, Attempt: 1, ObservedAt: time.Now().UTC(),
		Entries: runnerEntries(t, map[string]any{"os.kernel.release": "6.1", "os.big": int64(1) << 60})}
	if err := handle(t, sub, sealBatch(t, priv.Public(), sealed.PurposeFactBatch, batch)); err != nil {
		t.Fatal(err)
	}
	current, err := store.Current(context.Background(), device)
	if err != nil {
		t.Fatal(err)
	}
	if len(current) != 2 {
		t.Fatalf("stored %d facts from a sealed batch, want 2", len(current))
	}
	for _, e := range current {
		// The hash is over the value as the Runner wrote it: a number past
		// 2^53 survived being sealed, opened and decoded.
		if want, _ := factstest.Keys().Hash(device, e.Key, e.Value); want != e.Hash {
			t.Errorf("%s is stored under a hash that is not its value's", e.Key)
		}
	}
}

func TestSubscriber_DropsSealedEntriesItCannotOpenAsTheirBatchs(t *testing.T) {
	priv, keyer, err := entstore.DigestKeys(bytes.Repeat([]byte{6}, 32))
	if err != nil {
		t.Fatal(err)
	}
	other, _, err := entstore.DigestKeys(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	withKeys := []entstore.SubscriberOption{entstore.WithDigestKeys(priv, keyer)}
	for _, tc := range []struct {
		name string
		opts []entstore.SubscriberOption
		// publish is what arrives, built from the honest batch for device.
		publish func(t *testing.T, honest facts.Batch) facts.Batch
	}{
		{name: "moved into another job's batch", opts: withKeys, publish: func(t *testing.T, b facts.Batch) facts.Batch {
			moved := sealBatch(t, priv.Public(), sealed.PurposeFactBatch, b)
			moved.JobID = "job-2"
			return moved
		}},
		{name: "moved to another attempt", opts: withKeys, publish: func(t *testing.T, b facts.Batch) facts.Batch {
			moved := sealBatch(t, priv.Public(), sealed.PurposeFactBatch, b)
			moved.Attempt = 2
			return moved
		}},
		{name: "given another time", opts: withKeys, publish: func(t *testing.T, b facts.Batch) facts.Batch {
			moved := sealBatch(t, priv.Public(), sealed.PurposeFactBatch, b)
			moved.ObservedAt = moved.ObservedAt.Add(time.Hour)
			return moved
		}},
		{name: "sealed to another controller's key", opts: withKeys, publish: func(t *testing.T, b facts.Batch) facts.Batch {
			return sealBatch(t, other.Public(), sealed.PurposeFactBatch, b)
		}},
		{name: "sealed as a digest, not a batch", opts: withKeys, publish: func(t *testing.T, b facts.Batch) facts.Batch {
			return sealBatch(t, priv.Public(), sealed.PurposeFactDigest, b)
		}},
		{name: "not a sealed file", opts: withKeys, publish: func(_ *testing.T, b facts.Batch) facts.Batch {
			b.Entries, b.Sealed = nil, "not a sealed file"
			return b
		}},
		{name: "a controller with no key", publish: func(t *testing.T, b facts.Batch) facts.Batch {
			return sealBatch(t, priv.Public(), sealed.PurposeFactBatch, b)
		}},
		// Plaintext entries beside sealed ones are not a second way in: the
		// sealed entries decide, and these do not open.
		{name: "plaintext entries beside sealed ones that do not open", opts: withKeys, publish: func(t *testing.T, b facts.Batch) facts.Batch {
			plain := b.Entries
			out := sealBatch(t, other.Public(), sealed.PurposeFactBatch, b)
			out.Entries = plain
			return out
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, device := open(t)
			store := entstore.New(client, factstest.Keys(), factstest.Envelope())
			sub := entstore.NewSubscriber(store, nil, nil, tc.opts...)
			honest := facts.Batch{JobID: "job-1", DeviceID: device, Attempt: 1, ObservedAt: time.Now().UTC(),
				Entries: runnerEntries(t, map[string]any{"os.kernel.release": "6.1"})}
			if err := handle(t, sub, tc.publish(t, honest)); err != nil {
				t.Fatalf("Handle returned %v; a batch that will never open must be acknowledged, not redelivered", err)
			}
			if keys := storedKeys(t, store, device); len(keys) != 0 {
				t.Errorf("stored %v from sealed entries that are not this batch's", keys)
			}
		})
	}
}

// TestSubscriber_StoresAnEarlierRunnersUnsealedBatch: a Runner of an earlier
// release publishes its entries in plaintext, which have already crossed the
// stream; they are stored (and the Controller says so), since dropping them
// would hide nothing.
func TestSubscriber_StoresAnEarlierRunnersUnsealedBatch(t *testing.T) {
	client, device := open(t)
	store := entstore.New(client, factstest.Keys(), factstest.Envelope())
	sub := entstore.NewSubscriber(store, nil, nil)
	batch := facts.Batch{JobID: "job-1", DeviceID: device, Attempt: 1, ObservedAt: time.Now().UTC(),
		Entries: runnerEntries(t, map[string]any{"os.kernel.release": "6.1"})}
	if err := handle(t, sub, batch); err != nil {
		t.Fatal(err)
	}
	if keys := storedKeys(t, store, device); len(keys) != 1 || keys[0] != "os.kernel.release" {
		t.Errorf("stored %v, want the one fact", keys)
	}
}
