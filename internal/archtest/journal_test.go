// This file is Phase 40's structural guarantee about the run journal: an
// engine.JournalEntry can hold no value a device, a credential store, a
// decrypted envelope, or an injector produced. The journal is not a
// masking design and must never be described as one. There is nothing in
// an entry to mask, and the two rules here are what make that sentence
// checkable rather than aspirational.
//
// Two rules, because neither is sufficient alone and the pairing is the
// whole point. TestJournalEntryHoldsNoValue is reflective: it walks the
// entry's transitive field types and refuses any shape that could carry
// a value at all. That catches map[string]any, error and []byte, but it
// is blind to the difference between a vector of key names and a vector
// of the values under them, because both are []string.
// TestEveryJournalStringFieldIsConstructed closes exactly that hole: it
// pins every string and []string field to the one of seven provenance
// kinds that makes it safe, and fails when a field appears that the
// table does not name, so a later phase adding one has to classify it
// rather than inherit the guarantee by silence.
//
// The reflective half on its own is what the design's own adversarial
// pass named as the draft's weakest point. FAILURE_PATTERNS.md #120 is
// the local precedent for why a rule about secrets has to be structural:
// a process there registered every value it was handed as a secret and
// masked the ordinary ones out of its own output, and every test asking
// only "is the secret gone" passed while it did.
//
// A third rule joins them now that the projection exists, in
// journal_registry_test.go beside this file.
// TestEveryProjectedKeyNameIsRegistryDeclared is the other half of the
// build order's step 4: the two rules above are claims about a struct,
// and that one is a claim about a run. It drives the real
// engine.Executor over every FQCN the real registry holds and asserts
// that the key names reaching an entry are exactly the ones that
// registry declares. It lives here rather than in internal/engine
// because internal/engine blank-imports no Collection package, so the
// registry is empty inside its own tests and the whole Doc intersection
// is unreachable from there. FuzzProjectLevel covers the same rule from
// the inside, against the engine's own action table and an empty
// registry; this covers it against the catalog.
package archtest

import (
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// journalInventoryPackages are the two package trees a journal entry may
// not name a type from, at any depth.
//
// The refusal is by package rather than by type, and that is the load
// bearing part. pkg/inventory.Properties wraps map[string]PropertyValue
// and PropertyValue is an alias for any (pkg/inventory/item.go:31), so
// the shape rules below would catch that one on their own. They would
// not catch pkg/inventory.DeviceID, which is a bare named string and
// would look harmless in a field list. Admitting the harmless ones is
// how "just the ID type" becomes "just the item", so the whole
// vocabulary stays out and JournalEntry.DeviceID is a plain string.
var journalInventoryPackages = []string{
	modulePath + "/internal/inventory",
	modulePath + "/pkg/inventory",
}

// journalErrorType is the built-in error interface, resolved once so the
// walk below can name it in a message rather than reporting it as one
// more anonymous interface.
var journalErrorType = reflect.TypeOf((*error)(nil)).Elem()

// journalTimeType is time.Time, resolved once so journalStringPaths can
// treat it as a leaf rather than descending into the standard library's
// own unexported fields. See that function's doc comment for why.
var journalTimeType = reflect.TypeOf(time.Time{})

// journalViolation is one prohibited type found in the entry's field
// graph, carrying the path that reached it so a failure message names
// the field a reader has to go look at rather than only the offending
// type.
type journalViolation struct {
	// Path is the field path from the entry down, for example
	// "JournalEntry.StatKeys[]" or "JournalEntry.Foo[value]".
	Path string

	// Type is the prohibited type, printed as Go spells it.
	Type string

	// Reason says why the shape is refused, in the terms the provenance
	// rule uses, so the message teaches the rule instead of only citing
	// it.
	Reason string
}

// journalValueViolations walks typ's transitive field types and reports
// every shape that could hold a value the journal refuses to store.
//
// It is a separate function from the test so the rule can be exercised
// against a type this file controls. An assertion that only ever runs
// against engine.JournalEntry cannot tell "the entry is clean" apart
// from "the walk looks at nothing", which is what
// TestJournalValueViolationsDetectsAValueBearingField rules out. This is
// the same negative-control shape unsatisfiableActions and natsDials
// already use in this package.
//
// The walk descends through struct fields (exported and not, since an
// unexported field of an embedded type carries values just as well),
// slice and array elements, and what a pointer or a channel carries. A
// type is walked once: the prohibition test is deterministic per type,
// so a second path to the same type would report the same thing under a
// different name, and the cycle guard is what keeps a self-referential
// type from spinning.
//
// The map case in the switch below is unreachable while
// prohibitedJournalType refuses every map, which it does, so a map is
// reported by its own name and never descended into. It is kept rather
// than deleted because the two rules are separable: a later phase that
// narrows the map refusal to only the value-typed ones would otherwise
// silently lose the descent as well, and lose it at exactly the moment
// it started to matter again.
func journalValueViolations(typ reflect.Type) []journalViolation {
	var found []journalViolation
	walkJournalType(typ, typ.Name(), map[reflect.Type]bool{}, &found)
	return found
}

// walkJournalType is journalValueViolations' recursion. It checks typ
// itself first, then descends, so a prohibited container is reported
// once by its own name rather than repeatedly by whatever it holds.
func walkJournalType(typ reflect.Type, path string, seen map[reflect.Type]bool, found *[]journalViolation) {
	if reason := prohibitedJournalType(typ); reason != "" {
		*found = append(*found, journalViolation{Path: path, Type: typ.String(), Reason: reason})
		return
	}
	if seen[typ] {
		return
	}
	seen[typ] = true

	switch typ.Kind() {
	case reflect.Struct:
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			walkJournalType(field.Type, path+"."+field.Name, seen, found)
		}
	case reflect.Slice, reflect.Array:
		walkJournalType(typ.Elem(), path+"[]", seen, found)
	case reflect.Pointer:
		// A pointer is a transparent hop, so the path stays the one a
		// reader would type: probe.Indirect.Raw, not *probe.Indirect.Raw.
		walkJournalType(typ.Elem(), path, seen, found)
	case reflect.Chan:
		walkJournalType(typ.Elem(), path+"<-", seen, found)
	case reflect.Map:
		walkJournalType(typ.Key(), path+"[key]", seen, found)
		walkJournalType(typ.Elem(), path+"[value]", seen, found)
	}
}

