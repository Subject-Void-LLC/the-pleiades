// Tests of the per-device hash chain (chain.go), on its own and through the
// file store with its history file edited the way someone tampering would.
package facts_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts/factstest"
)

// chained builds n links of a valid chain.
func chained(t *testing.T, n int) []facts.Link {
	t.Helper()
	var links []facts.Link
	prev := ""
	at := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	for i := range n {
		key, value := "k", any(string(rune('a'+i)))
		hash, err := factstest.Keys().Hash("dev1", key, value)
		if err != nil {
			t.Fatal(err)
		}
		by := facts.Provenance{Run: "r", Order: facts.Order{Source: facts.ClockSource, Token: uint64(i + 1)}}
		chain, err := factstest.Keys().ChainHash("dev1", prev, key, hash, at, by)
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
	if v := facts.VerifyChain("dev1", chained(t, 3), factstest.Keys()); v.Break != nil || v.Values != 3 || v.Head == "" {
		t.Fatalf("an intact chain: %+v", v)
	}
	cases := map[string]struct {
		tamper func([]facts.Link) []facts.Link
		index  int
		reason string
		kind   facts.BreakKind
	}{
		"an edited value": {func(l []facts.Link) []facts.Link { l[1].Value = "z"; return l }, 1, "fact hash", facts.BreakValue},
		"an edited value and its hash": {func(l []facts.Link) []facts.Link {
			l[1].Value = "z"
			l[1].Hash, _ = factstest.Keys().Hash("dev1", "k", "z")
			return l
		}, 1, "chain hash", facts.BreakChainHash},
		"an edited provenance":                         {func(l []facts.Link) []facts.Link { l[2].By.Run = "other"; return l }, 2, "chain hash", facts.BreakChainHash},
		"a chain removed from a value after the start": {func(l []facts.Link) []facts.Link { l[1].Chain = ""; return l }, 1, "carries no chain", facts.BreakUnchained},
		"a deleted value":                              {func(l []facts.Link) []facts.Link { return append(l[:1], l[2:]...) }, 1, "stored before it", facts.BreakLink},
		"two values swapped":                           {func(l []facts.Link) []facts.Link { l[0], l[1] = l[1], l[0]; return l }, 0, "stored before it", facts.BreakLink},
		"an unchained value after the chain began": {func(l []facts.Link) []facts.Link {
			l[2].Chain, l[2].Prev = "", ""
			return l
		}, 2, "carries no chain", facts.BreakUnchained},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			links := tc.tamper(chained(t, 3))
			v := facts.VerifyChain("dev1", links, factstest.Keys())
			if v.Break == nil || v.Break.Index != tc.index || !strings.Contains(v.Break.Reason, tc.reason) || v.Break.Kind != tc.kind {
				t.Fatalf("verdict = %+v, want a %s break at %d naming %q", v, tc.kind, tc.index, tc.reason)
			}
			// One link at a time, as an inspection walks a paged history: the
			// same verdict, and the same break for every link after it.
			w := facts.NewChainWalk("dev1", factstest.Keys())
			for _, l := range links {
				w.Next(l)
			}
			if got := w.Verdict(); got.Values != v.Values || got.Head != v.Head || *got.Break != *v.Break {
				t.Errorf("the walk's verdict %+v is not VerifyChain's %+v", got, v)
			}
		})
	}
	if w, links := facts.NewChainWalk("dev1", factstest.Keys()), chained(t, 3); w.Next(links[0]) != nil || w.Next(links[1]) != nil || w.Next(links[2]) != nil ||
		w.Verdict().Head != links[2].Chain || w.Verdict().Values != 3 {
		t.Errorf("an intact chain walked link by link: %+v", w.Verdict())
	}
	// One device's history does not verify as another's.
	if v := facts.VerifyChain("dev2", chained(t, 2), factstest.Keys()); v.Break == nil || v.Break.Index != 0 {
		t.Errorf("another device's chain verified: %+v", v)
	}
	// A value naming its run was stored after the chain existed, so one
	// whose chain was removed is a break, not an older value.
	stripped := chained(t, 2)
	stripped[0].Chain, stripped[0].Prev = "", ""
	if v := facts.VerifyChain("dev1", stripped, factstest.Keys()); v.Break == nil || v.Break.Index != 0 {
		t.Errorf("a stripped chain with provenance verified: %+v", v)
	}
}

