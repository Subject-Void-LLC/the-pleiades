// Package playbook's composite Source: several sources answering as one.
//
// A deployment can have playbooks in two unrelated places. PLAYBOOK_DIR is
// a directory an operator mounted; a Project is a repository the controller
// cloned. Both are playbooks and a template names one without caring which
// it came from, so the catalog and the dispatcher should see a single list
// rather than each learning about both.
package playbook

import (
	"context"
	"errors"
	"fmt"
	"sort"
)

// MultiSource resolves against several sources in order.
type MultiSource struct {
	sources []Source
}

// NewMultiSource combines sources, skipping nil ones so a composition root
// can pass an optional source without a conditional.
func NewMultiSource(sources ...Source) *MultiSource {
	kept := make([]Source, 0, len(sources))
	for _, s := range sources {
		if s != nil {
			kept = append(kept, s)
		}
	}
	return &MultiSource{sources: kept}
}

// Get returns the first source's answer for id.
//
// A not-found from one source is not an answer, it is that source declining,
// so the search continues. Any OTHER error stops it: a directory that cannot
// be read is an outage, and silently falling through to the next source
// would turn it into "no such playbook", which is the wrong thing to tell
// somebody and the wrong thing to do about it.
func (m *MultiSource) Get(ctx context.Context, id string) ([]byte, error) {
	for _, s := range m.sources {
		body, err := s.Get(ctx, id)
		if err == nil {
			return body, nil
		}
		if !errors.Is(err, ErrNotFound) {
			return nil, err
		}
	}
	return nil, fmt.Errorf("%w: no playbook %q in any configured source", ErrNotFound, id)
}

// List returns every id across every source, sorted and deduplicated.
//
// Deduplicated because two sources can legitimately offer the same id and a
// catalog rendering it twice is a chooser with two identical rows. Get's
// order decides which one actually runs; this only decides what is offered.
func (m *MultiSource) List(ctx context.Context) ([]string, error) {
	seen := map[string]struct{}{}
	var out []string
	for _, s := range m.sources {
		ids, err := s.List(ctx)
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			if _, dup := seen[id]; dup {
				continue
			}
			seen[id] = struct{}{}
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out, nil
}