// prohibitedJournalType reports why typ may not appear anywhere in a
// journal entry, or "" when it may.
//
// Each refusal names the concrete thing that would otherwise land in the
// journal, because the rule is only followable if a reader can see what
// it is protecting. The list is exactly the design note's, deliberately:
// a shape nobody has argued about is not added here on a hunch, since a
// prohibition that fires on a field the rule was never about is one
// people route around rather than honor.
func prohibitedJournalType(typ reflect.Type) string {
	if isJournalInventoryType(typ) {
		return "an inventory type. The journal names a device by its stored id and nothing else; " +
			"an inventory value carries the device's own properties, and Properties.Raw() hands back " +
			"map[string]any. The whole vocabulary is refused, not just the value-bearing half"
	}

	switch typ.Kind() {
	case reflect.Interface:
		switch {
		case typ == journalErrorType:
			return "an error. NodeResult.Err embeds device output verbatim, and the engine " +
				"contractually never masks it (internal/engine/executor_secrets_test.go asserts a raw " +
				"secret survives there). JournalEntry.FailureStage records which stage failed instead, " +
				"which is queryable and cannot drift when someone rewords an error string"
		case typ.NumMethod() == 0:
			return "a bare any/interface{}. What it holds is decided at run time by whatever wrote it, " +
				"which for NodeResult.Stats is a Collection method talking to a device"
		default:
			return "an interface. What it holds is decided by the caller at run time, so no rule about " +
				"this type can say what ends up stored"
		}
	case reflect.Slice, reflect.Array:
		if typ.Elem().Kind() == reflect.Uint8 {
			return "a run of bytes. That is the shape a file body, a rendered template and a decrypted " +
				"envelope all arrive in"
		}
	case reflect.Map:
		// Every map, not just map[string]any. The interface case above
		// already refuses the value-typed ones, so a bare map[string]any
		// never reaches here; what this closes is the typed map nobody
		// argues about until it ships, map[string]string being the one
		// that reads as obviously safe. It is not safe here, for a reason
		// specific to this type rather than a general suspicion of maps:
		// TestEveryJournalStringFieldIsConstructed classifies a field by
		// NAME, and a map has no field names to classify. Its value half
		// is therefore an unclassifiable free-text channel, which is the
		// one thing the provenance rule exists to make impossible. The
		// design note's Section 1 states the entry carries no map at all,
		// so this refuses exactly what that sentence already promises.
		return "a map. Its value half has no field name, so the provenance rule cannot classify what " +
			"lands there and TestEveryJournalStringFieldIsConstructed cannot see it. A journal entry " +
			"records key NAMES in a []string, never a key-to-value mapping"
	}
	return ""
}

