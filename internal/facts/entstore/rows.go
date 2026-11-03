// Package entstore: reading the Fact table back as facts.Entry values, and
// the encodings its JSON columns use.
package entstore

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	entdevice "github.com/Subject-Void-LLC/the-pleiades/internal/ent/device"
	entfact "github.com/Subject-Void-LLC/the-pleiades/internal/ent/fact"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
)

// Current implements facts.Store: for each of device's keys, the value most
// recently confirmed, by run order where the runs compare (the later row on
// a tie). A volatile row takes part like any other.
func (s *Store) Current(ctx context.Context, device string) ([]facts.Entry, error) {
	rows, err := s.client.Fact.Query().
		Where(entfact.HasDeviceWith(entdevice.DeviceIDEQ(device))).
		Order(ent.Asc(entfact.FieldID)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("facts: %w", err)
	}
	current := map[string]facts.Entry{}
	for _, row := range rows {
		e := entry(row)
		cur, ok := current[e.Key]
		if !ok {
			current[e.Key] = e
			continue
		}
		if after, _ := facts.Later(cur.LastBy.Order, cur.LastSeenAt, e.LastBy.Order, e.LastSeenAt); !after {
			current[e.Key] = e
		}
	}
	out := make([]facts.Entry, 0, len(current))
	for _, e := range current {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

// History implements facts.Store: every row of device's key in run order
// (facts.InRunOrder).
func (s *Store) History(ctx context.Context, device, key string) ([]facts.Entry, error) {
	rows, err := s.client.Fact.Query().
		Where(entfact.KeyEQ(key), entfact.HasDeviceWith(entdevice.DeviceIDEQ(device))).
		Order(ent.Asc(entfact.FieldID)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("facts: %w", err)
	}
	var stable, volatile []facts.Entry
	for _, row := range rows {
		if row.Kind == entfact.KindVolatile {
			volatile = append(volatile, entry(row))
			continue
		}
		stable = append(stable, entry(row))
	}
	return append(facts.InRunOrder(stable), volatile...), nil
}

// Verify implements facts.Store: device's chain over its stable rows, in the
// order they were written.
func (s *Store) Verify(ctx context.Context, device string) (facts.Verdict, error) {
	rows, err := s.client.Fact.Query().
		Where(entfact.HasDeviceWith(entdevice.DeviceIDEQ(device)), entfact.KindEQ(entfact.KindStable)).
		Order(ent.Asc(entfact.FieldID)).
		All(ctx)
	if err != nil {
		return facts.Verdict{}, fmt.Errorf("facts: %w", err)
	}
	links := make([]facts.Link, len(rows))
	for i, row := range rows {
		e := entry(row)
		links[i] = facts.Link{Key: e.Key, Value: e.Value, Hash: e.Hash, ObservedAt: e.ObservedAt, By: e.By, Chain: row.Chain}
		if row.ChainPrev != nil {
			links[i].Prev = *row.ChainPrev
		}
	}
	return facts.VerifyChain(links), nil
}

// payloadKey is where a JSON column holds its value: the value's canonical
// JSON as text, so a number reads back exactly as it was stored (a JSON
// column decodes numbers as float64, which rounds an integer past 2^53, and
// an order token is a uint64).
const payloadKey = "json"

// mustCanonical is a prepared value's canonical JSON text. Prepare already
// encoded it, so a failure here cannot happen; it stores null rather than
// panicking if it ever does.
func mustCanonical(v any) string {
	canon, err := facts.Canonical(v)
	if err != nil {
		return "null"
	}
	return string(canon)
}

// encode is a provenance as its column holds it.
func encode(p facts.Provenance) (map[string]any, error) {
	raw, err := json.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("facts: %w", err)
	}
	return map[string]any{payloadKey: string(raw)}, nil
}

// decodeProvenance reads a provenance column; one that is empty or does
// not decode is unknown provenance.
func decodeProvenance(col map[string]any) facts.Provenance {
	var p facts.Provenance
	if text, ok := col[payloadKey].(string); ok {
		_ = json.Unmarshal([]byte(text), &p)
	}
	return p
}

// entry is a row as a facts.Entry.
func entry(row *ent.Fact) facts.Entry {
	seen := row.LastSeenAt
	if seen.IsZero() {
		seen = row.CreatedAt
	}
	var value any
	if text, ok := row.Payload[payloadKey].(string); ok {
		dec := json.NewDecoder(strings.NewReader(text))
		dec.UseNumber()
		if err := dec.Decode(&value); err != nil {
			value = nil
		}
	}
	kind := facts.KindStable
	if row.Kind == entfact.KindVolatile {
		kind = facts.KindVolatile
	}
	by := decodeProvenance(row.Provenance)
	lastBy := decodeProvenance(row.LastProvenance)
	if lastBy == (facts.Provenance{}) {
		lastBy = by
	}
	return facts.Entry{Key: row.Key, Value: value, Hash: row.Hash, ObservedAt: row.CreatedAt.UTC(), LastSeenAt: seen.UTC(), Kind: kind, By: by, LastBy: lastBy}
}
