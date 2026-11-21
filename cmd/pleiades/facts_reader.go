// The Crawl CLI's read side of the fact store (Phase 41): what a run reads of
// the devices' stored facts, through engine.FactReader.
package main

import (
	"context"
	"errors"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
)

// projectFactReader reads the project's .pleiades/facts, opening it on each
// read and creating nothing, so a check (which stores nothing) reads what
// earlier runs stored and a project no run has stored a fact in yet reads as
// devices with no facts. A store that is there but cannot be read is an
// error, never an empty answer, and so is a store whose key the project no
// longer has: what is stored is sealed under it (factKeysToRead).
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

// Current implements engine.FactReader.
func (r projectFactReader) Current(ctx context.Context, device string) ([]facts.Entry, error) {
	store, err := r.open()
	if store == nil || err != nil {
		return nil, err
	}
	return store.Current(ctx, device)
}

// History implements engine.FactReader.
func (r projectFactReader) History(ctx context.Context, device, key string) ([]facts.Entry, error) {
	store, err := r.open()
	if store == nil || err != nil {
		return nil, err
	}
	return store.History(ctx, device, key)
}