// isJournalInventoryType reports whether typ is declared in one of the
// two inventory package trees.
//
// The match is exact-or-slash-terminated rather than a bare prefix, for
// the same reason isEntPackage's is: "/internal/inventory" as a plain
// prefix would also match a future "/internal/inventorysync" and refuse
// a package this rule was never about.
func isJournalInventoryType(typ reflect.Type) bool {
	pkg := typ.PkgPath()
	if pkg == "" {
		return false
	}
	for _, forbidden := range journalInventoryPackages {
		if pkg == forbidden || strings.HasPrefix(pkg, forbidden+"/") {
			return true
		}
	}
	return false
}

// TestJournalEntryHoldsNoValue asserts no field of engine.JournalEntry,
// at any depth, has a type that could carry a value a device, a
// credential store, a decrypted envelope, or an injector produced.
//
// This is the rule that lets the journal be described as holding nothing
// to mask. Every admissible field is a platform identifier, a
// registry-resolved catalog constant, a closed enum, a content digest,
// an author-written label from the compiled runbook, or a count, and
// none of those six is a shape this walk refuses.
//
// It is a blanket refusal with no exception, and that is a decision
// rather than a stage nobody has reached yet. Section 12 of the Phase 40
// design note proposes a seventh kind, a run-observed value a method
// declares safe to record per parameter, which would turn this test into
// a rule with one tested exception. Section 12 says in its own words
// that the weakening "should be argued again before it is built, not
// assumed settled by this amendment". So the strict version ships, and
// the exception costs a deliberate argument later instead of arriving as
// a default. JournalEntry's own doc comment records the same pending
// amendment from the other side.
func TestJournalEntryHoldsNoValue(t *testing.T) {
	entry := reflect.TypeOf(engine.JournalEntry{})
	if entry.NumField() == 0 {
		t.Fatal("engine.JournalEntry has no fields, so this walk would pass by examining nothing")
	}

	for _, v := range journalValueViolations(entry) {
		t.Errorf(
			"%s is %s, which is %s.\n"+
				"A journal entry field must be a platform identifier, a registry-resolved catalog "+
				"constant, a closed enum this package defines, a content digest, an author-written "+
				"label from the compiled runbook, or a count. Nothing else has an argument for why a "+
				"device value cannot reach it.\n"+
				"If this field is really needed, the answer is a key name, a count or an enum derived "+
				"from it, never the thing itself.",
			v.Path, v.Type, v.Reason)
	}
}

