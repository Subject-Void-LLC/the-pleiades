// Package entstore: a Runner's batch holds a built-in fact only from a
// built-in method.
//
// A Runner refuses at load an external Collection that declares a fact a
// built-in holds (collection.CheckExternalFact), and the engine stores only
// what the emitting method declares. The Controller holds a batch to the same
// rule rather than trusting that every Runner does (a Runner of an older
// release, during an upgrade, does not): a fact a built-in holds is stored
// only when a built-in method emitted it.
package entstore

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// BuiltinMethods reads the names of the methods in a module catalog
// (internal/api/wellknown's ModuleCatalog).
func BuiltinMethods(catalog []byte) (map[string]bool, error) {
	var manifests map[string]json.RawMessage
	if err := json.Unmarshal(catalog, &manifests); err != nil {
		return nil, fmt.Errorf("facts: reading the module catalog: %w", err)
	}
	out := make(map[string]bool, len(manifests))
	for name := range manifests {
		out[name] = true
	}
	return out, nil
}

// WithBuiltinProvenance drops, from a batch whose emitting method is not one
// of builtins, every entry a built-in holds: a key the catalog declares
// (WithDeclarations) or one in collection.ReservedFactFamilies, compared
// without case.
func WithBuiltinProvenance(builtins map[string]bool) SubscriberOption {
	return func(s *Subscriber) { s.builtins = builtins }
}

// heldByBuiltin reports whether key is a fact a built-in holds.
func (s *Subscriber) heldByBuiltin(key string) bool {
	if s.declared != nil {
		if _, ok := s.declared(key); ok {
			return true
		}
	}
	folded := strings.ToLower(key)
	for _, family := range collection.ReservedFactFamilies {
		if strings.HasPrefix(folded, family) {
			return true
		}
	}
	return false
}

// builtinProvenance returns entries without those a non-built-in method
// could not have been allowed to write, and the keys it dropped.
func (s *Subscriber) builtinProvenance(method string, entries []facts.Entry) ([]facts.Entry, []string) {
	if s.builtins == nil || s.builtins[method] {
		return entries, nil
	}
	kept := entries[:0:0]
	var dropped []string
	for _, e := range entries {
		if s.heldByBuiltin(e.Key) {
			dropped = append(dropped, e.Key)
			continue
		}
		kept = append(kept, e)
	}
	return kept, dropped
}
