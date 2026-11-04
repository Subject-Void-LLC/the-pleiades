// Tests of the Controller's facts digest key (fact_digest_keys.go) as main
// wires it: read from PLEIADES_FACTS_DIGEST_KEY, refused when malformed, and
// turned into the Worker's and the facts consumer's options, which key a
// digest a Runner sealed to this Controller.
package main

import (
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
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
	if open, keyer, err := loadFactDigestKeys(quietLogger()); open != nil || keyer != nil || err != nil {
		t.Errorf("no secret: %v %v %v, want no keys and no error", open, keyer, err)
	}
	t.Setenv(factDigestKeyEnv, "abc")
	if _, _, err := loadFactDigestKeys(quietLogger()); err == nil {
		t.Error("a malformed secret was accepted")
	}
	t.Setenv(factDigestKeyEnv, base64.StdEncoding.EncodeToString(digestSecret))
	open, keyer, err := loadFactDigestKeys(quietLogger())
	if err != nil || open == nil || keyer == nil {
		t.Fatalf("a valid secret: %v", err)
	}
	// Every replica given the secret derives these same keys.
	wantOpen, wantKeyer, _ := entstore.DigestKeys(digestSecret)
	if open.Public().String() != wantOpen.Public().String() || keyer.ID() != wantKeyer.ID() {
		t.Error("the controller's keys are not the ones the secret derives")
	}
}

func TestFactDigestOptions_KeyWhatARunnerSealedThroughTheConsumerMainBuilds(t *testing.T) {
	if worker, consumer := factDigestOptions(nil, nil); worker != nil || consumer != nil {
		t.Error("no keys gave options")
	}
	open, keyer, err := entstore.DigestKeys(digestSecret)
	if err != nil {
		t.Fatal(err)
	}
	worker, consumer := factDigestOptions(open, keyer)
	if len(worker) != 1 || len(consumer) != 1 {
		t.Fatalf("%d worker and %d consumer options, want one each", len(worker), len(consumer))
	}
	store := &recordingStore{}
	sub, err := newFactsSubscriber(store, nil, nil, consumer...)
	if err != nil {
		t.Fatal(err)
	}
	plaintext, _ := facts.SealPlaintext("job-1", "dev-1", "file.x", pinDigest)
	file, err := sealed.SealToKey(open.Public(), sealed.PurposeFactDigest, plaintext)
	if err != nil {
		t.Fatal(err)
	}
	entries, _, _ := facts.Prepare(map[string]any{"file.x": map[string]any{"state": "present", "sha256_sealed": string(file)}}, facts.Guard{AllowSealed: true})
	data, _ := json.Marshal(facts.Batch{JobID: "job-1", DeviceID: "dev-1", Attempt: 1, ObservedAt: time.Now(), Entries: entries})
	if err := sub.Handle(event.Event{ID: "e1", Type: facts.EventType, Data: data}); err != nil {
		t.Fatal(err)
	}
	want, _ := keyer.Seal("dev-1", "file.x", pinDigest)
	if len(store.got) != 1 {
		t.Fatalf("stored %+v", store.got)
	}
	if v, _ := store.got[0].Value.(map[string]any); v["hmac_sha256"] != want || v["hmac_key"] != keyer.ID() || v["sha256_sealed"] != nil {
		t.Errorf("stored %v, want the controller's keyed digest", v)
	}
}
