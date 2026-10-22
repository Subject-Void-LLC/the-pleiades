// Tests for sealing to a key and to a passphrase: each opens only with what
// it was sealed to, and a changed file refuses to open rather than opening
// as something else.
package sealed_test

import (
	"bytes"
	"encoding/pem"
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/sealed"
)

const secret = "hunter2-but-longer"

func newKey(t *testing.T) *sealed.PrivateKey {
	t.Helper()
	k, err := sealed.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// rewrite decodes a sealed file, lets change edit its block, and encodes it
// again, the way someone tampering with the file would.
func rewrite(t *testing.T, data []byte, change func(*pem.Block)) []byte {
	t.Helper()
	block, _ := pem.Decode(data)
	if block == nil {
		t.Fatal("not a PEM block")
	}
	change(block)
	return pem.EncodeToMemory(block)
}

func parse(t *testing.T, data []byte) *sealed.File {
	t.Helper()
	f, err := sealed.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestSealToKey_OpensOnlyWithItsKey(t *testing.T) {
	k := newKey(t)
	data, err := sealed.SealToKey(k.Public(), sealed.PurposePassword, []byte(secret))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(secret)) {
		t.Fatal("the sealed file holds the secret readably")
	}
	f := parse(t, data)
	if f.Method != sealed.MethodKey || f.Purpose != sealed.PurposePassword || f.Recipient != k.Public().Fingerprint() {
		t.Errorf("parsed %+v", f)
	}
	got, err := f.OpenWithKey(k)
	if err != nil || string(got) != secret {
		t.Fatalf("OpenWithKey = %q, %v", got, err)
	}
	if _, err := f.OpenWithKey(newKey(t)); !errors.Is(err, sealed.ErrWrongKey) {
		t.Errorf("another key: err = %v, want ErrWrongKey", err)
	}
	if _, err := f.OpenWithPassphrase("a long enough passphrase"); err == nil {
		t.Error("a key-sealed file opened with a passphrase")
	}

	// A file sealed to one key and relabelled with another's fingerprint
	// gets past the fingerprint check and still does not open.
	other := newKey(t)
	relabelled := rewrite(t, data, func(b *pem.Block) { b.Headers["Recipient"] = other.Public().Fingerprint() })
	if _, err := parse(t, relabelled).OpenWithKey(other); !errors.Is(err, sealed.ErrCannotOpen) {
		t.Errorf("relabelled: err = %v, want ErrCannotOpen", err)
	}
	flipped := rewrite(t, data, func(b *pem.Block) { b.Bytes[len(b.Bytes)-1] ^= 1 })
	if _, err := parse(t, flipped).OpenWithKey(k); !errors.Is(err, sealed.ErrCannotOpen) {
		t.Errorf("changed body: err = %v, want ErrCannotOpen", err)
	}
}

func TestSealWithPassphrase_OpensOnlyWithItsPassphrase(t *testing.T) {
	const passphrase = "correct horse battery staple"
	data, err := sealed.SealWithPassphrase(passphrase, sealed.PurposePassword, []byte(secret))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(secret)) || bytes.Contains(data, []byte(passphrase)) {
		t.Fatal("the sealed file holds the secret or the passphrase readably")
	}
	f := parse(t, data)
	if f.Method != sealed.MethodPassphrase || f.Recipient != "" {
		t.Errorf("parsed %+v", f)
	}
	got, err := f.OpenWithPassphrase(passphrase)
	if err != nil || string(got) != secret {
		t.Fatalf("OpenWithPassphrase = %q, %v", got, err)
	}
	if _, err := f.OpenWithPassphrase(passphrase + "!"); !errors.Is(err, sealed.ErrCannotOpen) {
		t.Errorf("wrong passphrase: err = %v, want ErrCannotOpen", err)
	}
	if _, err := f.OpenWithKey(newKey(t)); err == nil {
		t.Error("a passphrase-sealed file opened with a key")
	}

	// A cheaper derivation, still inside the bounds, is bound into the
	// encryption: substituting one does not open the file.
	cheaper := rewrite(t, data, func(b *pem.Block) { b.Headers["Argon2id"] = "t=1,m=65536,p=4" })
	if _, err := parse(t, cheaper).OpenWithPassphrase(passphrase); !errors.Is(err, sealed.ErrCannotOpen) {
		t.Errorf("changed parameters: err = %v, want ErrCannotOpen", err)
	}

	if _, err := sealed.SealWithPassphrase("too short", sealed.PurposePassword, []byte(secret)); err == nil ||
		!strings.Contains(err.Error(), "at least") {
		t.Errorf("short passphrase: err = %v", err)
	}
}

