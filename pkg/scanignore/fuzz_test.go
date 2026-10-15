// Fuzzing for the ignore file parser.
package scanignore_test

import (
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/scanignore"
)

// FuzzParse holds Parse to its contract on any input: it returns or
// refuses without panicking, and every entry it returns is complete and
// unique, and can be judged.
func FuzzParse(f *testing.F) {
	for _, seed := range []string{
		"- id: A\n  reason: r\n  until: 2026-01-01\n",
		"[]", "", "- {id: A, reason: r, until: 2026-02-30}", "- id: &a A\n  reason: *a\n  until: 2026-01-01\n",
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		file, err := scanignore.Parse(data)
		if err != nil {
			return
		}
		seen := map[string]bool{}
		for _, e := range file.Entries {
			if e.ID == "" || e.Reason == "" || e.Until.IsZero() || seen[e.ID] {
				t.Fatalf("an entry Parse accepted is incomplete or repeated: %+v", e)
			}
			seen[e.ID] = true
		}
		j := file.Judge(time.Now())
		for _, e := range file.Entries {
			_, _ = j.Ignored(e.ID)
		}
		_, _ = j.Expired(), j.Unused()
	})
}
