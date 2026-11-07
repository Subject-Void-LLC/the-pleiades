// Package collection: what a method declares about the facts it emits
// (Phase 41b). A method that calls sdk.RunbookContext.EmitFact declares each
// key, or each key prefix, it may emit, as stable (every value kept, a
// change is drift) or volatile (only the latest kept, never drift), with a
// description and the JSON type of its value. The engine stores nothing for
// a key the method did not declare, or for a value of another type.
package collection

import (
	"fmt"
	"strings"
	"unicode"
)

// FactKind says how a fact's values are kept.
type FactKind string

const (
	// FactStable is a fact whose change means the device changed (a kernel
	// version): every value is kept, and a change is drift.
	FactStable FactKind = "stable"
	// FactVolatile is a fact that moves on its own (uptime): only the latest
	// value is kept, and a change is never drift.
	FactVolatile FactKind = "volatile"
)

// FactType is the JSON type of a fact's value.
type FactType string

const (
	// FactString is a JSON string.
	FactString FactType = "string"
	// FactInteger is a JSON number with no fraction.
	FactInteger FactType = "integer"
	// FactNumber is any JSON number.
	FactNumber FactType = "number"
	// FactBoolean is true or false.
	FactBoolean FactType = "boolean"
	// FactList is a JSON array.
	FactList FactType = "list"
	// FactObject is a JSON object.
	FactObject FactType = "object"
)

// MaxFactsPerObservation is how many facts one task may record on a device:
// an observation holding more is stored as none of them. It is
// internal/facts' bound, which a Collection cannot import, held equal to it
// by internal/engine's tests.
const MaxFactsPerObservation = 8192

// MaxFactKeyBytes is the longest a fact's key may be, in bytes: the store's
// bound (internal/facts.MaxKeyBytes), held equal to it by internal/engine's
// tests, so a method can refuse a key at plan time that storage would drop.
const MaxFactKeyBytes = 256

// RawDigestField is the field of a fact's object value under which a method
// hands over a digest of something not everyone may read (a file only its
// owner reads): the engine keys or seals it before the run's report, a
// register or the fact store sees it, and drops it otherwise, since a plain
// digest of a small secret lets anyone who reads it test guesses. It is
// internal/facts' RawDigestField, held equal to it by internal/engine's tests.
const RawDigestField = "sha256_raw"

// FactSpec declares one fact, or one family of facts sharing a prefix, that a
// method may emit.
type FactSpec struct {
	// Key is the fact's name, or with Prefix the start every name in the
	// family shares.
	Key string `json:"key"`
	// Prefix makes Key a prefix: any name starting with it is declared.
	Prefix bool `json:"prefix,omitempty"`
	// ItemNames says that what follows a family's prefix names an item (a
	// package called password-store), not a field, so the secret-name
	// check reads only the prefix. It is never assumed: a family keyed by
	// field names (env.DB_PASSWORD) leaves it unset and keeps the check on
	// every name. It needs Prefix, and a prefix ending in a dot.
	ItemNames bool `json:"item_names,omitempty"`
	// Kind is stable or volatile.
	Kind FactKind `json:"kind"`
	// Type is the JSON type of the value.
	Type FactType `json:"type"`
	// Description says what the fact is, in one line.
	Description string `json:"description"`
}

// NamesItems reports whether s is a family whose names after its prefix are
// items' (ItemNames), which the secret-name check does not read.
func (s FactSpec) NamesItems() bool { return s.Prefix && s.ItemNames }

// Matches reports whether name is the fact s declares.
func (s FactSpec) Matches(name string) bool {
	if s.Prefix {
		return strings.HasPrefix(name, s.Key) && len(name) > len(s.Key)
	}
	return name == s.Key
}

// FactFor returns the declaration of name among specs: an exact key before
// a prefix, and the longest prefix among prefixes.
func FactFor(specs []FactSpec, name string) (FactSpec, bool) {
	var best FactSpec
	found := false
	for _, s := range specs {
		if !s.Matches(name) {
			continue
		}
		if !s.Prefix {
			return s, true
		}
		if !found || len(s.Key) > len(best.Key) {
			best, found = s, true
		}
	}
	return best, found
}

// DeclareFacts builds a method's fact declarations from its documented
// returns, so a fact is described once: each of keys, stable unless it is
// among volatile, typed from the return's documented type ("string", "int",
// "bool", "float", "list of ...", "map" and their spellings). A key with no
// documented return, or a type it cannot read, gets no description or no
// type, which Register then refuses, so a mistake stops the binary at start.
func DeclareFacts(returns []ReturnField, keys []string, volatile ...string) []FactSpec {
	out := make([]FactSpec, 0, len(keys))
	for _, key := range keys {
		spec := FactSpec{Key: key, Kind: FactStable}
		for _, v := range volatile {
			if v == key {
				spec.Kind = FactVolatile
			}
		}
		for _, r := range returns {
			if r.Name == key {
				spec.Description, spec.Type = r.Description, factTypeOf(r.Type)
			}
		}
		out = append(out, spec)
	}
	return out
}