// TestJournalValueViolationsDetectsAValueBearingField is the negative
// control for the rule above: it proves the walk really does report a
// field that could hold a value, and really does leave the admissible
// shapes alone.
//
// The probe type is synthesized here rather than planted in
// internal/engine and removed again. Planting one is a real control, but
// a manual one that runs once and leaves nothing behind; this one runs
// on every CI job, so the walk cannot quietly stop finding things.
//
// Both halves matter. A walk that reported everything would pass the
// first half and fail the second, and it would make the rule above
// unusable by failing on RunID.
func TestJournalValueViolationsDetectsAValueBearingField(t *testing.T) {
	// Every shape the rule refuses, one per field, plus the shapes it
	// must admit. The clean fields are the second half of the control:
	// they are exactly the kinds engine.JournalEntry is built from.
	type probe struct {
		Stats     map[string]interface{} // a map whose value is an interface
		Anything  any                    // a bare any
		Err       error                  // an error
		Body      []byte                 // a byte slice
		Device    inventory.DeviceID     // a named string, but from a refused package
		Nested    []any                  // an interface reached as a slice element
		Indirect  *struct{ Raw any }     // an interface reached through a pointer and a struct
		TypedMap  map[string]string      // a map with no interface anywhere: see below
		Name      string                 // admissible: an author label
		Keys      []string               // admissible: a key vector
		Count     int                    // admissible: a count
		Started   time.Time              // admissible: a platform-stamped instant
		Truthy    bool                   // admissible: the two-valued closed enum
		Outcome   engine.Outcome         // admissible: a closed enum internal/engine defines
		StatSlice []engine.FailureStage  // admissible: a slice of a closed enum
	}

	got := journalValueViolations(reflect.TypeOf(probe{}))

	gotPaths := make([]string, 0, len(got))
	for _, v := range got {
		gotPaths = append(gotPaths, v.Path)
		if v.Reason == "" {
			t.Errorf("%s was reported with no reason: the message is what makes the rule followable", v.Path)
		}
	}
	sort.Strings(gotPaths)

	wantPaths := []string{
		"probe.Anything",
		"probe.Body",
		"probe.Device",
		"probe.Err",
		"probe.Nested[]",
		// TypedMap is the regression guard for the hole this pairing had
		// on first write. map[string]string holds no interface, is not a
		// []byte and names no inventory type, so every shape rule here
		// admitted it, and the classification sweep could not see it
		// either because a map has no field name to classify. It is the
		// exact shape a value-carrying widening would take.
		"probe.TypedMap",
		// The map is reported by its OWN name, not as probe.Stats[value].
		// prohibitedJournalType refuses every map outright, so the walk
		// stops at the container rather than descending to the any inside
		// it, which is what walkJournalType's own doc comment says it
		// intends: a prohibited container is named once rather than
		// repeatedly by whatever it holds. Before maps were refused this
		// read probe.Stats[value], and the change is the point rather than
		// a regression: map[string]string used to pass this rule entirely,
		// because only the value half was ever inspected.
		"probe.Stats",
		"probe.Indirect.Raw",
	}
	sort.Strings(wantPaths)

	if !reflect.DeepEqual(gotPaths, wantPaths) {
		t.Errorf("journalValueViolations() reported %v, want %v", gotPaths, wantPaths)
	}
}

// journalFieldKind is one of the six provenance kinds a journal entry
// field may be. The numbering is the design note's own and
// engine.JournalEntry's doc comment repeats it field by field, so a
// reader moving between the two never has to translate.
type journalFieldKind int

// The seven kinds. The seventh was added deliberately (Phase 40's design
// note, Sections 12 and 13), and adding an eighth is a design decision
// rather than a table edit.
const (
	// journalKindPlatformID is a platform-generated identifier: the
	// platform minted or stamped it, so nothing a device said can reach
	// it.
	journalKindPlatformID journalFieldKind = iota + 1

	// journalKindRegistryConstant is a compile-time constant resolved
	// through a registry at write time. The resolution is what makes it
	// this kind: copying the caller's bytes would make the same field
	// kind 5 at best, and unconstrained text at worst.
	journalKindRegistryConstant

	// journalKindClosedEnum is a closed enum internal/engine defines and
	// the projection maps onto explicitly.
	journalKindClosedEnum

	// journalKindDigest is a content-addressed digest, computed and never
	// authored.
	journalKindDigest

	// journalKindAuthorLabel is a label a runbook author typed. This is
	// the residual text channel and the design's one recorded soft spot.
	journalKindAuthorLabel

	// journalKindCount is a count. It is listed for completeness: a count
	// is an int, so no string field can legitimately claim it, and the
	// table check below says so.
	journalKindCount

	// journalKindDeclaredIdentifier is an undo parameter's value that the
	// emitting method's own manifest declares an identifier
	// (sdk.InverseSpec.Record): a name, a path, an id. The projection
	// admits it only for a built-in method's declared key, and the
	// registry sweep (journalAssertNoValueSurvived) holds each one to that
	// declaration.
	journalKindDeclaredIdentifier
)

// journalKindNames turns a kind into the phrase the design note uses for
// it, so a failure message reads as the rule rather than as a number.
var journalKindNames = map[journalFieldKind]string{
	journalKindPlatformID:       "1, a platform-generated identifier",
	journalKindRegistryConstant: "2, a compile-time constant resolved through a registry at write time",
	journalKindClosedEnum:       "3, a closed enum internal/engine defines",
	journalKindDigest:           "4, a content-addressed digest",
	journalKindAuthorLabel:      "5, an author-written label from the compiled runbook",
	journalKindCount:            "6, a count",

	journalKindDeclaredIdentifier: "7, an undo parameter's value its method declares an identifier",
}

