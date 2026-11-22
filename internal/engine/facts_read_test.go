// Package engine_test holds the fact read port tests: a wired reader answers
// with each value's age, an absent key and an unknown one are told apart, a
// store that fails is an error, and an unwired executor refuses.
package engine_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
)

// fakeReader answers from a fixed set of answers by device and records what
// it was asked.
type fakeReader struct {
	mu      sync.Mutex
	answers map[string]map[string]engine.FactAnswer
	err     error
	asked   []string
	keys    [][]string
}

func (f *fakeReader) ReadFacts(_ context.Context, device string, keys []string) (map[string]engine.FactAnswer, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, device)
	f.keys = append(f.keys, append([]string(nil), keys...))
	if f.err != nil {
		return nil, f.err
	}
	out := map[string]engine.FactAnswer{}
	for _, k := range keys {
		if a, ok := f.answers[device][k]; ok {
			out[k] = a
		}
	}
	return out, nil
}

// TestReadFact_CarriesTheAgeOfTheLastConfirmation: a read returns the value and
// its age from when it was last confirmed, never from when it changed.
func TestReadFact_CarriesTheAgeOfTheLastConfirmation(t *testing.T) {
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	reader := &fakeReader{answers: map[string]map[string]engine.FactAnswer{"dev-1": {
		"os.kernel.release": {State: engine.FactPresent, Value: "6.1", SeenAt: now.Add(-3 * time.Hour), Kind: facts.KindStable},
	}}}
	x := newCheckExecutor(factFleet(), engine.WithFactReader(reader))
	got, err := x.ReadFact(context.Background(), "dev-1", "os.kernel.release", now)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Known || got.State != engine.FactPresent || got.Value != "6.1" || got.Age != 3*time.Hour {
		t.Fatalf("ReadFact = %+v, want the value with the age of its last confirmation (3h)", got)
	}
}

// TestReadFact_AnAbsentKeyAndAnUnknownOneAreToldApart: a key a gathered device
// lacks is absent, one nobody looked for is unknown, and neither is an error.
func TestReadFact_AnAbsentKeyAndAnUnknownOneAreToldApart(t *testing.T) {
	reader := &fakeReader{answers: map[string]map[string]engine.FactAnswer{"dev-1": {"os.codename": {State: engine.FactAbsent}}}}
	x := newCheckExecutor(factFleet(), engine.WithFactReader(reader))
	absent, err := x.ReadFact(context.Background(), "dev-1", "os.codename", time.Now())
	if err != nil || absent.Known || absent.State != engine.FactAbsent {
		t.Fatalf("ReadFact of an absent key = %+v, %v; want absent and no error", absent, err)
	}
	unknown, err := x.ReadFact(context.Background(), "dev-1", "os.kernel.release", time.Now())
	if err != nil || unknown.Known || unknown.State != engine.FactUnknown {
		t.Fatalf("ReadFact of a key the reader left out = %+v, %v; want unknown and no error", unknown, err)
	}
}

// TestReadFact_AStoreThatFailsIsAnErrorNotAnAbsence: a read the store cannot
// make is reported, never read as a device with no facts.
func TestReadFact_AStoreThatFailsIsAnErrorNotAnAbsence(t *testing.T) {
	boom := errors.New("the database is down")
	x := newCheckExecutor(factFleet(), engine.WithFactReader(&fakeReader{err: boom}))
	if _, err := x.ReadFact(context.Background(), "dev-1", "os.kernel.release", time.Now()); !errors.Is(err, boom) {
		t.Fatalf("error = %v, want the store's error", err)
	}
}

// TestReadFact_AnUnwiredExecutorRefusesToAnswer: without a reader, a read is an
// error naming the missing wire, never an empty answer.
func TestReadFact_AnUnwiredExecutorRefusesToAnswer(t *testing.T) {
	x := newCheckExecutor(factFleet())
	if _, err := x.ReadFact(context.Background(), "dev-1", "os.kernel.release", time.Now()); !errors.Is(err, engine.ErrNoFactReader) {
		t.Fatalf("error = %v, want ErrNoFactReader", err)
	}
}

// TestAnswerFacts_AStoreAnswersPresentOrUnknownNeverAbsent: a key the store
// holds is present; every other key is unknown, whatever observed the device,
// since a store keeps what was emitted and not what was looked for.
func TestAnswerFacts_AStoreAnswersPresentOrUnknownNeverAbsent(t *testing.T) {
	now := time.Now()
	current := map[string]facts.Entry{"os.kernel.release": {Key: "os.kernel.release", Value: "6.1", LastSeenAt: now.Add(-time.Minute), Kind: facts.KindStable}}
	got := engine.AnswerFacts([]string{"os.kernel.release", "test.uptime", "undeclared.key"}, current, now)
	if got["os.kernel.release"].State != engine.FactPresent || got["os.kernel.release"].Value != "6.1" {
		t.Errorf("a held key = %+v, want present", got["os.kernel.release"])
	}
	for _, key := range []string{"test.uptime", "undeclared.key"} {
		if got[key].State != engine.FactUnknown {
			t.Errorf("%s, which the store does not hold = %+v, want unknown", key, got[key])
		}
	}
}