// factTypeOf reads a documented return type as a fact's JSON type, "" when
// it names none.
func factTypeOf(doc string) FactType {
	doc = strings.ToLower(strings.TrimSpace(doc))
	switch {
	case doc == "string" || doc == "str":
		return FactString
	case doc == "int" || doc == "integer":
		return FactInteger
	case doc == "float" || doc == "number":
		return FactNumber
	case doc == "bool" || doc == "boolean":
		return FactBoolean
	case doc == "list" || strings.HasPrefix(doc, "list of "):
		return FactList
	case doc == "map" || doc == "dict" || doc == "object":
		return FactObject
	}
	return ""
}

// ValidateFacts checks a method's fact declarations on their own: each names
// a printable key with no space or slash, is stable or volatile, has a value
// type and a description, and no two declare the same key. Its messages read
// after the method's name. The loader calls it for an external program's
// methods, as Register does.
func ValidateFacts(specs []FactSpec) error {
	seen := map[string]bool{}
	for _, s := range specs {
		switch {
		case !validFactKey(s.Key):
			return fmt.Errorf("declares a fact %q, which is not a fact name: empty, longer than 256 bytes, or holding a space, a slash or an unprintable character", s.Key)
		case s.Kind != FactStable && s.Kind != FactVolatile:
			return fmt.Errorf("declares fact %q with kind %q, which is neither %q nor %q", s.Key, s.Kind, FactStable, FactVolatile)
		case !validFactType(s.Type):
			return fmt.Errorf("declares fact %q with type %q, which is not a JSON type it can name", s.Key, s.Type)
		case strings.TrimSpace(s.Description) == "":
			return fmt.Errorf("declares fact %q with no description", s.Key)
		case s.ItemNames && (!s.Prefix || len(s.Key) < 2 || !strings.HasSuffix(s.Key, ".")):
			return fmt.Errorf("declares fact %q's names as items, which only a family whose prefix ends in a dot can", s.Key)
		}
		id := s.Key
		if s.Prefix {
			id += "*"
		}
		if seen[id] {
			return fmt.Errorf("declares fact %q twice", s.Key)
		}
		seen[id] = true
	}
	return nil
}

// checkFacts is Register's check of d's fact declarations: valid on their
// own, and agreeing with every registered method's declaration that can name
// the same key (an equal key, a key inside a family, or two families one
// holding the other), since a device's facts are kept by key whatever method
// emitted them, and one key cannot be kept both ways. External Collections
// register after the built-ins, so this is also what keeps a program's
// family from claiming a built-in's key under another kind. Registration
// happens at start (package init and the loader, one at a time), so the
// check and the write are not raced.
func checkFacts(d Descriptor) error {
	if err := ValidateFacts(d.Manifest.Facts); err != nil {
		return fmt.Errorf("collection: %q %w", d.Name, err)
	}
	// An external method (one the loader gave a Provider) may not declare a
	// fact a built-in holds at all, whatever its kind and type
	// (facts_reserved.go); the agreement check below is for built-ins.
	if d.Provider != nil {
		if err := checkExternalFacts(d); err != nil {
			return err
		}
	}
	for _, s := range d.Manifest.Facts {
		for name, other := range collections.All() {
			for _, o := range other.Manifest.Facts {
				if overlaps(s, o) && (o.Kind != s.Kind || o.Type != s.Type) {
					return fmt.Errorf("collection: %q declares fact %s %s %s, and %q declares %s %s %s, which can name the same key",
						d.Name, factName(s), s.Kind, s.Type, name, factName(o), o.Kind, o.Type)
				}
			}
		}
	}
	return nil
}

// factName is a declaration's key as a message shows it: quoted, with a
// trailing * for a family.
func factName(s FactSpec) string {
	if s.Prefix {
		return fmt.Sprintf("%q*", s.Key)
	}
	return fmt.Sprintf("%q", s.Key)
}

// validFactKey is internal/facts.ValidKey's rule, which a package under pkg/
// cannot import: non-empty, at most 256 bytes, printable, no space or slash.
func validFactKey(key string) bool {
	if key == "" || len(key) > 256 {
		return false
	}
	for _, r := range key {
		if !unicode.IsPrint(r) || unicode.IsSpace(r) || r == '/' {
			return false
		}
	}
	return true
}

// validFactType reports whether t is one of the fact value types.
func validFactType(t FactType) bool {
	switch t {
	case FactString, FactInteger, FactNumber, FactBoolean, FactList, FactObject:
		return true
	}
	return false
}
