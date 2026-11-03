// Fuzz and benchmark tests of the shared fact rules (Phase 41, 41b): a value
// is device-supplied, so deeply nested and adversarially typed ones must
// never panic, must hash the same after crossing JSON, must stay bounded and
// must be of the type the method declared; and a chain must verify whatever
// its links hold.
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
		if err := store.Record(ctx, "dev", facts.Observation{Entries: entries, At: base.Add(time.Duration(i) * time.Second)}); err != nil {
			b.Fatal(err)
		}
	}
	entries, _, _ := facts.Prepare(map[string]any{"ansible_kernel": "6.1", "ansible_hostname": "web1"}, facts.Guard{})
	i := 0
	for b.Loop() {
		i++
		if err := store.Record(ctx, "dev", facts.Observation{Entries: entries, At: base.Add(time.Duration(2000+i) * time.Second)}); err != nil {
			b.Fatal(err)
		}
	}
}

// FuzzPrepareDeclared holds Prepare's declaration rule to arbitrary values:
// no panic, and every value stored is of the type the method declared (a
// number for a number, a whole number for an integer).
func FuzzPrepareDeclared(f *testing.F) {
	for _, seed := range []string{`"6.1"`, `7`, `7.5`, `1e400`, `18446744073709551616`, `true`, `null`, `[1]`, `{"a":1}`} {
		f.Add(seed, "integer")
		f.Add(seed, "string")
		f.Add(seed, "number")
	}
	f.Fuzz(func(t *testing.T, raw, typ string) {
		dec := json.NewDecoder(strings.NewReader(raw))
		dec.UseNumber()
		var value any
		if err := dec.Decode(&value); err != nil {
			return
		}
		declared := func(string) (facts.Spec, bool) {
			return facts.Spec{Kind: facts.KindStable, Type: facts.ValueType(typ)}, true
		}
		entries, problems, err := facts.Prepare(map[string]any{"k": value}, facts.Guard{Declared: declared})
		if err != nil || len(entries)+len(problems) != 1 {
			t.Fatalf("one key: %d stored, %d refused, %v", len(entries), len(problems), err)
		}
		if typ == "" {
			// No declared type checks none: any value may be stored.
			return
		}
		for _, e := range entries {
			switch v := e.Value.(type) {
			case string:
				if typ != "string" {
					t.Fatalf("a string stored as %s", typ)
				}
			case json.Number:
				if typ != "number" && typ != "integer" {
					t.Fatalf("a number stored as %s", typ)
				}
				if typ == "integer" && strings.ContainsAny(v.String(), ".eE") {
					t.Fatalf("%s stored as an integer", v)
				}
			default:
				if typ == "string" || typ == "number" || typ == "integer" {
					t.Fatalf("a %T stored as %s", v, typ)
				}
			}
		}
	})
}

// FuzzVerifyChain holds the chain check to arbitrary links: it never panics,
// a break names a link that exists, and an intact chain built from the same
// keys and values verifies whole.
func FuzzVerifyChain(f *testing.F) {
	f.Add("k", "v", "", "x", uint64(1))
	f.Add("ansible_kernel", "6.1", "deadbeef", "", uint64(0))
	f.Fuzz(func(t *testing.T, key, value, prev, chain string, token uint64) {
		at := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
		by := facts.Provenance{Run: "r", Order: facts.Order{Source: facts.ClockSource, Token: token}}
		hash, _ := facts.Hash(key, value)
		forged := []facts.Link{{Key: key, Value: value, Hash: hash, ObservedAt: at, By: by, Prev: prev, Chain: chain}}
		if v := facts.VerifyChain("dev1", forged); v.Break != nil && (v.Break.Index < 0 || v.Break.Index >= len(forged)) {
			t.Fatalf("a break at %d of %d links", v.Break.Index, len(forged))
		}
		real, err := facts.ChainHash("dev1", "", key, hash, at, by)
		if err != nil {
			return
		}
		next, _ := facts.ChainHash("dev1", real, key, hash, at.Add(time.Second), by)
		intact := []facts.Link{
			{Key: key, Value: value, Hash: hash, ObservedAt: at, By: by, Chain: real},
			{Key: key, Value: value, Hash: hash, ObservedAt: at.Add(time.Second), By: by, Prev: real, Chain: next},
		}
		if v := facts.VerifyChain("dev1", intact); v.Break != nil || v.Values != 2 || v.Head != next {
			t.Fatalf("an intact chain: %+v", v)
		}
	})
}

// BenchmarkFileStoreRecordVolatile confirms a volatile fact on a device whose
// state already holds a hundred of them: the write facts.gather's uptime
// makes on every run.
func BenchmarkFileStoreRecordVolatile(b *testing.B) {
	store, err := facts.NewFileStore(filepath.Join(b.TempDir(), "facts"))
	if err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()
	base := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	volatile := func(string) (facts.Spec, bool) { return facts.Spec{Kind: facts.KindVolatile}, true }
	observed := map[string]any{}
	for i := range 100 {
		observed["volatile_"+strconv.Itoa(i)] = i
	}
	seed, _, _ := facts.Prepare(observed, facts.Guard{Declared: volatile})
	if err := store.Record(ctx, "dev", facts.Observation{Entries: seed, At: base}); err != nil {
		b.Fatal(err)
	}
	i := 0
	for b.Loop() {
		i++
		entries, _, _ := facts.Prepare(map[string]any{"volatile_0": i}, facts.Guard{Declared: volatile})
		obs := facts.Observation{Entries: entries, At: base.Add(time.Duration(i) * time.Second),
			By: facts.Provenance{Order: facts.Order{Source: facts.ClockSource, Token: uint64(i)}}}
		if err := store.Record(ctx, "dev", obs); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkFileStoreVerify checks the chain of a device holding a thousand
// stored values.
func BenchmarkFileStoreVerify(b *testing.B) {
	store, err := facts.NewFileStore(filepath.Join(b.TempDir(), "facts"))
	if err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()
	base := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	for i := range 1000 {
		entries, _, _ := facts.Prepare(map[string]any{"version": strconv.Itoa(i)}, facts.Guard{})
		if err := store.Record(ctx, "dev", facts.Observation{Entries: entries, At: base.Add(time.Duration(i) * time.Second)}); err != nil {
			b.Fatal(err)
		}
	}
	for b.Loop() {
		if v, err := store.Verify(ctx, "dev"); err != nil || v.Break != nil || v.Values != 1000 {
			b.Fatalf("%+v, %v", v, err)
		}
	}
}
