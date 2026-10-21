// Package lock: fuzzing the lock record's decode, owners included.
package lock

import (
	"encoding/json"
	"testing"
)

// FuzzLockValueDecode: any record that decodes with holders yields one
// owner per holder, an owner-less holder reads as "" and matches no
// owner, and a record round-trips without inventing an owner. Old records
// (no "owner" field) and new ones both parse.
func FuzzLockValueDecode(f *testing.F) {
	f.Add([]byte(`{"mode":0,"holders":[{"id":"a","ttl":5000000000}]}`))
	f.Add([]byte(`{"mode":1,"holders":[{"id":"a","ttl":1,"owner":"job-1_rtr1-ab12cd34"},{"id":"b","ttl":2}]}`))
	f.Add([]byte(`{"mode":0,"holders":[]}`))
	f.Add([]byte(`not json`))
	f.Fuzz(func(t *testing.T, data []byte) {
		var v lockValue
		if err := json.Unmarshal(data, &v); err != nil {
			return
		}
		owners := ownersOf(v.Holders)
		if len(owners) != len(v.Holders) {
			t.Fatalf("%d holders gave %d owners", len(v.Holders), len(owners))
		}
		held := &HeldError{Owners: owners}
		if held.HeldBy("") {
			t.Fatal("an empty owner matched a hold")
		}
		for i, h := range v.Holders {
			if h.Owner == "" && owners[i] != "" {
				t.Fatalf("an owner-less holder read as %q", owners[i])
			}
		}
		again, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		var back lockValue
		if err := json.Unmarshal(again, &back); err != nil {
			t.Fatalf("a decoded record did not decode again: %v", err)
		}
		if len(back.Holders) != len(v.Holders) {
			t.Fatalf("round trip changed the holders: %d to %d", len(v.Holders), len(back.Holders))
		}
		for i := range back.Holders {
			if back.Holders[i].Owner != v.Holders[i].Owner {
				t.Fatalf("round trip changed holder %d's owner: %q to %q", i, v.Holders[i].Owner, back.Holders[i].Owner)
			}
		}
	})
}

// BenchmarkInProcessAcquire_Owner measures Acquire and Release with and
// without an owner, which adds a validation and a map entry.
func BenchmarkInProcessAcquire_Owner(b *testing.B) {
	for _, tc := range []struct {
		name  string
		owner string
	}{{"none", ""}, {"dispatch key", "01a15e80-5c6c-7634-99f9-52bca31d06fb_rtr1-ab12cd34"}} {
		b.Run(tc.name, func(b *testing.B) {
			m := NewInProcessManager()
			ctx := b.Context()
			for b.Loop() {
				lease, err := m.Acquire(ctx, "bench-device", 0, AcquireOptions{Owner: tc.owner})
				if err != nil {
					b.Fatal(err)
				}
				_ = lease.Release(ctx)
			}
		})
	}
}
