// Package factstest: the at-rest keys tests seal stored facts under, and
// helpers that read and rewrite the Crawl CLI's sealed files as the plaintext
// lines they hold, so a test can edit one the way an attacker holding the key
// could, or look for plaintext the way one without it would.
package factstest

import (
	"bytes"
	"os"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
)

// Secret is the fixed 32-byte secret Keys derives from.
var Secret = []byte("facts-at-rest-test-secret-32byte")

// Keys is a store's at-rest keys under Secret, the same on every call.
func Keys() *facts.Keys {
	keys, err := facts.NewKeys(Secret, "test")
	if err != nil {
		// A fixed 32-byte secret always derives.
		panic(err)
	}
	return keys
}

// OpenLines reads the sealed append-only file at path as the plaintext lines
// it holds for device in the given role under keys, each without its newline.
func OpenLines(t testing.TB, keys *facts.Keys, path, device, role string) [][]byte {
	t.Helper()
	raw, err := os.ReadFile(path) // #nosec G304 -- a test's own temporary file
	if err != nil {
		t.Fatal(err)
	}
	var out [][]byte
	offset := int64(0)
	for _, sealed := range bytes.SplitAfter(raw, []byte("\n")) {
		if len(sealed) == 0 {
			continue
		}
		plain, err := keys.OpenLine(device, role, offset, bytes.TrimSuffix(sealed, []byte("\n")))
		if err != nil {
			t.Fatalf("%s: the line at byte %d does not open: %v", path, offset, err)
		}
		out = append(out, plain)
		offset += int64(len(sealed))
	}
	return out
}

// SealLines is the sealed content of an append-only file holding lines for
// device in the given role, as a store holding keys would have written them.
func SealLines(t testing.TB, keys *facts.Keys, device, role string, lines [][]byte) []byte {
	t.Helper()
	var out []byte
	for _, plain := range lines {
		sealed, err := keys.SealLine(device, role, int64(len(out)), plain)
		if err != nil {
			t.Fatal(err)
		}
		out = append(append(out, sealed...), '\n')
	}
	return out
}

// WriteLines rewrites the sealed append-only file at path to hold lines.
func WriteLines(t testing.TB, keys *facts.Keys, path, device, role string, lines [][]byte) {
	t.Helper()
	if err := os.WriteFile(path, SealLines(t, keys, device, role, lines), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Envelope is the envelope service a test's Controller store seals each
// fact's key and value under: one fixed master key, the same on every call.
func Envelope() *crypto.EnvelopeService {
	svc, err := crypto.NewEnvelopeService([]byte("facts-at-rest-test-master-32byte"), "v1", nil, "")
	if err != nil {
		// A fixed 32-byte key always makes a service.
		panic(err)
	}
	return svc
}
