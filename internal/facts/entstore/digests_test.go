// Tests of keyed digests on the Controller (digests.go), through the real
// consumer and a real SQLite store: a digest a Runner sealed to this
// Controller is stored keyed, a digest sealed for another fact or device and
// one reaching a Controller without the keys are stored as nothing, and a raw
// digest is never stored at all.
package entstore_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts/entstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/sealed"
)

// digestRaw is the SHA-256 of "pin1234\n" (printf 'pin1234\n' | sha256sum).
const digestRaw = "3f773b3737a2f378fa4589ecacc53598168149f40fa0294c8580ee8fa4455b96"

// sealFor is what a Runner publishes for device's fact key: raw, sealed to pub.
func sealFor(t *testing.T, pub sealed.PublicKey, device, key string) string {
	t.Helper()
	plaintext, err := facts.SealPlaintext(device, key, digestRaw)
	if err != nil {
		t.Fatal(err)
	}
	file, err := sealed.SealToKey(pub, sealed.PurposeFactDigest, plaintext)
	if err != nil {
		t.Fatal(err)
	}
	return string(file)
}

// sealedPassword is a file sealed to pub as a password, not a fact digest.
func sealedPassword(t *testing.T, pub sealed.PublicKey) string {
	t.Helper()
	plaintext, _ := facts.SealPlaintext("x", "file.w", digestRaw)
	file, err := sealed.SealToKey(pub, sealed.PurposePassword, plaintext)
	if err != nil {
		t.Fatal(err)
	}
	return string(file)
}

// runnerEntries is observed as a sealing Runner prepares it.
func runnerEntries(t *testing.T, observed map[string]any) []facts.Entry {
	t.Helper()
	entries, problems, err := facts.Prepare(observed, facts.Guard{AllowSealed: true})
	if err != nil || len(problems) != 0 {
		t.Fatalf("Prepare: %v, %v", problems, err)
	}
	return entries
}

// storedValues is every stored fact's value, by key, and whether anything in
// the database holds the raw digest.
func storedValues(t *testing.T, store *entstore.Store, device string) map[string]map[string]any {
	t.Helper()
	current, err := store.Current(context.Background(), device)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]map[string]any{}
	for _, e := range current {
		v, _ := e.Value.(map[string]any)
		out[e.Key] = v
		if want, _ := facts.Hash(e.Key, e.Value); want != e.Hash {
			t.Errorf("%s is stored under a hash that is not its value's", e.Key)
		}
	}
	return out
}

func TestDigestKeys_EveryReplicaDerivesTheSameKeys(t *testing.T) {
	secret := bytes.Repeat([]byte{6}, 32)
	open1, keyer1, err := entstore.DigestKeys(secret)
	if err != nil {
		t.Fatal(err)
	}
	open2, keyer2, _ := entstore.DigestKeys(secret)
	if open1.Public().String() != open2.Public().String() || keyer1.ID() != keyer2.ID() {
		t.Error("two replicas given one secret derived different keys")
	}
	if _, _, err := entstore.DigestKeys(make([]byte, 16)); err == nil {
		t.Error("a 16-byte secret was accepted")
	}
}

func TestSubscriber_KeysWhatARunnerSealedAndStoresNothingRaw(t *testing.T) {
	client, device := open(t)
	store := entstore.New(client)
	open, keyer, err := entstore.DigestKeys(bytes.Repeat([]byte{6}, 32))
	if err != nil {
		t.Fatal(err)
	}
	entries := runnerEntries(t, map[string]any{
		"file.x": map[string]any{"state": "present", "sha256_sealed": sealFor(t, open.Public(), device, "file.x")},
		// Sealed for another device's file: moved here, it opens as the wrong
		// fact and is refused.
		"file.y": map[string]any{"state": "present", "sha256_sealed": sealFor(t, open.Public(), "another-device", "file.y")},
		// Not a sealed file at all, and one sealed for another purpose (a
		// password): neither is opened as a digest.
		"file.z": map[string]any{"state": "present", "sha256_sealed": "not a sealed file"},
		"file.w": map[string]any{"state": "present", "sha256_sealed": sealedPassword(t, open.Public())},
	})
	sub := entstore.NewSubscriber(store, nil, nil, entstore.WithDigestKeys(open, keyer))
	if err := handle(t, sub, facts.Batch{JobID: "job-1", DeviceID: device, Attempt: 1, ObservedAt: time.Now(), Entries: entries}); err != nil {
		t.Fatal(err)
	}
	got := storedValues(t, store, device)
	want, _ := keyer.Seal(device, "file.x", digestRaw)
	if x := got["file.x"]; x["hmac_sha256"] != want || x["hmac_key"] != keyer.ID() || x["sha256_sealed"] != nil || x["state"] != "present" {
		t.Errorf("file.x stored as %v", x)
	}
	for _, key := range []string{"file.y", "file.z", "file.w"} {
		if v := got[key]; v == nil || v["hmac_sha256"] != nil || v["sha256_sealed"] != nil || v["state"] != "present" {
			t.Errorf("%s stored as %v, want the fact without a digest", key, v)
		}
	}
}

func TestSubscriber_WithoutKeysStoresTheFactWithoutItsDigest(t *testing.T) {
	client, device := open(t)
	store := entstore.New(client)
	open, _, _ := entstore.DigestKeys(bytes.Repeat([]byte{6}, 32))
	entries := runnerEntries(t, map[string]any{
		"file.x": map[string]any{"state": "present", "sha256_sealed": sealFor(t, open.Public(), device, "file.x")},
	})
	sub := entstore.NewSubscriber(store, nil, nil)
	if err := handle(t, sub, facts.Batch{JobID: "job-1", DeviceID: device, Attempt: 1, ObservedAt: time.Now(), Entries: entries}); err != nil {
		t.Fatal(err)
	}
	if x := storedValues(t, store, device)["file.x"]; x == nil || len(x) != 1 || x["state"] != "present" {
		t.Errorf("file.x stored as %v, want the fact without any digest", x)
	}
}

func TestSubscriber_NeverStoresARawDigest(t *testing.T) {
	client, device := open(t)
	store := entstore.New(client)
	open, keyer, _ := entstore.DigestKeys(bytes.Repeat([]byte{6}, 32))
	// A Runner that sent a raw digest (an old build, or one tampered with):
	// its hash covers the raw value, which the Controller strips, so the
	// entry no longer matches its hash and is refused whole.
	value := map[string]any{"state": "present", "sha256_raw": digestRaw}
	hash, err := facts.Hash("file.x", value)
	if err != nil {
		t.Fatal(err)
	}
	entries := []facts.Entry{{Key: "file.x", Value: value, Hash: hash, Kind: facts.KindStable}}
	sub := entstore.NewSubscriber(store, nil, nil, entstore.WithDigestKeys(open, keyer))
	if err := handle(t, sub, facts.Batch{JobID: "job-1", DeviceID: device, Attempt: 1, ObservedAt: time.Now(), Entries: entries}); err != nil {
		t.Fatal(err)
	}
	rows := client.Fact.Query().AllX(context.Background())
	all, _ := json.Marshal(rows)
	if bytes.Contains(all, []byte(digestRaw)) || len(rows) != 0 {
		t.Errorf("stored %d rows, holding the raw digest: %v", len(rows), bytes.Contains(all, []byte(digestRaw)))
	}
}
