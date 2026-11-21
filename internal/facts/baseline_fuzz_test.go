// Fuzz tests of baselines and drift (Phase 41d): Drift over any baseline and
// any current facts never panics and keeps its rules, and a baseline line
// decodes only as what it is, wherever its bytes came from.
package facts_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts/factstest"
)

// driftInput is what FuzzDrift decodes: a baseline's pairs, the keys it
// recorded volatile and its selector, and a device's current entries.
type driftInput struct {
	Pairs    []facts.BaselinePair `json:"pairs"`
	Volatile []string             `json:"volatile"`
	Keys     []string             `json:"keys"`
	Current  []facts.Entry        `json:"current"`
}

// FuzzDrift holds Drift to its rules over arbitrary input: no panic; a
// device with no entries, or none of whose baseline's keys was compared and
// with no change, is unknown; a volatile key is never a change and never
// unconfirmed; each change and each unconfirmed key is one key, in key
// order, and no key is both; drifted exactly when there is a change; every
// compared key matches, changed, was removed or is unconfirmed; and no value
// is shown beside a digest.
func FuzzDrift(f *testing.F) {
	for _, seed := range []string{
		`{"pairs":[{"key":"a","hash":"1","value":"x"}],"current":[{"key":"a","hash":"1","value":"x","kind":"stable"}]}`,
		`{"pairs":[{"key":"a","hash":"1"},{"key":"a","hash":"2"}],"current":[{"key":"a","hash":"2","kind":"volatile"},{"key":"b","hash":"3"}]}`,
		`{"pairs":[{"key":"f","not_comparable":true},{"key":"g","hash":"9","value":{"hmac_sha256":"ab"}}],"keys":["g*"],"current":[{"key":"g","hash":"8","value":{"x":[{"sha256":"1"}]}}]}`,
		`{"pairs":[],"current":[]}`,
		`{"pairs":[{"key":"p.a","hash":"1"}],"current":[{"key":"p.a","hash":"1","last_seen_at":"2026-01-01T00:00:00Z"},{"key":"p.b","hash":"2","last_seen_at":"2026-02-01T00:00:00Z"}]}`,
		`{"pairs":[{"key":"p.a","hash":"1"}],"volatile":["p.c"],"current":[{"key":"p.a","hash":"2","last_seen_at":"2026-01-01T00:00:00Z"},{"key":"p.c","hash":"2","last_seen_at":"2026-02-01T00:00:00Z"},{"key":"c.x_digest","hash":"5","value":"ab"}]}`,
	} {
		f.Add(seed)
	}
	at := time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)
	f.Fuzz(func(t *testing.T, raw string) {
		var in driftInput
		dec := json.NewDecoder(bytes.NewReader([]byte(raw)))
		dec.UseNumber()
		if err := dec.Decode(&in); err != nil {
			return
		}
		b := facts.Baseline{Name: "b", Device: "d", KeysID: "k", Keys: in.Keys, Pairs: in.Pairs, Volatile: in.Volatile}
		rep := facts.Drift(b, in.Current, at)
		switch {
		case len(in.Current) == 0 || rep.Compared == 0 && len(rep.Changes) == 0:
			if rep.State != facts.DriftUnknown || rep.Reason == "" {
				t.Fatalf("state %s (%q), want unknown with a reason", rep.State, rep.Reason)
			}
			return
		case rep.State != facts.DriftInSync && rep.State != facts.DriftDrifted:
			t.Fatalf("state %s for a comparable device", rep.State)
		case (rep.State == facts.DriftDrifted) != (len(rep.Changes) > 0):
			t.Fatalf("state %s with %d changes", rep.State, len(rep.Changes))
		}
		volatile, inBaseline := map[string]bool{}, map[string]bool{}
		for _, e := range in.Current {
			if e.Kind == facts.KindVolatile {
				volatile[e.Key] = true
			}
		}
		for _, p := range in.Pairs {
			inBaseline[p.Key] = true
		}
		listed := map[string]bool{}
		fromBaseline := 0
		for i, c := range rep.Changes {
			if volatile[c.Key] {
				t.Fatalf("volatile key %q is a change", c.Key)
			}
			if i > 0 && rep.Changes[i-1].Key >= c.Key {
				t.Fatalf("changes out of order or repeated: %q then %q", rep.Changes[i-1].Key, c.Key)
			}
			if c.Digest && (c.BaselineValue != nil || c.CurrentValue != nil) {
				t.Fatalf("%q holds a digest and shows a value", c.Key)
			}
			if facts.DigestKey(c.Key) && !c.Digest {
				t.Fatalf("%q names a digest and is not hidden", c.Key)
			}
			if c.Change != facts.ChangeAdded {
				fromBaseline++
			}
			listed[c.Key] = true
		}
		for i, u := range rep.Unconfirmed {
			if volatile[u.Key] || listed[u.Key] {
				t.Fatalf("unconfirmed key %q is volatile or also a change", u.Key)
			}
			if i > 0 && rep.Unconfirmed[i-1].Key >= u.Key {
				t.Fatalf("unconfirmed out of order or repeated: %q then %q", rep.Unconfirmed[i-1].Key, u.Key)
			}
			if inBaseline[u.Key] {
				fromBaseline++
			} else if u.WouldBe != facts.ChangeAdded {
				t.Fatalf("unconfirmed %q is no baseline key and would be %s", u.Key, u.WouldBe)
			}
		}
		if rep.Compared != rep.Matching+fromBaseline {
			t.Fatalf("compared %d, matching %d, changed, removed or unconfirmed %d", rep.Compared, rep.Matching, fromBaseline)
		}
		if !sort.StringsAreSorted(rep.NotComparable) || !sort.StringsAreSorted(rep.KindChanged) {
			t.Fatal("a key list is out of order")
		}
	})
}

