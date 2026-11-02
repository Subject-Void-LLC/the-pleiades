// Fuzz and benchmark tests of the shared fact rules (Phase 41): a value is
// device-supplied, so deeply nested and adversarially typed ones must never
// panic, must hash the same after crossing JSON, and must stay bounded.
package facts_test

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
)

// FuzzPrepare decodes arbitrary JSON as one observed value and holds Prepare
// to its rules: no panic; a stored value within the bound; and the stored
// value, sent through JSON again as a Runner sends it, verified with the same
// hash.
func FuzzPrepare(f *testing.F) {
	for _, seed := range []string{
		`"6.1"`, `9007199254740993`, `1e308`, `-0`, `[1,"x",null,true]`,
		`{"a":{"b":{"c":[{"d":[1,2,{"e":"f"}]}]}}}`, `"` + strings.Repeat("x", 70000) + `"`,
		`"\u0000\u001b[2J\u202e"`, `{"":1,"a b":2}`, `[` + strings.Repeat("[", 500) + strings.Repeat("]", 500) + `]`,
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		dec := json.NewDecoder(strings.NewReader(raw))
		dec.UseNumber()
		var value any
		if err := dec.Decode(&value); err != nil {
			return
		}
		entries, _, err := facts.Prepare(map[string]any{"k": value}, facts.Guard{})
		if err != nil {
			t.Fatalf("one key refused whole: %v", err)
		}
		for _, e := range entries {
			canon, err := facts.Canonical(e.Value)
			if err != nil || len(canon) > facts.MaxValueBytes {
				t.Fatalf("stored a value of %d bytes (%v)", len(canon), err)
			}
			wire, err := json.Marshal(facts.Batch{Entries: []facts.Entry{e}})
			if err != nil {
				t.Fatal(err)
			}
			var back facts.Batch
			d := json.NewDecoder(bytes.NewReader(wire))
			d.UseNumber()
			if err := d.Decode(&back); err != nil {
				t.Fatal(err)
			}
			verified, refused, err := facts.Verify(back.Entries, facts.Guard{})
			if err != nil || refused != 0 || len(verified) != 1 || verified[0].Hash != e.Hash {
				t.Fatalf("after JSON: %+v, refused %d, %v; want the same hash %s", verified, refused, err, e.Hash)
			}
		}
	})
}

// BenchmarkPrepare is one task's observation of facts.gather's size.
func BenchmarkPrepare(b *testing.B) {
	observed := map[string]any{}
	for i := range 20 {
		observed["ansible_fact_"+strconv.Itoa(i)] = "value " + strconv.Itoa(i)
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, _, err := facts.Prepare(observed, facts.Guard{Secrets: []string{"s3cr3t"}}); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkFileStoreRecord writes one task's facts to a device whose history
// already holds a thousand values, the write a run makes per task.
func BenchmarkFileStoreRecord(b *testing.B) {
	store, err := facts.NewFileStore(filepath.Join(b.TempDir(), "facts"))
	if err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()
	base := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	for i := range 1000 {
		entries, _, _ := facts.Prepare(map[string]any{"uptime": i}, facts.Guard{})
		if err := store.Record(ctx, "dev", entries, base.Add(time.Duration(i)*time.Second)); err != nil {
			b.Fatal(err)
		}
	}
	entries, _, _ := facts.Prepare(map[string]any{"ansible_kernel": "6.1", "ansible_hostname": "web1"}, facts.Guard{})
	i := 0
	for b.Loop() {
		i++
		if err := store.Record(ctx, "dev", entries, base.Add(time.Duration(2000+i)*time.Second)); err != nil {
			b.Fatal(err)
		}
	}
}
