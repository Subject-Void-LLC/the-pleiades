// Package engine_test holds the fact read port tests: a wired reader answers
// with each value's age, an absent key is unknown, and an unwired executor refuses.
package engine_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
)

// fakeReader answers from a fixed set and records what it was asked.
type fakeReader struct {
	entries []facts.Entry
	err     error
	asked   []string
}

func (f *fakeReader) Current(_ context.Context, device string) ([]facts.Entry, error) {
	f.asked = append(f.asked, device)
	return f.entries, f.err
}

func (f *fakeReader) History(_ context.Context, device, key string) ([]facts.Entry, error) {
	return nil, nil
}

// TestReadFact_CarriesTheAgeOfTheLastConfirmation: a read returns the value and
// its age from when it was last confirmed, never from when it changed.
func TestReadFact_CarriesTheAgeOfTheLastConfirmation(t *testing.T) {
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	reader := &fakeReader{entries: []facts.Entry{
		{Key: "os.kernel.release", Value: "6.1", ObservedAt: now.Add(-48 * time.Hour), LastSeenAt: now.Add(-3 * time.Hour)},
	}}
	x := newCheckExecutor(factFleet(), engine.WithFactReader(reader))
	got, err := x.ReadFact(context.Background(), "dev-1", "os.kernel.release", now)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Known || got.Value != "6.1" || got.Age != 3*time.Hour {
		t.Fatalf("ReadFact = %+v, want the value with the age of its last confirmation (3h)", got)
	}
}

// TestReadFact_AnAbsentKeyIsUnknownNotAnError: a device with no such fact says
// so, which a condition can tell apart from a failed read.
func TestReadFact_AnAbsentKeyIsUnknownNotAnError(t *testing.T) {
	x := newCheckExecutor(factFleet(), engine.WithFactReader(&fakeReader{}))
	got, err := x.ReadFact(context.Background(), "dev-1", "os.kernel.release", time.Now())
	if err != nil || got.Known {
		t.Fatalf("ReadFact of an absent key = %+v, %v; want Known false and no error", got, err)
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
