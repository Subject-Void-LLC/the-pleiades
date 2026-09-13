package credtype_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
)

// FuzzExternalReference fuzzes the parse that turns a stored reference into
// a source and the rest.
//
// It is fuzzed rather than tabled because of where the input comes from. A
// reference is a column on a credential row, written through the API by any
// holder of credential:write and read back on the dispatch path, so it is
// attacker-controlled text that reaches a parser at the moment a job runs.
// That is one of the deserialization boundaries Phase 39's audit categories
// name.
//
// Three properties, and the third is the one a table could not establish.
func FuzzExternalReference(f *testing.F) {
	seeds := []string{
		"file:prod_api_token",
		"hashivault_kv:secret/data/prod#token",
		"",
		":",
		"file:",
		":token",
		"file",
		"file:a:b:c",
		"FILE:x",
		"file:\x00",
		strings.Repeat("a", 4096) + ":x",
		"file:" + strings.Repeat("../", 512) + "etc/passwd",
	}
	for _, s := range seeds {
		f.Add("api_token", s)
	}

	// No real source is wired, so every declared source answers
	// not-implemented. That is the right shape for this target: it isolates
	// the PARSE, which is what is being fuzzed, from any one source's own
	// interpretation of what follows the colon.
	lookups, err := credtype.NewLookups()
	if err != nil {
		f.Fatalf("NewLookups() error = %v", err)
	}

	f.Fuzz(func(t *testing.T, inputID, reference string) {
		value, err := lookups.Resolve(context.Background(), inputID, reference)

		// 1. A parse never yields a value. Every source here is
		//    declared-not-implemented, so any non-empty return would mean
		//    the parse invented one.
		if value != "" {
			t.Fatalf("Resolve(%q, %q) returned a value with no source implemented: %q", inputID, reference, value)
		}

		// 2. Every failure is classified. An unclassified error would mean
		//    the parse reached a state nobody described, and callers switch
		//    on these sentinels to decide what to tell an operator.
		if err == nil {
			t.Fatalf("Resolve(%q, %q) returned no value and no error", inputID, reference)
		}
		switch {
		case errors.Is(err, credtype.ErrLookupReference),
			errors.Is(err, credtype.ErrLookupUnknown),
			errors.Is(err, credtype.ErrLookupNotImplemented):
		default:
			t.Fatalf("Resolve(%q, %q) returned an unclassified error: %v", inputID, reference, err)
		}

		// 3. The classification agrees with the parse. A reference with no
		//    colon, an empty source, or an empty remainder is malformed;
		//    anything else named a source, and the only question left is
		//    whether this platform has it. Getting this backwards would
		//    tell an operator their data is malformed when the real answer
		//    is that the source is not built yet, which sends them editing
		//    a correct row.
		source, rest, found := strings.Cut(reference, ":")
		malformed := !found || source == "" || rest == ""
		if malformed && !errors.Is(err, credtype.ErrLookupReference) {
			t.Fatalf("Resolve(%q, %q) is malformed but was not reported as such: %v", inputID, reference, err)
		}
		if !malformed && errors.Is(err, credtype.ErrLookupReference) {
			t.Fatalf("Resolve(%q, %q) names the source %q and was reported as malformed: %v",
				inputID, reference, source, err)
		}
	})
}
