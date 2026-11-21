// Tests of the Controller's facts digest key (fact_digest_keys.go) as main
// wires it: read from PLEIADES_FACTS_DIGEST_KEY, refused when malformed, and
// turned into the Worker's and the facts consumer's options, which key a
// digest a Runner sealed to this Controller, and during a change of the key
// one sealed to PLEIADES_FACTS_DIGEST_KEY_PREVIOUS's.
package main

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts/entstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/sealed"
)

// digestSecret is a 32-byte digest secret.
var digestSecret = []byte("a 32-byte facts digest secret!!!")

// pinDigest is the SHA-256 of "pin1234\n".
const pinDigest = "3f773b3737a2f378fa4589ecacc53598168149f40fa0294c8580ee8fa4455b96"

func TestLoadFactDigestKeys_ReadsTheSecretOrSaysThereIsNone(t *testing.T) {
	t.Setenv(factDigestKeyEnv, "")
	t.Setenv(factDigestKeyPreviousEnv, "")
	if keys, err := loadFactDigestKeys(quietLogger()); keys != (factDigestKeys{}) || err != nil {
		t.Errorf("no secret: %+v %v, want no keys and no error", keys, err)
	}
	t.Setenv(factDigestKeyEnv, "abc")
	if _, err := loadFactDigestKeys(quietLogger()); err == nil {
		t.Error("a malformed secret was accepted")
	}
	t.Setenv(factDigestKeyEnv, base64.StdEncoding.EncodeToString(digestSecret))
	keys, err := loadFactDigestKeys(quietLogger())
	if err != nil || keys.open == nil || keys.keyer == nil || keys.previous != nil {
		t.Fatalf("a valid secret: %+v %v, want its keys and no previous key", keys, err)
	}
	// Every replica given the secret derives these same keys.
	wantOpen, wantKeyer, _ := entstore.DigestKeys(digestSecret)
	if keys.open.Public().String() != wantOpen.Public().String() || keys.keyer.ID() != wantKeyer.ID() {
		t.Error("the controller's keys are not the ones the secret derives")
	}
}

func TestLoadFactDigestKeys_ReadsThePreviousSealKeyOrRefusesItByName(t *testing.T) {
	encode := base64.StdEncoding.EncodeToString
	t.Setenv(factDigestKeyEnv, encode(digestSecret))
	for name, value := range map[string]string{
		"not base64":         "abc",
		"too short":          encode([]byte("sixteen bytes!!!")),
		"the current secret": encode(digestSecret),
	} {
		t.Setenv(factDigestKeyPreviousEnv, value)
		if _, err := loadFactDigestKeys(quietLogger()); err == nil || !strings.Contains(err.Error(), factDigestKeyPreviousEnv) {
			t.Errorf("%s: %v, want a refusal naming %s", name, err, factDigestKeyPreviousEnv)
		}
	}
	t.Setenv(factDigestKeyPreviousEnv, encode(previousDigestSecret))
	keys, err := loadFactDigestKeys(quietLogger())
	if err != nil || keys.previous == nil {
		t.Fatalf("a valid previous secret: %+v %v", keys, err)
	}
	// The previous secret's key pair, which every replica derives alike; the
	// keyer stays the current secret's.
	wantPrevious, _, _ := entstore.DigestKeys(previousDigestSecret)
	_, wantKeyer, _ := entstore.DigestKeys(digestSecret)
	if keys.previous.Public().String() != wantPrevious.Public().String() || keys.keyer.ID() != wantKeyer.ID() {
		t.Error("the previous key pair is not the previous secret's, or the keyer is not the current secret's")
	}
}

// sealedDigestBatch is a batch for dev-1 holding file.x with pinDigest sealed
// to pub, as a Runner publishes it, named by the built-in method that
// gathers it (the consumer keeps a built-in fact only from a built-in method).
func sealedDigestBatch(t *testing.T, pub sealed.PublicKey) []byte {
	t.Helper()
	plaintext, _ := facts.SealPlaintext("job-1", "dev-1", "file.x", pinDigest)
	file, err := sealed.SealToKey(pub, sealed.PurposeFactDigest, plaintext)
	if err != nil {
		t.Fatal(err)
	}
	entries, _, _ := facts.Prepare(map[string]any{"file.x": map[string]any{"state": "present", "sha256_sealed": string(file)}}, facts.Guard{AllowSealed: true})
	data, _ := json.Marshal(facts.Batch{JobID: "job-1", DeviceID: "dev-1", Attempt: 1, ObservedAt: time.Now(), Entries: entries,
		By: facts.Provenance{Method: "facts.gather"}})
	return data
}

func TestFactDigestOptions_KeyWhatARunnerSealedThroughTheConsumerMainBuilds(t *testing.T) {
	if worker, consumer := factDigestOptions(factDigestKeys{}); worker != nil || consumer != nil {
		t.Error("no keys gave options")
	}
	open, keyer, err := entstore.DigestKeys(digestSecret)
	if err != nil {
		t.Fatal(err)
	}
	previous, _, err := entstore.DigestKeys(previousDigestSecret)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := keyer.Seal("dev-1", "file.x", pinDigest)
	for _, tc := range []struct {
		name string
		keys factDigestKeys
		// sealTo is the key the Runner sealed to.
		sealTo *sealed.PrivateKey
		// keyed is whether the stored fact holds the keyed digest.
		keyed bool
	}{
		{"the current key", factDigestKeys{open: open, keyer: keyer}, open, true},
		{"the previous key during a change", factDigestKeys{open: open, keyer: keyer, previous: previous}, previous, true},
		{"the previous key once it is unset", factDigestKeys{open: open, keyer: keyer}, previous, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			worker, consumer := factDigestOptions(tc.keys)
			if len(worker) != 1 {
				t.Fatalf("%d worker options, want one: the current key's public half", len(worker))
			}
			store := &recordingStore{}
			sub, err := newFactsSubscriber(store, nil, nil, consumer...)
			if err != nil {
				t.Fatal(err)
			}
			if err := sub.Handle(factsEventFromJSON(t, "e1", sealedDigestBatch(t, tc.sealTo.Public()))); err != nil {
				t.Fatal(err)
			}
			if len(store.got) != 1 {
				t.Fatalf("stored %+v", store.got)
			}
			v, _ := store.got[0].Value.(map[string]any)
			if v["sha256_sealed"] != nil {
				t.Errorf("stored %v, holding the sealed digest", v)
			}
			// Keyed by the current keyer whichever key opened it; without
			// the key it was sealed to, the fact is stored without it.
			if got := v["hmac_sha256"] == want && v["hmac_key"] == keyer.ID(); got != tc.keyed {
				t.Errorf("stored %v, want the current keyed digest: %v", v, tc.keyed)
			}
		})
	}
}
