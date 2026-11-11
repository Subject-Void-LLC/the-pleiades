// Package entstore: a Runner's batch holding a built-in fact from a method
// this Controller's catalog lacks (FP 1020).
//
// A Runner refuses at load an external Collection that declares a fact a
// built-in holds (collection.CheckExternalFact, from release 0.4.0), and the
// engine stores only what the emitting method declares. Which methods are
// built-in is release data: a Runner of a newer release runs a built-in this
// Controller's catalog does not name, and one of an older release a built-in
// this release renamed. So the Controller does not hold a batch to its own
// catalog. It stores every fact and notes, at info, the ones its own release
// reserves for a built-in (LESSONS 415, 422). A batch's method is the batch's
// own claim, so a check here would stop no Runner that names a catalog
// method, and would cost only an honest Runner's facts; binding the method
// to the node the Controller dispatched is separate work.
package entstore

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// MaxNotedFacts bounds how many fact names one built-in difference note
// lists; the note gives the whole count beside them.
const MaxNotedFacts = 16

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

// WithBuiltinProvenance names the built-in methods, so that a batch whose
// emitting method is not one of builtins, and which holds a fact a built-in
// holds (a key the catalog declares, WithDeclarations, or one under the fact
// floor, collection.FactFloor, compared without case), is noted at info. It
// drops nothing: the note says the Runner's release and this one differ.
func WithBuiltinProvenance(builtins map[string]bool) SubscriberOption {
	return func(s *Subscriber) { s.builtins = builtins }
}

// declaredFamily names, in a note, a key held because the catalog declares
// it rather than because it is under the fact floor.
const declaredFamily = "declared by the catalog"

// heldByBuiltin reports the family under which key is a fact a built-in
// holds: the fact floor prefix it falls under, or declaredFamily for a key
// the catalog declares outside every prefix; "" when no built-in holds it.
func (s *Subscriber) heldByBuiltin(key string) string {
	// The same floor the Runner's loader holds an external method's facts to
	// (collection.FactFloor, Phase 136a): the reserved families and every
	// first-party namespace as a prefix, whether or not a build links it.
	folded := strings.ToLower(key)
	for _, prefix := range collection.FactFloor() {
		if strings.HasPrefix(folded, prefix) {
			return prefix
		}
	}
	if s.declared != nil {
		if _, ok := s.declared(key); ok {
			return declaredFamily
		}
	}
	return ""
}

// builtinDifference is what a batch from method holds that this
// controller's release reserves for a built-in, when its catalog lacks
// method: how many entries, the families they fall under, sorted, and the
// first MaxNotedFacts of their keys in the batch's order. It drops nothing.
// Zero when no built-ins were named or method is one of them.
func (s *Subscriber) builtinDifference(method string, entries []facts.Entry) (count int, families, keys []string) {
	if s.builtins == nil || s.builtins[method] {
		return 0, nil, nil
	}
	seen := map[string]bool{}
	for _, e := range entries {
		family := s.heldByBuiltin(e.Key)
		if family == "" {
			continue
		}
		count++
		if !seen[family] {
			seen[family] = true
			families = append(families, family)
		}
		// Bounded: a batch may hold thousands of keys (a package each).
		if len(keys) < MaxNotedFacts {
			keys = append(keys, e.Key)
		}
	}
	sort.Strings(families)
	return count, families, keys
}

// noteBuiltinDifference logs, at info, the facts of batch a built-in holds
// that method, outside this controller's catalog, emitted: the job and
// device, the method as quoted (termsafe.Quote, bounded and escaped, since
// it is the batch's own text), the count, the families and the bounded keys,
// never a value. A key passed facts.Verify, so it is a name that shows as
// itself and is within facts.MaxKeyBytes.
func (s *Subscriber) noteBuiltinDifference(batch facts.Batch, quoted, method string, entries []facts.Entry) {
	count, families, keys := s.builtinDifference(method, entries)
	if count == 0 {
		return
	}
	s.logger.Info("stored facts this controller's release reserves for built-ins, from a method its catalog lacks; the Runner's release and this one differ",
		slog.String("job", batch.JobID), slog.String("device", batch.DeviceID), slog.String("method", quoted),
		slog.Int("facts", count), slog.Any("families", families), slog.Any("keys", keys))
}
