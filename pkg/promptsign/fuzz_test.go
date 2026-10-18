// Fuzzing for the canonical form.
package promptsign_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/promptsign"
)

// FuzzCanonical holds the canonical form to its contract on any input: it
// returns or refuses without panicking; what it returns is its own
// canonical form (idempotent); and it means what the input meant to
// encoding/json.
func FuzzCanonical(f *testing.F) {
	for _, seed := range []string{
		`{"b":1,"a":[1.5,-0,"é\n"]}`, `[]`, `"😀"`, `{"a":{"a":{"a":null}}}`, `0.1`,
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		canon, err := promptsign.Canonical(data)
		if err != nil {
			return
		}
		again, err := promptsign.Canonical(canon)
		if err != nil || string(again) != string(canon) {
			t.Fatalf("not idempotent: %q then %q (%v)", canon, again, err)
		}
		var a, b any
		if json.Unmarshal(data, &a) != nil || json.Unmarshal(canon, &b) != nil {
			t.Fatalf("encoding/json reads one but not the other: %q and %q", data, canon)
		}
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("the canonical form means something else: %q and %q", data, canon)
		}
	})
}
