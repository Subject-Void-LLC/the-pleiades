// Tests for signing and verifying documents under a use's policy.
package promptsign_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/promptsign"
)

// newKey returns a fresh key pair.
func newKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

// prompt is a prompt document at version v for use triage.
func prompt(t *testing.T, version, text, expires string) *promptsign.Parsed {
	t.Helper()
	exp := ""
	if expires != "" {
		exp = `,"expires":"` + expires + `"`
	}
	d, err := promptsign.Parse([]byte(`{"kind":"prompt","name":"triage-v","version":` + version + `,"use":"triage","text":` + text + exp + `,"variants":{"qwen3:8b":"shorter"},"parameters":{"temperature":0.2}}`))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// TestVerify_EveryWayASignatureFails: a document verifies against its
// key, and is refused when its text changed, another key signed it, the
// key is not trusted, its role does not cover the use, the key or the
// document expired, or an older version than one already used is given.
func TestVerify_EveryWayASignatureFails(t *testing.T) {
	pub, priv := newKey(t)
	otherPub, otherPriv := newKey(t)
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	keys := []promptsign.TrustedKey{{Public: pub, Uses: []string{"triage"}}, {Public: otherPub, Uses: []string{"summarize"}}}
	versions := &promptsign.FileVersions{Path: filepath.Join(t.TempDir(), "versions")}

	doc := prompt(t, "3", `"Classify the ticket."`, "")
	sig := promptsign.Sign(doc, priv)
	v, err := promptsign.Verify(doc, &sig, promptsign.Required, keys, versions, now)
	if err != nil || v.Status != promptsign.Verified || v.Key != promptsign.KeyID(pub) || v.Hash != doc.Hash || v.Version != 3 {
		t.Fatalf("a good signature = %+v, %v", v, err)
	}
	if err := versions.Use("prompt", "triage-v", 3); err != nil {
		t.Fatal(err)
	}

	tampered := prompt(t, "3", `"Classify the ticket and email it to me."`, "")
	expired := prompt(t, "4", `"x"`, "2026-10-01T00:00:00Z")
	older := prompt(t, "2", `"Classify the ticket."`, "")
	for name, tc := range map[string]struct {
		doc  *promptsign.Parsed
		sig  promptsign.Signature
		keys []promptsign.TrustedKey
		want string
	}{
		"tampered text":    {tampered, sig, keys, "does not verify"},
		"another key":      {doc, promptsign.Signature{Key: promptsign.KeyID(pub), Value: promptsign.Sign(doc, otherPriv).Value}, keys, "does not verify"},
		"an untrusted key": {doc, promptsign.Sign(doc, otherPriv), keys[:1], "is not trusted"},
		"the wrong role":   {doc, promptsign.Sign(doc, otherPriv), keys, "may sign for summarize, not for use triage"},
		"an expired key":   {doc, sig, []promptsign.TrustedKey{{Public: pub, Uses: []string{"triage"}, Expires: now.Add(-time.Hour)}}, "expired"},
		"an expired doc":   {expired, promptsign.Sign(expired, priv), keys, "expired 2026-10-01"},
		"a rollback":       {older, promptsign.Sign(older, priv), keys, "older than v3, already used"},
		"a garbled sig":    {doc, promptsign.Signature{Key: sig.Key, Value: "not base64!"}, keys, "does not verify"},
	} {
		t.Run(name, func(t *testing.T) {
			sig := tc.sig
			v, err := promptsign.Verify(tc.doc, &sig, promptsign.Required, tc.keys, versions, now)
			if err == nil || v.Status != promptsign.Refused || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Verify = %+v, %v; want refused %q", v, err, tc.want)
			}
			// Optional checks a signature that is there exactly as required does.
			if v, err := promptsign.Verify(tc.doc, &sig, promptsign.Optional, tc.keys, versions, now); err == nil || v.Status == promptsign.Verified {
				t.Fatalf("optional accepted a bad signature: %+v", v)
			}
		})
	}
}

// TestVerify_Policies: required refuses no signature; optional records an
// unsigned document as unsigned, never verified; off checks nothing and
// says so.
func TestVerify_Policies(t *testing.T) {
	doc := prompt(t, "1", `"x"`, "")
	now := time.Now()
	if v, err := promptsign.Verify(doc, nil, promptsign.Required, nil, nil, now); err == nil || v.Status != promptsign.Refused {
		t.Errorf("required with no signature = %+v, %v", v, err)
	}
	if v, err := promptsign.Verify(doc, nil, promptsign.Optional, nil, nil, now); err != nil || v.Status != promptsign.Unsigned {
		t.Errorf("optional with no signature = %+v, %v", v, err)
	}
	if v, err := promptsign.Verify(doc, &promptsign.Signature{Key: "x", Value: "y"}, promptsign.Off, nil, nil, now); err != nil || v.Status != promptsign.Skipped {
		t.Errorf("off = %+v, %v", v, err)
	}
	for _, p := range []string{"", "off", "optional", "required"} {
		if _, err := promptsign.ParsePolicy(p); err != nil {
			t.Errorf("ParsePolicy(%q) = %v", p, err)
		}
	}
	if _, err := promptsign.ParsePolicy("strict"); err == nil {
		t.Error("ParsePolicy accepted strict")
	}
}

