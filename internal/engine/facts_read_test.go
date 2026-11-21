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

// TestAnswerFacts_AbsentOnlyWhereAMethodThatEmitsTheKeyLooked: a key the
// store holds is present; one it lacks is absent only when an observer
// declares it, and unknown when no observer does or none has observed.
func TestAnswerFacts_AbsentOnlyWhereAMethodThatEmitsTheKeyLooked(t *testing.T) {
	registerFactMethods(t)
	now := time.Now()
	current := map[string]facts.Entry{"os.kernel.release": {Key: "os.kernel.release", Value: "6.1", LastSeenAt: now.Add(-time.Minute), Kind: facts.KindStable}}
	keys := []string{"os.kernel.release", "test.uptime", "undeclared.key"}
	got := engine.AnswerFacts(keys, current, []string{"enginefacts.gather"}, now)
	if got["os.kernel.release"].State != engine.FactPresent || got["os.kernel.release"].Value != "6.1" {
		t.Errorf("a held key = %+v, want present", got["os.kernel.release"])
	}
	if got["test.uptime"].State != engine.FactAbsent {
		t.Errorf("a key the observer declares and the store lacks = %+v, want absent", got["test.uptime"])
	}
	if got["undeclared.key"].State != engine.FactUnknown {
		t.Errorf("a key no observer declares = %+v, want unknown", got["undeclared.key"])
	}
	if none := engine.AnswerFacts(keys, nil, nil, now); none["test.uptime"].State != engine.FactUnknown {
		t.Errorf("a device nothing observed = %+v, want unknown", none["test.uptime"])
	}
}
