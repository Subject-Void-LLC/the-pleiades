// Tests of Resumed (late.go): when a late change is followed in the history
// by the value confirmed after it, and when it is not.
package facts_test

import (
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
)

// TestResumed_OnlyAConfirmationAfterTheLateChangeWithNothingBetween: the one
// case that writes a value is a confirmation of the value current before the
// late change, made after it, with no stored value between them; every other
// case writes nothing, so no value is ever invented.
func TestResumed_OnlyAConfirmationAfterTheLateChangeWithNothingBetween(t *testing.T) {
	at := func(token uint64) (time.Time, facts.Provenance) {
		return time.Date(2026, 11, 5, 10, int(token), 0, 0, time.UTC),
			facts.Provenance{Run: "r" + string(rune('a'+token)), Order: facts.Order{Source: facts.ClockSource, Token: token}}
	}
	// stored is a stable value first observed at first and last confirmed at
	// last.
	stored := func(value string, first, last uint64) facts.Entry {
		hash, err := facts.Hash("k", value)
		if err != nil {
			t.Fatal(err)
		}
		fAt, fBy := at(first)
		lAt, lBy := at(last)
		return facts.Entry{Key: "k", Value: value, Hash: hash, Kind: facts.KindStable, ObservedAt: fAt, By: fBy, LastSeenAt: lAt, LastBy: lBy}
	}
	late := func(value string, token uint64) facts.Entry {
		e := stored(value, token, token)
		return facts.Entry{Key: "k", Hash: e.Hash, ObservedAt: e.ObservedAt, By: e.By}
	}
	volatile := stored("A", 5, 11)
	volatile.Kind = facts.KindVolatile

	cases := map[string]struct {
		prior []facts.Entry
		late  facts.Entry
		// want is the value written after late and the token it is dated
		// at, or "" for none.
		want  string
		token uint64
	}{
		"confirmed after the late change":           {[]facts.Entry{stored("A", 5, 11)}, late("B", 10), "A", 11},
		"confirmed after it, a later change stored": {[]facts.Entry{stored("A", 5, 11), stored("C", 13, 13)}, late("B", 10), "A", 11},
		"nothing stored before the late change":     {[]facts.Entry{stored("C", 13, 13)}, late("B", 10), "", 0},
		"an ordinary change, not late":              {[]facts.Entry{stored("A", 5, 9)}, late("B", 10), "", 0},
		"the late value is the one before it":       {[]facts.Entry{stored("B", 5, 11)}, late("B", 10), "", 0},
		"a stored value between them":               {[]facts.Entry{stored("A", 5, 15), stored("C", 12, 12)}, late("B", 10), "", 0},
		"only a volatile value before it":           {[]facts.Entry{volatile}, late("B", 10), "", 0},
		"the latest of two spells before it":        {[]facts.Entry{stored("A", 3, 3), stored("D", 4, 4), stored("A", 6, 11)}, late("B", 10), "A", 11},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, ok := facts.Resumed(tc.prior, tc.late)
			if tc.want == "" {
				if ok {
					t.Fatalf("Resumed = %+v, want nothing written", got)
				}
				return
			}
			wantAt, wantBy := at(tc.token)
			if !ok || got.Value != tc.want || !got.ObservedAt.Equal(wantAt) || got.By != wantBy || got.LastBy != wantBy || got.Kind != facts.KindStable {
				t.Fatalf("Resumed = %+v, %v; want %s first observed at token %d by its run", got, ok, tc.want, tc.token)
			}
		})
	}
}