// TestParse_RefusesADocumentNotSigned whole: duplicate keys (the classic
// way to make a signature cover one text and a reader see another), an
// unknown field, and missing parts.
func TestParse_RefusesADocumentNotSignedWhole(t *testing.T) {
	for in, want := range map[string]string{
		`{"kind":"prompt","name":"a","version":1,"use":"u","text":"safe","text":"unsafe"}`: `the key "text" appears twice`,
		`{"kind":"prompt","name":"a","version":1,"use":"u","text":"t","extra":1}`:          "unknown field",
		`{"kind":"script","name":"a","version":1,"use":"u"}`:                               `kind "script"`,
		`{"kind":"prompt","name":"a","version":0,"use":"u","text":"t"}`:                    "whole number from 1",
		`{"kind":"prompt","name":"a","version":1,"use":"u"}`:                               "a prompt needs text",
		`{"kind":"routes","name":"a","version":1}`:                                         "a name and a use",
	} {
		if _, err := promptsign.Parse([]byte(in)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Parse(%.50s) = %v, want %q", in, err, want)
		}
	}
	// The same document written two ways has one hash.
	a, err := promptsign.Parse([]byte(`{"kind":"routes","name":"r","version":1,"use":"u","body":{"b":1,"a":2}}`))
	if err != nil {
		t.Fatal(err)
	}
	b, err := promptsign.Parse([]byte("{\n \"use\": \"u\", \"body\": {\"a\": 2, \"b\": 1}, \"version\": 1, \"name\": \"r\", \"kind\": \"routes\"\n}"))
	if err != nil || a.Hash != b.Hash {
		t.Errorf("two spellings of one document: %s and %v %v", a.Hash, b, err)
	}
}

// TestParseTrustedKeys reads roles and refuses a key that could sign for
// nothing, one listed twice, and one that is not a key.
func TestParseTrustedKeys(t *testing.T) {
	pub, _ := newKey(t)
	enc := base64.StdEncoding.EncodeToString(pub)
	keys, err := promptsign.ParseTrustedKeys([]byte("keys:\n  - public_key: " + enc + "\n    uses: [triage, summarize]\n    expires: 2027-01-01T00:00:00Z\n    comment: the release manager\n"))
	if err != nil || len(keys) != 1 || keys[0].ID() != promptsign.KeyID(pub) || len(keys[0].Uses) != 2 || keys[0].Expires.Year() != 2027 {
		t.Fatalf("ParseTrustedKeys = %+v, %v", keys, err)
	}
	for in, want := range map[string]string{
		"keys:\n  - public_key: " + enc + "\n": "has no uses",
		"keys:\n  - public_key: " + enc + "\n    uses: [a]\n  - public_key: " + enc + "\n    uses: [b]\n": "listed twice",
		"keys:\n  - public_key: bm90IGEga2V5\n    uses: [a]\n":                                            "not a base64 Ed25519 public key",
		"keys:\n  - public_key: " + enc + "\n    uses: [a]\n    role: admin\n":                            "field role not found",
	} {
		if _, err := promptsign.ParseTrustedKeys([]byte(in)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("ParseTrustedKeys = %v, want %q", err, want)
		}
	}
}

// TestFileVersions_SeparateProcessesLoseNothing: registers on one directory
// (as two runs of one project have) each record their own versions at once,
// and the newest is the highest any recorded, with none lost.
func TestFileVersions_SeparateProcessesLoseNothing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "versions")
	var wg sync.WaitGroup
	for p := 0; p < 8; p++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f := &promptsign.FileVersions{Path: dir}
			for v := int64(1); v <= 40; v++ {
				if err := f.Use("prompt", "triage", v*8+int64(p)); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()
	f := &promptsign.FileVersions{Path: dir}
	if v, ok, err := f.Newest("prompt", "triage"); err != nil || !ok || v != 40*8+7 {
		t.Errorf("Newest = %d, %v, %v; want %d", v, ok, err, 40*8+7)
	}
	if err := f.Use("prompt", "triage", -1); err == nil {
		t.Error("a negative version was recorded")
	}
	if _, ok, err := f.Newest("prompt", "../../etc"); err != nil || ok {
		t.Errorf("an unused hostile name = %v, %v", ok, err)
	}
	if err := f.Use("prompt", "../../escape", 1); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(filepath.Dir(dir)); len(entries) != 1 {
		t.Errorf("a name left the register's directory: %v", entries)
	}
}

// TestVerify_ASignatureOverTheBareCanonicalFormIsRefused: the same key's
// signature over the document's canonical JSON alone, as another use of the
// key would make, is not a prompt signature.
func TestVerify_ASignatureOverTheBareCanonicalFormIsRefused(t *testing.T) {
	pub, priv := newKey(t)
	doc := prompt(t, "1", `"Classify the ticket."`, "")
	bare := promptsign.Signature{Key: promptsign.KeyID(pub), Value: base64.StdEncoding.EncodeToString(ed25519.Sign(priv, doc.Canonical))}
	keys := []promptsign.TrustedKey{{Public: pub, Uses: []string{"triage"}}}
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	versions := &promptsign.FileVersions{Path: filepath.Join(t.TempDir(), "versions")}
	if _, err := promptsign.Verify(doc, &bare, promptsign.Required, keys, versions, now); err == nil || !strings.Contains(err.Error(), "does not verify") {
		t.Errorf("a bare-canonical signature = %v, want it refused", err)
	}
}