// journalStringField is one field's classification: which kind it is,
// and the construction that makes that claim true.
type journalStringField struct {
	kind journalFieldKind

	// why states the construction, not the intent. "A platform id"
	// classifies nothing; "the Walk sink stamps it from the dispatch
	// payload, never from anything the run reports" is checkable by
	// reading one function.
	why string
}

// journalStringFields classifies every string and []string field on
// engine.JournalEntry.
//
// This table is the test. Reflection can prove a field is a []string; it
// cannot tell a vector of key names from a vector of the values under
// them, and that difference is the entire guarantee. So each one is
// written down with the construction that makes it safe, and a field the
// table does not name fails the build.
//
// Adding a field here is not paperwork. It is the moment to ask whether
// the new field really is one of the seven kinds, which is the question the
// journal exists to keep answerable.
var journalStringFields = map[string]journalStringField{
	"JobID": {
		kind: journalKindPlatformID,
		why:  "the Walk sink stamps it at construction from the wire.DispatchPayload it was built for, never from anything the run itself reports",
	},
	"RunID": {
		kind: journalKindPlatformID,
		why:  "minted inside Executor.Run on its own per-call state, so it exists before any node runs",
	},
	"NodeID": {
		kind: journalKindPlatformID,
		why:  "the graph id the DAG builder synthesized, such as tasks[0], never the task's register name",
	},
	"DeviceID": {
		kind: journalKindPlatformID,
		why:  "the inventory item's own stored id field, never a device property the platform read back",
	},
	"ProviderProgram": {
		kind: journalKindPlatformID,
		why:  "the path the loader resolved itself (checkDir's resolved directory joined with the directory entry's name) and set on the descriptor when it registered the method, read back at write time through collection.Lookup as FQCN is; a program's description carries only a name and a manifest, so nothing the program, the runbook or a device says can reach it",
	},
	"ProviderDigest": {
		kind: journalKindDigest,
		why:  "sha256:<hex> of the program's bytes, computed by the loader's openProgram from the file it opened and set on the descriptor at registration, never reported by the program",
	},
	"DAGVersion": {
		kind: journalKindDigest,
		why:  "sha256:<hex> over the fully resolved definition, computed by the DAG builder and never authored",
	},
	"FQCN": {
		kind: journalKindRegistryConstant,
		why:  "resolved through collection.Lookup, then the engine's own builtin table, and stored as the descriptor's registered name; a miss stores the FQCNUnregistered constant and sets the flag, so the author's bytes are never stored",
	},
	"InverseFQCN": {
		kind: journalKindRegistryConstant,
		why:  "resolved exactly as FQCN is, and it needs that more than FQCN does: it is not even author text but whatever a Collection method put in a map at run time, and sdk.RecordInverse checks only that it is non-empty",
	},
	"StatKeys": {
		kind: journalKindRegistryConstant,
		why:  "top-level stat key names intersected against the executing method's own Doc.Returns, plus the sdk.StatInverse and sdk.StatDiff package constants; top level only, because one level down a key can be device text (internal/catalog/http/request.go keys the headers stat by the device's own response header names)",
	},
	"ParamKeys": {
		kind: journalKindRegistryConstant,
		why:  "task param key names intersected against the executing method's own Doc.Params; param values are excluded outright and no declaration admits one, since file.copy's content param is the file body",
	},
	"InverseParamKeys": {
		kind: journalKindRegistryConstant,
		why:  "the resolved inverse target's Doc.Params key names, key names only; their values, where declared, are InverseParams",
	},
	"InverseParams.Key": {
		kind: journalKindRegistryConstant,
		why:  "a key the emitting method's manifest lists in its sdk.InverseSpec.Record for this undo's method and the target's Doc.Params declares; any other key's value is not recorded",
	},
	"InverseParams.Text": {
		kind: journalKindDeclaredIdentifier,
		why:  "a string undo parameter under a declared key, admitted only at 256 bytes or fewer of valid UTF-8 that termsafe.CheckLine accepts (engine.recordedValue)",
	},
	"InverseParams.Number": {
		kind: journalKindDeclaredIdentifier,
		why:  "a numeric undo parameter under a declared key, kept as its JSON text; a number carries no text a terminal could act on",
	},
	"RollbackOf": {
		kind: journalKindPlatformID,
		why:  "the run id (Crawl) or job id (Walk) of the run being undone, handed to engine.WithRollback by the rollback command or job, which read it from the platform's own journal",
	},
	"UndoesNode": {
		kind: journalKindPlatformID,
		why:  "a graph node id of the run being undone, handed to engine.WithRollback from that run's own journal entries",
	},
	"Outcome": {
		kind: journalKindClosedEnum,
		why:  "engine.Outcome, mapped from the executor's control flow by an exhaustive projection that fails closed on a branch it does not recognize",
	},
	"FailureStage": {
		kind: journalKindClosedEnum,
		why:  "engine.FailureStage, one value per failure call site in executor.go, read off control flow rather than parsed back out of an error's text",
	},
	"SkipKind": {
		kind: journalKindClosedEnum,
		why:  "engine.SkipKind, whose three condition values are ConditionProgram's own keyword literals, so the projection copies a keyword across instead of inventing a second vocabulary that could drift",
	},
	"DAGID": {
		kind: journalKindAuthorLabel,
		why:  "the runbook's author-written id:, and the mildest of the three author labels: validRunbookID constrains it to ^[A-Za-z0-9_-]*$ and buildFromDef refuses a runbook whose id: fails that check",
	},
	"TaskName": {
		kind: journalKindAuthorLabel,
		why:  "the task's author-written name:, unconstrained free text, so an author who pastes a secret into a task name puts it in the journal",
	},
	"Register": {
		kind: journalKindAuthorLabel,
		why:  "the task's author-written register: name, unconstrained free text; it is the key a later when_cel reads this node's result under, never the result",
	},
}

