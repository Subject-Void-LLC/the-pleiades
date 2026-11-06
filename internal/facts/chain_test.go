// Tests of the per-device hash chain (chain.go), on its own and through the
// file store with its history file edited the way someone tampering would.
package facts_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
)

// chained builds n links of a valid chain.
func chained(t *testing.T, n int) []facts.Link {
	t.Helper()
	var links []facts.Link
	prev := ""
	at := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	for i := range n {
		key, value := "k", any(string(rune('a'+i)))
		hash, err := facts.Hash(key, value)
		if err != nil {
			t.Fatal(err)
		}
		by := facts.Provenance{Run: "r", Order: facts.Order{Source: facts.ClockSource, Token: uint64(i + 1)}}
		chain, err := facts.ChainHash("dev1", prev, key, hash, at, by)
		if err != nil {
			t.Fatal(err)
		}
		links = append(links, facts.Link{Key: key, Value: value, Hash: hash, ObservedAt: at, By: by, Prev: prev, Chain: chain})
		prev = chain
	}
	return links
}

// TestVerifyChain_FindsTheFirstEditDeletionOrMove: each kind of tampering is
// found at the value it broke, and only unchained values at the start are
// passed over.
func TestVerifyChain_FindsTheFirstEditDeletionOrMove(t *testing.T) {
	if v := facts.VerifyChain("dev1", chained(t, 3)); v.Break != nil || v.Values != 3 || v.Head == "" {
		t.Fatalf("an intact chain: %+v", v)
	}
	cases := map[string]struct {
		tamper func([]facts.Link) []facts.Link
		index  int
		reason string
	}{
		"an edited value": {func(l []facts.Link) []facts.Link { l[1].Value = "z"; return l }, 1, "fact hash"},
		"an edited value and its hash": {func(l []facts.Link) []facts.Link {
			l[1].Value = "z"
			l[1].Hash, _ = facts.Hash("k", "z")
			return l
		}, 1, "chain hash"},
		"an edited provenance":                         {func(l []facts.Link) []facts.Link { l[2].By.Run = "other"; return l }, 2, "chain hash"},
		"a chain removed from a value after the start": {func(l []facts.Link) []facts.Link { l[1].Chain = ""; return l }, 1, "carries no chain"},
		"a deleted value":                              {func(l []facts.Link) []facts.Link { return append(l[:1], l[2:]...) }, 1, "stored before it"},
		"two values swapped":                           {func(l []facts.Link) []facts.Link { l[0], l[1] = l[1], l[0]; return l }, 0, "stored before it"},
		"an unchained value after the chain began": {func(l []facts.Link) []facts.Link {
			l[2].Chain, l[2].Prev = "", ""
			return l
		}, 2, "carries no chain"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			v := facts.VerifyChain("dev1", tc.tamper(chained(t, 3)))
			if v.Break == nil || v.Break.Index != tc.index || !strings.Contains(v.Break.Reason, tc.reason) {
				t.Fatalf("verdict = %+v, want a break at %d naming %q", v, tc.index, tc.reason)
			}
		})
	}
	// One device's history does not verify as another's.
	if v := facts.VerifyChain("dev2", chained(t, 2)); v.Break == nil || v.Break.Index != 0 {
		t.Errorf("another device's chain verified: %+v", v)
	}
	// A value naming its run was stored after the chain existed, so one
	// whose chain was removed is a break, not an older value.
	stripped := chained(t, 2)
	stripped[0].Chain, stripped[0].Prev = "", ""
	if v := facts.VerifyChain("dev1", stripped); v.Break == nil || v.Break.Index != 0 {
		t.Errorf("a stripped chain with provenance verified: %+v", v)
	}
}

// TestVerifyChain_AnUnchainedValueAtTheStartIsABreak: a row with no chain and
// no provenance, put in front of a real chain by someone who can write the
// store, is found rather than passed over as an older value
// (FAILURE_PATTERNS 921). Its hash describes its value, so only the missing
// chain can give it away.
func TestVerifyChain_AnUnchainedValueAtTheStartIsABreak(t *testing.T) {
	hash, err := facts.Hash("ansible_kernel", "forged")
	if err != nil {
		t.Fatal(err)
	}
	forged := []facts.Link{{Key: "ansible_kernel", Value: "forged", Hash: hash}}
	for name, links := range map[string][]facts.Link{
		"alone":                 forged,
		"before a real chain":   append(append([]facts.Link{}, forged...), chained(t, 2)...),
		"two before a real one": append(append(append([]facts.Link{}, forged...), forged...), chained(t, 1)...),
	} {
		t.Run(name, func(t *testing.T) {
			v := facts.VerifyChain("dev1", links)
			if v.Break == nil || v.Break.Index != 0 || !strings.Contains(v.Break.Reason, "carries no chain") {
				t.Fatalf("verdict = %+v, want a break at the forged value", v)
			}
		})
	}
	// The control: the same chain without the forged row verifies.
	if v := facts.VerifyChain("dev1", chained(t, 2)); v.Break != nil {
		t.Fatalf("the real chain alone: %+v", v)
	}
}

// TestFileStore_VerifyFindsAnEditedOrDeletedLine edits the real history file
// and checks the store finds it.
func TestFileStore_VerifyFindsAnEditedOrDeletedLine(t *testing.T) {
	for name, tamper := range map[string]func([][]byte) [][]byte{
		"an edited value": func(lines [][]byte) [][]byte {
			lines[1] = bytes.Replace(lines[1], []byte(`"6.2"`), []byte(`"9.9"`), 1)
			return lines
		},
		"a deleted line": func(lines [][]byte) [][]byte { return append(lines[:1], lines[2:]...) },
	} {
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "facts")
			store, err := facts.NewFileStore(dir)
			if err != nil {
				t.Fatal(err)
			}
			at := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
			for i, kernel := range []string{"6.1", "6.2", "6.3"} {
				obs := facts.Observation{Entries: prepared(t, map[string]any{"ansible_kernel": kernel}), At: at.Add(time.Duration(i) * time.Minute),
					By: facts.Provenance{Run: "r", Order: facts.Order{Source: facts.ClockSource, Token: uint64(i + 1)}}}
				if err := store.Record(context.Background(), "dev1", obs); err != nil {
					t.Fatal(err)
				}
			}
			if v, err := store.Verify(context.Background(), "dev1"); err != nil || v.Break != nil || v.Values != 3 {
				t.Fatalf("before tampering: %+v, %v", v, err)
			}
			path := filepath.Join(dir, "dev1.jsonl")
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			lines := bytes.SplitAfter(bytes.TrimSuffix(raw, []byte("\n")), []byte("\n"))
			if len(lines) != 3 {
				t.Fatalf("%d lines", len(lines))
			}
			lines = tamper(lines)
			out := bytes.Join(lines, nil)
			if !bytes.HasSuffix(out, []byte("\n")) {
				out = append(out, '\n')
			}
			if err := os.WriteFile(path, out, 0o600); err != nil {
				t.Fatal(err)
			}
			v, err := store.Verify(context.Background(), "dev1")
			if err != nil || v.Break == nil || v.Break.Index != 1 {
				t.Fatalf("after tampering: %+v, %v; want a break at the second value", v, err)
			}
		})
	}
}
