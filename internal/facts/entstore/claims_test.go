// Package entstore_test holds the subscriber tests for the claim check: accepted, refused and failing claim stores.
package entstore_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts/entstore"
)

// fakeClaims records each claim it is asked about and answers from a script:
// a refusal reason, or an error.
type fakeClaims struct {
	asked   []facts.Claim
	refusal string
	err     error
}

func (f *fakeClaims) ClaimBatch(_ context.Context, _, _ string, next facts.Claim) (string, error) {
	f.asked = append(f.asked, next)
	return f.refusal, f.err
}

// TestSubscriber_HoldsEachBatchToItsDevicesClaim: the batch's attempt, Runner
// and order reach the claim store as the batch carries them; an accepted batch
// is stored; a refused one is dropped whole, said at warn, and not redelivered;
// a claim store that fails redelivers the batch and stores nothing.
func TestSubscriber_HoldsEachBatchToItsDevicesClaim(t *testing.T) {
	observed := map[string]any{"host.name": "web1", "Package.openssh": "9.6"}
	batch := func(device string) facts.Batch {
		return facts.Batch{JobID: "job-1", DeviceID: device, Attempt: 2, ObservedAt: time.Now(),
			Entries: prepared(t, observed),
			By:      facts.Provenance{Node: "tasks[3]", Method: "facts.gather", Runner: "runner-a", Order: facts.Order{Source: "lease:e1", Token: 7}}}
	}

	t.Run("accepted: the claim sees the batch's place and the batch is stored", func(t *testing.T) {
		client, device := open(t)
		store := entstore.New(client)
		claims := &fakeClaims{}
		sub := entstore.NewSubscriber(store, nil, nil, entstore.WithClaims(claims))
		if err := handle(t, sub, batch(device)); err != nil {
			t.Fatal(err)
		}
		want := facts.Claim{Attempt: 2, Runner: "runner-a", Order: facts.Order{Source: "lease:e1", Token: 7}}
		if len(claims.asked) != 1 || claims.asked[0] != want {
			t.Fatalf("the claim store was asked %+v, want %+v", claims.asked, want)
		}
		if got := storedMethods(t, store, device); len(got) == 0 {
			t.Fatalf("an accepted batch stored nothing")
		}
	})

	t.Run("refused: dropped whole, said at warn, acknowledged", func(t *testing.T) {
		client, device := open(t)
		store := entstore.New(client)
		claims := &fakeClaims{refusal: "the batch names a Runner other than the one attempt 2's claim holds"}
		var logged bytes.Buffer
		sub := entstore.NewSubscriber(store, slog.New(slog.NewTextHandler(&logged, nil)), nil, entstore.WithClaims(claims))
		if err := handle(t, sub, batch(device)); err != nil {
			t.Fatalf("a refused batch must be acknowledged, got %v", err)
		}
		if got := storedMethods(t, store, device); len(got) != 0 {
			t.Fatalf("a refused batch stored %v", got)
		}
		log := logged.String()
		if !strings.Contains(log, "level=WARN") || !strings.Contains(log, "runner=runner-a") {
			t.Errorf("the refusal is not said at warn with its Runner:\n%s", log)
		}
		if strings.Contains(log, "web1") || strings.Contains(log, "9.6") {
			t.Errorf("a log line holds a fact's value:\n%s", log)
		}
	})

	t.Run("a failing claim store redelivers and stores nothing", func(t *testing.T) {
		client, device := open(t)
		store := entstore.New(client)
		claims := &fakeClaims{err: errors.New("the database is down")}
		sub := entstore.NewSubscriber(store, nil, nil, entstore.WithClaims(claims))
		if err := handle(t, sub, batch(device)); err == nil {
			t.Fatal("a failed claim check must return the error so the bus redelivers")
		}
		if got := storedMethods(t, store, device); len(got) != 0 {
			t.Fatalf("a batch whose claim could not be checked stored %v", got)
		}
	})
}