// journalAuthorLabelFields is the exact set of fields allowed to be kind
// 5, the residual channel through which bytes a runbook author typed
// reach the journal.
//
// It is pinned separately from the table because classifying a new field
// as kind 5 is the one classification that widens the guarantee's blast
// radius, and a table edit alone is too quiet a way to do that. Growing
// this set is a design decision that belongs in the design note, next to
// the sentence recording that a runbook's name: and register: are
// unconstrained.
var journalAuthorLabelFields = map[string]bool{
	"DAGID":    true,
	"TaskName": true,
	"Register": true,
}

// journalStringFieldNames returns every field of typ whose values are
// strings: a string, a named string type such as engine.Outcome, or a
// slice or array of either.
//
// It keys off reflect.Kind rather than an exact match against the string
// type on purpose. engine.Outcome, engine.FailureStage and
// engine.SkipKind are all named string types, and a rule that only saw
// the unnamed one would let a later phase smuggle a free-text field in
// behind a type name.
func journalStringFieldNames(typ reflect.Type) []string {
	var names []string
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		names = append(names, journalStringPaths(field.Type, field.Name, map[reflect.Type]bool{})...)
	}
	sort.Strings(names)
	return names
}

// journalStringPaths returns every path under typ that reaches a string,
// named by the field a reader would type to get there.
//
// It recurses rather than reading only the entry's own fields, and that
// is the whole point of it. An earlier version looked at top-level
// fields alone, so it saw StatKeys []string and reported it, and saw
// neither a nested struct holding a string nor a [][]string: both are
// string-bearing, neither has a top-level Kind of String or a
// slice-of-String, and both therefore reached the classification table
// invisible to it. A field the table cannot see is a field nobody has to
// classify, which is exactly the hole this rule exists to close, and it
// is the shape a value-carrying widening would take.
//
// time.Time is a declared leaf. StartedAt and FinishedAt are kind 1
// platform-generated identifiers whose internals are the standard
// library's business: descending into one would demand a classification
// for time.Location's own name and extend fields, which say nothing
// about this entry and would make the table a record of Go's internals
// rather than of this type's provenance.
//
// The seen set is a cycle guard, for the same reason walkJournalType
// carries one: a self-referential type would otherwise spin here rather
// than fail with a useful message.
func journalStringPaths(typ reflect.Type, path string, seen map[reflect.Type]bool) []string {
	if typ == journalTimeType {
		return nil
	}
	switch typ.Kind() {
	case reflect.String:
		return []string{path}
	case reflect.Slice, reflect.Array, reflect.Pointer:
		// A slice, an array and a pointer are all transparent hops: the
		// path stays the one a reader would type, so [][]string reports
		// its own field name rather than a synthetic element path.
		return journalStringPaths(typ.Elem(), path, seen)
	case reflect.Struct:
		if seen[typ] {
			return nil
		}
		seen[typ] = true
		var out []string
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			out = append(out, journalStringPaths(field.Type, path+"."+field.Name, seen)...)
		}
		return out
	}
	return nil
}

