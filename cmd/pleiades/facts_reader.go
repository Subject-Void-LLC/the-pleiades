// The Crawl CLI's read side of the fact store (Phase 41): what a run reads of
// the devices' stored facts, through engine.FactReader.
package main

import (
	"context"
	"errors"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
)

// projectFactReader reads the project's .pleiades/facts, opening it on each
// read and creating nothing, so a check (which stores nothing) reads what
// earlier runs stored. A run reads each device once (the engine's snapshot),
// and that read opens every sealed line the device has, since a line's key
// is inside its seal (internal/facts' Lookup says what it costs).
//
// A project no fact was ever stored in (no directory, or one a run made and
// never wrote) has no stored facts: each key is unknown. A
// project that stored facts and has lost its key gives every device of a run
// one answer: none can be read, a device with no files of its own included,
// since what the project knew of it may be sealed under the key that is gone.
// A store that is there but cannot be read (another key sealed it, a line was
// edited, a release before sealing wrote it) is an error for the device whose
// files do not open, never an empty answer; every answer short of present or
// absent fails the condition reading it (internal/engine's facts_view.go).
type projectFactReader struct {
	dir string
	// keys opens what the store holds.
	keys *facts.Keys
}

var _ engine.FactReader = projectFactReader{}

// open is the store, or nil when no run has made one yet.
func (r projectFactReader) open() (*facts.FileStore, error) {
	store, err := facts.OpenFileStore(r.dir, r.keys)
	if errors.Is(err, facts.ErrNoStore) {
		return nil, nil
	}
	return store, err
}

// ReadFacts implements engine.FactReader.
func (r projectFactReader) ReadFacts(ctx context.Context, device string, keys []string) (map[string]engine.FactAnswer, error) {
	store, err := r.open()
	if store == nil || err != nil {
		return nil, err
	}
	if keyErr := r.keys.Ready(); keyErr != nil {
		holds, err := store.HoldsAny()
		if err != nil {
			return nil, err
		}
		if holds {
			return nil, keyErr
		}
		// Nothing was ever stored, so nothing is sealed under a key that is
		// gone: every key of every device is unknown.
		return nil, nil
	}
	lookup, err := store.Lookup(ctx, device, keys)
	if err != nil {
		return nil, err
	}
	return engine.AnswerFacts(keys, lookup.Current, time.Now()), nil
}