func TestParse_RefusesWhatThisVersionDoesNotWrite(t *testing.T) {
	k := newKey(t)
	byKey, err := sealed.SealToKey(k.Public(), sealed.PurposePassword, []byte(secret))
	if err != nil {
		t.Fatal(err)
	}
	byPassphrase, err := sealed.SealWithPassphrase("a long enough passphrase", sealed.PurposePassword, []byte(secret))
	if err != nil {
		t.Fatal(err)
	}
	header := func(data []byte, name, value string) []byte {
		return rewrite(t, data, func(b *pem.Block) {
			if value == "" {
				delete(b.Headers, name)
			} else {
				b.Headers[name] = value
			}
		})
	}
	for name, c := range map[string]struct {
		data []byte
		want string
	}{
		"plaintext":                    {[]byte(secret + "\n"), "not a sealed file"},
		"another PEM type":             {pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte{1}}), "not a sealed file"},
		"larger than any":              {bytes.Repeat([]byte("a"), sealed.MaxFileSize+1), "not a sealed file"},
		"data after the block":         {append(append([]byte{}, byKey...), "extra"...), "after the sealed block"},
		"data before the block":        {append([]byte("note: open me\n"), byKey...), "before the sealed block"},
		"a broken block before it":     {append([]byte("-----BEGIN JUNK\n"), byKey...), "before the sealed block"},
		"another version":              {header(byKey, "Version", "2"), "version"},
		"another purpose":              {header(byKey, "Purpose", "token"), "purpose"},
		"another method":               {header(byKey, "Method", "rot13"), "method"},
		"an unknown header":            {header(byKey, "Comment", "hello"), "header"},
		"no recipient":                 {header(byKey, "Recipient", ""), "recipient"},
		"a header of the other method": {header(byKey, "Salt", "AAAAAAAAAAAAAAAAAAAAAA"), "header"},
		"no salt":                      {header(byPassphrase, "Salt", ""), "Salt"},
		"a short salt":                 {header(byPassphrase, "Salt", "AAAA"), "Salt"},
		"malformed parameters":         {header(byPassphrase, "Argon2id", "t=3"), "t=,m=,p="},
		// A file states its own cost, so one asking for more memory than a
		// derivation may use is refused before anything is derived.
		"too much memory":  {header(byPassphrase, "Argon2id", "t=3,m=4194304,p=4"), "outside"},
		"too many passes":  {header(byPassphrase, "Argon2id", "t=1000,m=65536,p=4"), "outside"},
		"too many threads": {header(byPassphrase, "Argon2id", "t=3,m=65536,p=64"), "outside"},
	} {
		if _, err := sealed.Parse(c.data); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want one naming %q", name, err, c.want)
		}
	}
	if _, err := sealed.Parse([]byte(secret)); !errors.Is(err, sealed.ErrNotSealed) {
		t.Errorf("plaintext: err = %v, want ErrNotSealed", err)
	}
}

func TestSeal_RefusesASecretNoCallerMeans(t *testing.T) {
	pub := newKey(t).Public()
	for name, c := range map[string]struct {
		purpose string
		secret  []byte
		want    string
	}{
		"empty":           {sealed.PurposePassword, nil, "empty"},
		"too large":       {sealed.PurposePassword, bytes.Repeat([]byte("a"), sealed.MaxSecretSize+1), "larger"},
		"unknown purpose": {"token", []byte(secret), "purpose"},
	} {
		if _, err := sealed.SealToKey(pub, c.purpose, c.secret); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s by key: err = %v, want one naming %q", name, err, c.want)
		}
		if _, err := sealed.SealWithPassphrase("a long enough passphrase", c.purpose, c.secret); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s by passphrase: err = %v, want one naming %q", name, err, c.want)
		}
	}
}

func TestPublicKey_RoundTripsThroughItsText(t *testing.T) {
	k := newKey(t)
	text := k.Public().String()
	if !strings.HasPrefix(text, "pleiades-seal1:") {
		t.Fatalf("text form %q", text[:20])
	}
	// Wrapped when pasted, it still reads.
	wrapped := text[:40] + "\n  " + text[40:]
	pub, err := sealed.ParsePublicKey(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	if pub.Fingerprint() != k.Public().Fingerprint() || !strings.HasPrefix(pub.Fingerprint(), "SHA256:") {
		t.Errorf("fingerprint %s, want %s", pub.Fingerprint(), k.Public().Fingerprint())
	}

	raw, err := k.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	again, err := sealed.ParsePrivateKey(raw)
	if err != nil || again.Public().Fingerprint() != k.Public().Fingerprint() {
		t.Errorf("private key round trip: %v", err)
	}
	if _, err := sealed.ParsePrivateKey([]byte("short")); err == nil {
		t.Error("ParsePrivateKey accepted garbage")
	}

	for name, s := range map[string]string{
		"another prefix": "age1qyqszqgpqyqszqgpqyqszqgpqyqszqgp",
		"not base64":     "pleiades-seal1:!!!",
		"the wrong size": "pleiades-seal1:AAAA",
	} {
		if _, err := sealed.ParsePublicKey(s); err == nil || !strings.Contains(err.Error(), "not a pleiades seal key") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}