// unclassifiedJournalStringFields returns the string-valued fields of
// typ that table does not name, which is the failure this rule exists to
// produce.
func unclassifiedJournalStringFields(typ reflect.Type, table map[string]journalStringField) []string {
	var missing []string
	for _, name := range journalStringFieldNames(typ) {
		if _, ok := table[name]; !ok {
			missing = append(missing, name)
		}
	}
	return missing
}

// staleJournalStringFields returns the table entries naming a field typ
// no longer has, so the table stays a true record of the type rather
// than accumulating classifications of fields somebody deleted. This is
// TestAdapterAllowlistHasNoStaleEntries' reasoning applied to a table
// that classifies rather than permits.
func staleJournalStringFields(typ reflect.Type, table map[string]journalStringField) []string {
	present := make(map[string]bool)
	for _, name := range journalStringFieldNames(typ) {
		present[name] = true
	}

	stale := make([]string, 0)
	for name := range table {
		if !present[name] {
			stale = append(stale, name)
		}
	}
	sort.Strings(stale)
	return stale
}

// TestEveryJournalStringFieldIsConstructed asserts that every string and
// []string field on engine.JournalEntry is classified as one of the six
// provenance kinds, with the construction that makes the claim true
// written down beside it.
//
// This is the half TestJournalEntryHoldsNoValue cannot do. Reflection
// sees []string and stops: StatKeys holding key names and a StatValues
// holding the values under them are the same type, and only one of them
// is admissible. So the guarantee is carried by a table a human wrote,
// and this test's real job is making the table impossible to skip. A
// field the table does not name fails the build, which turns "classify
// your new field" from a review comment somebody may or may not leave
// into a compile-time-shaped obligation.
//
// It also refuses two quieter ways to weaken it: a classification with
// no stated construction, and a new field silently joining the kind 5
// author-text set, which is the one kind whose blast radius grows with
// membership.
func TestEveryJournalStringFieldIsConstructed(t *testing.T) {
	entry := reflect.TypeOf(engine.JournalEntry{})

	names := journalStringFieldNames(entry)
	if len(names) == 0 {
		t.Fatal("engine.JournalEntry has no string-valued fields, so this table would classify nothing")
	}

	for _, name := range unclassifiedJournalStringFields(entry, journalStringFields) {
		t.Errorf(
			"engine.JournalEntry.%s is a string-valued field that archtest's journalStringFields does "+
				"not classify.\n"+
				"Every one of them has to name which of the six provenance kinds it is, and the "+
				"construction that makes that true, because reflection cannot tell a vector of key "+
				"names from a vector of the values under them.\n"+
				"If the honest answer is \"it holds whatever the method returned\", the field does not "+
				"belong on the entry: store a key name, a count or an enum instead.",
			name)
	}

	if stale := staleJournalStringFields(entry, journalStringFields); len(stale) > 0 {
		t.Errorf("journalStringFields classifies fields engine.JournalEntry no longer has: %v", stale)
	}

	for name, field := range journalStringFields {
		if _, ok := journalKindNames[field.kind]; !ok {
			t.Errorf("journalStringFields[%q] claims kind %d, which is not one of the seven", name, field.kind)
		}
		if strings.TrimSpace(field.why) == "" {
			t.Errorf("journalStringFields[%q] states no construction: a kind with no reason classifies nothing", name)
		}
		if field.kind == journalKindCount {
			t.Errorf("journalStringFields[%q] claims kind %s, but a count is an int; a string cannot be one",
				name, journalKindNames[journalKindCount])
		}
	}

	for name, field := range journalStringFields {
		isLabel := field.kind == journalKindAuthorLabel
		if isLabel && !journalAuthorLabelFields[name] {
			t.Errorf(
				"engine.JournalEntry.%s is classified as kind %s. It is not in journalAuthorLabelFields.\n"+
					"Kind 5 is the residual channel through which bytes a runbook author typed reach the "+
					"journal, and it is the design's one recorded soft spot. Widening it is a decision to "+
					"argue in the design note, not a table edit: a runbook's name: and register: are "+
					"unconstrained free text.",
				name, journalKindNames[journalKindAuthorLabel])
		}
		if !isLabel && journalAuthorLabelFields[name] {
			t.Errorf("journalAuthorLabelFields names %q, which journalStringFields classifies as kind %s",
				name, journalKindNames[field.kind])
		}
	}

	for name := range journalAuthorLabelFields {
		if _, ok := journalStringFields[name]; !ok {
			t.Errorf("journalAuthorLabelFields names %q, which journalStringFields does not classify at all", name)
		}
	}
}

