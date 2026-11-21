// Tests of a change of the facts secret on the consumer's side (digests.go's
// WithPreviousDigestKey), through the real consumer and a real SQLite store:
// a batch and a digest a Runner sealed to the previous secret's key open only
// while that key is kept, and the digest is keyed under the current keyer;
// nothing sealed to a third key opens, kept or not; and nothing whose
// Recipient header was changed opens under either key.
package entstore_test

import (
	"bytes"
	"encoding/pem"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts/entstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts/factstest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/sealed"
)

// rotationSeal is the seal keys of three facts secrets: the current one, the
// previous one a change moves away from, and a third this controller never
// held, with the current and previous secrets' keyers.
type rotationSeal struct {
	current, previous, third *sealed.PrivateKey
	keyer, previousKeyer     *facts.Keyer
}

// rotationSealKeys derives rotationSeal from three secrets, as a controller
// derives each from its PLEIADES_FACTS_DIGEST_KEY.
func rotationSealKeys(t *testing.T) rotationSeal {
	t.Helper()
	var keys rotationSeal
	var err error
	if keys.current, keys.keyer, err = entstore.DigestKeys(bytes.Repeat([]byte{6}, 32)); err != nil {
		t.Fatal(err)
	}
	if keys.previous, keys.previousKeyer, err = entstore.DigestKeys(bytes.Repeat([]byte{5}, 32)); err != nil {
		t.Fatal(err)
	}
	if keys.third, _, err = entstore.DigestKeys(bytes.Repeat([]byte{4}, 32)); err != nil {
		t.Fatal(err)
	}
	return keys
}

// kept is a consumer's options during a change of the facts secret: the
// current keys and the previous seal key beside them.
func (k rotationSeal) kept() []entstore.SubscriberOption {
	return []entstore.SubscriberOption{entstore.WithDigestKeys(k.current, k.keyer), entstore.WithPreviousDigestKey(k.previous)}
}

// notKept is a consumer's options once the previous secret is unset.
func (k rotationSeal) notKept() []entstore.SubscriberOption {
	return []entstore.SubscriberOption{entstore.WithDigestKeys(k.current, k.keyer)}
}

// wantKeyedUnderCurrent fails t unless value is file.x stored with the
// digest keyed under the current keyer and nothing sealed beside it.
func wantKeyedUnderCurrent(t *testing.T, keys rotationSeal, device string, value map[string]any) {
	t.Helper()
	want, _ := keys.keyer.Seal(device, "file.x", digestRaw)
	underPrevious, _ := keys.previousKeyer.Seal(device, "file.x", digestRaw)
	if want == underPrevious || keys.keyer.ID() == keys.previousKeyer.ID() {
		t.Fatal("the control failed: the two keyers key alike, so this test could not tell them apart")
	}
	if value["hmac_sha256"] != want || value["hmac_key"] != keys.keyer.ID() || value["sha256_sealed"] != nil || value["state"] != "present" {
		t.Errorf("file.x stored as %v, want its digest keyed under the current keyer %s", value, keys.keyer.ID())
	}
}

// wantWithoutDigest fails t unless value is file.x stored with no digest at
// all: what a sealed digest this controller cannot open leaves.
func wantWithoutDigest(t *testing.T, value map[string]any) {
	t.Helper()
	if len(value) != 1 || value["state"] != "present" {
		t.Errorf("file.x stored as %v, want the fact without any digest", value)
	}
}

// rotationCases is who a Runner sealed to and which keys the consumer holds,
// with whether what was sealed opens.
func rotationCases(keys rotationSeal) []struct {
	name   string
	opts   []entstore.SubscriberOption
	sealTo *sealed.PrivateKey
	opens  bool
} {
	return []struct {
		name   string
		opts   []entstore.SubscriberOption
		sealTo *sealed.PrivateKey
		opens  bool
	}{
		{"the previous key, kept", keys.kept(), keys.previous, true},
		{"the previous key, not kept", keys.notKept(), keys.previous, false},
		{"a third key, the previous kept", keys.kept(), keys.third, false},
		{"a third key, no previous", keys.notKept(), keys.third, false},
		// The control: the current key opens with the previous one beside it.
		{"the current key, the previous kept", keys.kept(), keys.current, true},
	}
}

func TestSubscriber_OpensABatchSealedToThePreviousKeyOnlyWhileItIsKept(t *testing.T) {
	keys := rotationSealKeys(t)
	for _, tc := range rotationCases(keys) {
		t.Run(tc.name, func(t *testing.T) {
			client, device := open(t)
			store := entstore.New(client, factstest.Keys(), factstest.Envelope())
			var logged bytes.Buffer
			sub := entstore.NewSubscriber(store, slog.New(slog.NewTextHandler(&logged, nil)), nil, tc.opts...)
			// As a Runner seals a job dispatched under one key: the batch, and
			// the digest inside it, both to that key.
			batch := facts.Batch{JobID: "job-1", DeviceID: device, Attempt: 1, ObservedAt: time.Now().UTC(),
				Entries: runnerEntries(t, map[string]any{
					"os.kernel.release": "6.1",
					"file.x":            map[string]any{"state": "present", "sha256_sealed": sealFor(t, tc.sealTo.Public(), device, "file.x")},
				})}
			if err := handle(t, sub, sealBatch(t, tc.sealTo.Public(), sealed.PurposeFactBatch, batch)); err != nil {
				t.Fatalf("Handle returned %v; a batch is stored or acknowledged, never redelivered", err)
			}
			if !tc.opens {
				if got := storedKeys(t, store, device); len(got) != 0 {
					t.Errorf("stored %v from a batch sealed to a key this controller does not hold", got)
				}
				if !strings.Contains(logged.String(), "sealed to a key this controller does not hold") {
					t.Errorf("the batch was not refused as sealed to a key this controller does not hold:\n%s", logged.String())
				}
				return
			}
			if got := storedKeys(t, store, device); len(got) != 2 {
				t.Fatalf("stored %v, want both facts", got)
			}
			wantKeyedUnderCurrent(t, keys, device, storedValues(t, store, device)["file.x"])
			// What the previous key opened is said, so an operator sees it is
			// still in use; what the current key opened is not.
			if said := strings.Contains(logged.String(), "sealed to the previous facts key"); said != (tc.sealTo == keys.previous) {
				t.Errorf("said the previous key opened the batch: %v, want %v:\n%s", said, tc.sealTo == keys.previous, logged.String())
			}
		})
	}
}

