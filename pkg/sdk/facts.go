// Package sdk: which of a call's recorded values are facts (Phase 41).
//
// A method records a fact with RunbookContext.EmitFact and a statistic with
// SetStat. Both land in the call's stats, which is what register: and a
// report read, so a fact is readable the way it always was. EmitFact also
// names the key under StatFactKeys, and the engine takes that list off the
// result and stores those keys' values as the device's facts, where a real
// run's are kept beyond the run. The list rides the stats in process and
// across a child process boundary alike, so neither needs a field of its own.
package sdk

import (
	"fmt"
	"slices"
)

// StatFactKeys is the reserved stat naming which of a call's stats are facts:
// a list of keys, in the order first emitted. The engine takes it off every
// result.
const StatFactKeys = "fact_keys"

// NoteFactKey returns keys with key added once, for a context's EmitFact to
// record under StatFactKeys as a []any of strings, the shape JSON gives back.
func NoteFactKey(keys []any, key string) []any {
	if slices.Contains(keys, any(key)) {
		return keys
	}
	return append(keys, key)
}

// ParseFactKeys reads a StatFactKeys value, as a context writes it in
// process ([]any of strings) or as JSON gives it back across a child process
// boundary (the same). Anything else, or a key that is not a string, is an
// error: the engine then stores none of the call's facts.
func ParseFactKeys(v any) ([]string, error) {
	list, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("sdk: %s is a %T, not a list", StatFactKeys, v)
	}
	out := make([]string, 0, len(list))
	for _, item := range list {
		key, ok := item.(string)
		if !ok || key == "" {
			return nil, fmt.Errorf("sdk: a %s entry is a %T, not a fact name", StatFactKeys, item)
		}
		out = append(out, key)
	}
	return out, nil
}