// TestJournalStringFieldRulesDetectAnUnclassifiedField is the negative
// control for the rule above: it proves the field sweep really does
// report a field the table misses and a table entry the type has lost,
// rather than passing because it examines nothing.
//
// Like the other control in this file, the probe type and the probe
// table are synthesized here rather than planted in internal/engine and
// removed again, so the control runs on every CI job instead of once.
func TestJournalStringFieldRulesDetectAnUnclassifiedField(t *testing.T) {
	type probe struct {
		Known      string               // classified below
		Unexpected string               // not classified: must be reported
		Vector     []string             // not classified: must be reported, since a []string is the ambiguous case
		Named      engine.Outcome       // not classified: a named string type must not slip past
		Count      int                  // not string-valued: must not be reported
		Recorded   bool                 // not string-valued: must not be reported
		Started    time.Time            // not string-valued, and a struct that contains strings one level down
		Matrix     [][]string           // string-bearing through two slice hops: must be reported
		Body       struct{ Raw string } // string-bearing through a nested struct: must be reported
	}

	table := map[string]journalStringField{
		"Known":   {kind: journalKindPlatformID, why: "the control's classified field"},
		"Deleted": {kind: journalKindPlatformID, why: "a field the probe type does not have"},
	}

	probeType := reflect.TypeOf(probe{})

	gotMissing := unclassifiedJournalStringFields(probeType, table)
	sort.Strings(gotMissing)
	// Matrix and Body.Raw are the regression guards for this sweep's own
	// first-write hole: it read the entry's top-level fields only, so a
	// [][]string and a string inside a nested struct both reached the
	// table invisible to it. A field the table cannot see is a field
	// nobody has to classify, which is the guarantee inverted. Started
	// stays absent from this list on purpose: time.Time is a declared
	// leaf, and descending into it would demand a classification for
	// time.Location's own name field.
	wantMissing := []string{"Body.Raw", "Matrix", "Named", "Unexpected", "Vector"}
	if !reflect.DeepEqual(gotMissing, wantMissing) {
		t.Errorf("unclassifiedJournalStringFields() = %v, want %v", gotMissing, wantMissing)
	}

	gotStale := staleJournalStringFields(probeType, table)
	wantStale := []string{"Deleted"}
	if !reflect.DeepEqual(gotStale, wantStale) {
		t.Errorf("staleJournalStringFields() = %v, want %v", gotStale, wantStale)
	}

	// The positive half: a classified field is not reported as missing,
	// so the rule is not simply reporting everything it is shown.
	for _, name := range gotMissing {
		if name == "Known" {
			t.Error("unclassifiedJournalStringFields() reported the classified field, so it reports everything")
		}
	}

	// And the kind names really do cover every kind the table can hold,
	// since a missing entry there would print "kind 0" in a failure
	// message and teach a reader nothing.
	for kind := journalKindPlatformID; kind <= journalKindDeclaredIdentifier; kind++ {
		if _, ok := journalKindNames[kind]; !ok {
			t.Errorf("journalKindNames has no phrase for kind %d", kind)
		}
	}
	if len(journalKindNames) != int(journalKindDeclaredIdentifier) {
		t.Errorf("journalKindNames holds %d kinds, want the %d the provenance rule defines",
			len(journalKindNames), int(journalKindDeclaredIdentifier))
	}
}