// TestKeys_ChainHashIsKeyed: a link is not the unkeyed SHA-256 of what it
// covers, which anyone holding a copy of the store could recompute from a
// guessed key and the row's other columns; it differs under another key and
// on another device, is the same on every call, needs keys, and a chain
// linked under one key does not verify under another.
func TestKeys_ChainHashIsKeyed(t *testing.T) {
	keys := factstest.Keys()
	other, err := facts.NewKeys(bytes.Repeat([]byte{7}, 32), "test")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	by := facts.Provenance{Run: "r", Order: facts.Order{Source: facts.ClockSource, Token: 1}}
	hash, err := keys.Hash("dev1", "package.openssh-server", "9.6")
	if err != nil {
		t.Fatal(err)
	}
	link, err := keys.ChainHash("dev1", "", "package.openssh-server", hash, at, by)
	if err != nil || len(link) != 64 {
		t.Fatalf("ChainHash = %q, %v; want 64 hex characters", link, err)
	}
	canon, err := facts.Canonical([]any{"dev1", "", "package.openssh-server", hash, facts.StoredTime(at).Format(time.RFC3339Nano), by})
	if err != nil {
		t.Fatal(err)
	}
	if unkeyed := sha256.Sum256(canon); hex.EncodeToString(unkeyed[:]) == link {
		t.Error("the link is the unkeyed SHA-256 of what it covers: a guessed key confirms itself")
	}
	if again, _ := factstest.Keys().ChainHash("dev1", "", "package.openssh-server", hash, at, by); again != link {
		t.Error("one link differs between Keys of one secret")
	}
	if l, _ := other.ChainHash("dev1", "", "package.openssh-server", hash, at, by); l == link {
		t.Error("two keys give one link")
	}
	if l, _ := keys.ChainHash("dev2", "", "package.openssh-server", hash, at, by); l == link {
		t.Error("one link on two devices")
	}
	if _, err := (*facts.Keys)(nil).ChainHash("dev1", "", "k", hash, at, by); !errors.Is(err, facts.ErrNoKeys) {
		t.Errorf("ChainHash with no keys = %v, want ErrNoKeys", err)
	}
	// A link whose fact hash is other's but which was linked under keys: the
	// hash verifies under other, so the break is the link's own.
	otherHash, err := other.Hash("dev1", "package.openssh-server", "9.6")
	if err != nil {
		t.Fatal(err)
	}
	crossed, err := keys.ChainHash("dev1", "", "package.openssh-server", otherHash, at, by)
	if err != nil {
		t.Fatal(err)
	}
	links := []facts.Link{{Key: "package.openssh-server", Value: "9.6", Hash: otherHash, ObservedAt: at, By: by, Chain: crossed}}
	if v := facts.VerifyChain("dev1", links, other); v.Break == nil || v.Break.Index != 0 || !strings.Contains(v.Break.Reason, "chain hash") {
		t.Errorf("a link made under one key verified under another: %+v", v)
	}
	// The control: linked under other, the same value verifies under it.
	links[0].Chain, _ = other.ChainHash("dev1", "", "package.openssh-server", otherHash, at, by)
	if v := facts.VerifyChain("dev1", links, other); v.Break != nil {
		t.Errorf("the control failed: a link made under other does not verify under it: %+v", v)
	}
}

// TestSameMAC: two MACs are the same only when every byte and the length
// agree.
func TestSameMAC(t *testing.T) {
	for _, c := range []struct {
		a, b string
		same bool
	}{
		{"ab12", "ab12", true},
		{"", "", true},
		{"ab12", "ab13", false},
		{"ab12", "ab1", false},
		{"ab12", "", false},
	} {
		if got := facts.SameMAC(c.a, c.b); got != c.same {
			t.Errorf("SameMAC(%q, %q) = %v, want %v", c.a, c.b, got, c.same)
		}
	}
}

// TestVerifyChain_AnUnchainedValueAtTheStartIsABreak: a row with no chain and
// no provenance, put in front of a real chain by someone who can write the
// store, is found rather than passed over as an older value
// (FAILURE_PATTERNS 921). Its hash describes its value, so only the missing
// chain can give it away.
func TestVerifyChain_AnUnchainedValueAtTheStartIsABreak(t *testing.T) {
	hash, err := factstest.Keys().Hash("dev1", "ansible_kernel", "forged")
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
			v := facts.VerifyChain("dev1", links, factstest.Keys())
			if v.Break == nil || v.Break.Index != 0 || !strings.Contains(v.Break.Reason, "carries no chain") {
				t.Fatalf("verdict = %+v, want a break at the forged value", v)
			}
		})
	}
	// The control: the same chain without the forged row verifies.
	if v := facts.VerifyChain("dev1", chained(t, 2), factstest.Keys()); v.Break != nil {
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
			store, err := facts.NewFileStore(dir, factstest.Keys())
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
			// Edited by someone holding the project's key, who can open and
			// seal lines again: the chain is what finds them.
			path := filepath.Join(dir, "dev1.jsonl")
			lines := factstest.OpenLines(t, factstest.Keys(), path, "dev1", facts.RoleValues)
			if len(lines) != 3 {
				t.Fatalf("%d lines", len(lines))
			}
			factstest.WriteLines(t, factstest.Keys(), path, "dev1", facts.RoleValues, tamper(lines))
			v, err := store.Verify(context.Background(), "dev1")
			if err != nil || v.Break == nil || v.Break.Index != 1 {
				t.Fatalf("after tampering: %+v, %v; want a break at the second value", v, err)
			}
		})
	}
}