// FuzzDecodeBaselineLine seals arbitrary plaintext as a baseline line and
// decodes it: no panic; what decodes is a baseline of the device with a
// valid name and well-formed keys; the same line at another offset or for
// another device never opens; and the plaintext itself, unsealed, never
// decodes.
func FuzzDecodeBaselineLine(f *testing.F) {
	keys := factstest.Keys()
	good, err := json.Marshal(facts.Baseline{Name: "base", Device: "dev1", KeysID: keys.ID(), By: "u", Reason: "r", Pairs: []facts.BaselinePair{{Key: "os.family", Hash: "ab", Value: "alpine"}, {Key: "file.x", NotComparable: true}}})
	if err != nil {
		f.Fatal(err)
	}
	for _, seed := range [][]byte{good, []byte(`{"name":"-x","device":"dev1","keys_id":"k"}`), []byte(`{"name":"b","device":"dev2","keys_id":"k"}`), []byte(`{"name":"b","device":"dev1","keys_id":"k","pairs":[{"key":""}]}`), []byte("not json"), nil} {
		f.Add(seed, int64(0))
		f.Add(seed, int64(4096))
	}
	f.Fuzz(func(t *testing.T, plain []byte, offset int64) {
		if offset < 0 {
			offset = -offset
		}
		if _, err := facts.DecodeBaselineLine(keys, "dev1", offset, plain); err == nil {
			t.Fatal("an unsealed line decoded")
		}
		sealed, err := keys.SealLine("dev1", facts.RoleBaseline, offset, plain)
		if err != nil {
			t.Fatal(err)
		}
		for _, wrong := range []struct {
			device string
			offset int64
		}{{"dev2", offset}, {"dev1", offset + 1}} {
			if _, err := facts.DecodeBaselineLine(keys, wrong.device, wrong.offset, sealed); !errors.Is(err, facts.ErrSealed) {
				t.Fatalf("a line opened as %s at %d: %v", wrong.device, wrong.offset, err)
			}
		}
		b, err := facts.DecodeBaselineLine(keys, "dev1", offset, append(sealed, '\n'))
		if err != nil {
			return
		}
		if b.Device != "dev1" || b.KeysID == "" || !facts.ReadableKey(b.Name) {
			t.Fatalf("decoded %+v", b)
		}
		for _, p := range b.Pairs {
			if !facts.ReadableKey(p.Key) || p.Hash == "" && !p.NotComparable {
				t.Fatalf("decoded a pair %+v", p)
			}
		}
		for _, key := range b.Volatile {
			if !facts.ReadableKey(key) {
				t.Fatalf("decoded a volatile key %q", key)
			}
		}
	})
}