// TestSubscriber_KeysADigestSealedToThePreviousKeyOnlyWhileItIsKept: the
// digest alone, in a batch a Runner of the release before batch sealing
// published in plaintext, so only the digest's key decides.
func TestSubscriber_KeysADigestSealedToThePreviousKeyOnlyWhileItIsKept(t *testing.T) {
	keys := rotationSealKeys(t)
	for _, tc := range rotationCases(keys) {
		t.Run(tc.name, func(t *testing.T) {
			client, device := open(t)
			store := entstore.New(client, factstest.Keys(), factstest.Envelope())
			sub := entstore.NewSubscriber(store, nil, nil, tc.opts...)
			entries := runnerEntries(t, map[string]any{
				"file.x": map[string]any{"state": "present", "sha256_sealed": sealFor(t, tc.sealTo.Public(), device, "file.x")},
			})
			if err := handle(t, sub, facts.Batch{JobID: "job-1", DeviceID: device, Attempt: 1, ObservedAt: time.Now(), Entries: entries}); err != nil {
				t.Fatal(err)
			}
			value := storedValues(t, store, device)["file.x"]
			if tc.opens {
				wantKeyedUnderCurrent(t, keys, device, value)
			} else {
				wantWithoutDigest(t, value)
			}
		})
	}
}

// relabel is the sealed file text with its Recipient header changed to name
// to, as anyone holding the stream's bytes but no key can change it. It
// still parses, so what refuses it is the encryption, not the parser.
func relabel(t *testing.T, text string, to sealed.PublicKey) string {
	t.Helper()
	block, rest := pem.Decode([]byte(text))
	if block == nil || len(bytes.TrimSpace(rest)) != 0 {
		t.Fatal("not one PEM block")
	}
	block.Headers["Recipient"] = to.Fingerprint()
	out := string(pem.EncodeToMemory(block))
	if file, err := sealed.Parse([]byte(out)); err != nil || file.Recipient != to.Fingerprint() {
		t.Fatalf("the control failed: the relabelled file reads as %+v, %v", file, err)
	}
	return out
}

// TestSubscriber_OpensNothingWhoseRecipientWasChanged: a sealed file whose
// Recipient header names one of the keys this controller holds, but not the
// key it was sealed to, opens under neither key. A batch is dropped whole and
// a digest is stored as nothing, both during a change of the facts secret.
func TestSubscriber_OpensNothingWhoseRecipientWasChanged(t *testing.T) {
	keys := rotationSealKeys(t)
	for _, tc := range []struct {
		name            string
		sealedTo, named *sealed.PrivateKey
	}{
		{"sealed to the current key, named the previous", keys.current, keys.previous},
		{"sealed to the previous key, named the current", keys.previous, keys.current},
		{"sealed to a third key, named the current", keys.third, keys.current},
		{"sealed to a third key, named the previous", keys.third, keys.previous},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, device := open(t)
			store := entstore.New(client, factstest.Keys(), factstest.Envelope())
			var logged bytes.Buffer
			sub := entstore.NewSubscriber(store, slog.New(slog.NewTextHandler(&logged, nil)), nil, keys.kept()...)

			batch := facts.Batch{JobID: "job-1", DeviceID: device, Attempt: 1, ObservedAt: time.Now().UTC(),
				Entries: runnerEntries(t, map[string]any{"os.kernel.release": "6.1"})}
			relabelled := sealBatch(t, tc.sealedTo.Public(), sealed.PurposeFactBatch, batch)
			relabelled.Sealed = relabel(t, relabelled.Sealed, tc.named.Public())
			if err := handle(t, sub, relabelled); err != nil {
				t.Fatalf("Handle returned %v; a batch that will never open must be acknowledged", err)
			}
			if got := storedKeys(t, store, device); len(got) != 0 {
				t.Errorf("stored %v from a batch whose recipient was changed", got)
			}
			// Refused when it was opened, under the key it names: not by the
			// parser, and not as naming a key this controller lacks.
			if !strings.Contains(logged.String(), "do not open with the key they name") {
				t.Errorf("the relabelled batch was not refused by its encryption:\n%s", logged.String())
			}

			// A relabelled digest inside a batch that opens: the fact is
			// stored, without it.
			digestBatch := facts.Batch{JobID: "job-1", DeviceID: device, Attempt: 1, ObservedAt: time.Now().UTC(),
				Entries: runnerEntries(t, map[string]any{
					"file.x": map[string]any{"state": "present", "sha256_sealed": relabel(t, sealFor(t, tc.sealedTo.Public(), device, "file.x"), tc.named.Public())},
				})}
			if err := handle(t, sub, sealBatch(t, keys.current.Public(), sealed.PurposeFactBatch, digestBatch)); err != nil {
				t.Fatal(err)
			}
			wantWithoutDigest(t, storedValues(t, store, device)["file.x"])
		})
	}
}
