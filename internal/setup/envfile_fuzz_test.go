// The fuzz gate for the one file the setup command reads back.
package setup_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
	"github.com/Subject-Void-LLC/the-pleiades/internal/setup"
)

// FuzzParseEnvFile is the phase's fuzz gate for the one file this command
// reads back. A half-written or hand-edited env file is the realistic
// input, so the property is not merely "does not panic" but three things:
//
//  1. Nothing malformed is accepted as a valid secret. Any key the parser
//     returns decodes to exactly 32 bytes through the controller's own rule,
//     and any JWT secret is at least 32 bytes.
//  2. A file read and not changed renders to exactly the bytes it came from,
//     so rewriting it never alters a line this command does not own.
//  3. A refusal never depends on a secret's value. The input is parsed a
//     second time with every letter and digit in each secret assignment
//     rotated to another of its own kind, which leaves the value exactly as
//     valid or invalid as it was, and the two refusals must be identical.
//     A refusal that quoted the value would differ. This replaced a check
//     for the value as a substring of the error, which the fuzzer showed was
//     unsound: it produced the value "ASTER_ENCRYPTION", which is part of
//     the variable name every refusal rightly names.
func FuzzParseEnvFile(f *testing.F) {
	key := crypto.EncodeKey([]byte(strings.Repeat("f", 32)))
	seeds := []string{
		"",
		"MASTER_ENCRYPTION_KEY=" + key + "\n",
		"MASTER_ENCRYPTION_KEY=" + key[:30] + "\n",
		"MASTER_ENCRYPTION_KEY=" + key + "\nMASTER_ENCRYPTION_KEY=" + key + "\n",
		"JWT_SECRET=" + key + "\nPLEIADES_MAX_OUTAGE=30m\n",
		"export MASTER_ENCRYPTION_KEY=" + key,
		"MASTER_ENCRYPTION_KEY=\"" + key + "\"",
		"A=\"open\nMASTER_ENCRYPTION_KEY=" + key + "\n\"",
		"# c\r\nB = 'x'\r\nMASTER_ENCRYPTION_KEY_PREVIOUS=" + key + "\r\n",
		"MASTER_ENCRYPTION_KEY: " + key,
		"\xff",
		"MASTER_ENCRYPTION_KEY_VERSION=v1\nROTATE_ENCRYPTION_KEYS=true",
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		file, err := setup.ParseEnvFile(data)
		_, rotatedErr := setup.ParseEnvFile([]byte(rotateSecrets(string(data))))
		if (err == nil) != (rotatedErr == nil) || (err != nil && err.Error() != rotatedErr.Error()) {
			t.Fatalf("the refusal depends on a secret's value:\n  original: %v\n  rotated:  %v", err, rotatedErr)
		}
		if err != nil {
			return
		}

		if got := string(file.Bytes()); got != string(data) {
			t.Fatalf("an unchanged file rendered as %q, read from %q", got, data)
		}
		for _, name := range []string{setup.VarMasterKey, setup.VarPreviousKey} {
			if v, ok := file.Get(name); ok {
				k, err := crypto.DecodeKey(v, name)
				if err != nil || len(k) != 32 {
					t.Fatalf("%s was accepted but does not decode to a 32-byte key", name)
				}
			}
		}
		if v, ok := file.Get(setup.VarJWTSecret); ok && len(v) < 32 {
			t.Fatalf("JWT_SECRET was accepted at %d bytes", len(v))
		}
	})
}

// rotateSecrets rotates every ASCII letter by 13 and every digit by 5 in
// the value of each assignment to a secret variable, in any spelling. The
// line's structure (the name, the separator, quotes, spaces, signs) is
// untouched, so the rotated file is exactly as valid as the original: a
// base64 value stays base64 of the same length, and a JWT secret keeps its
// length and its alphabet.
func rotateSecrets(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		for _, name := range []string{setup.VarMasterKey, setup.VarPreviousKey, setup.VarJWTSecret} {
			at := strings.Index(line, name)
			if at < 0 {
				continue
			}
			rest := line[at+len(name):]
			sep := strings.IndexAny(rest, "=:")
			if sep < 0 {
				continue
			}
			valueStart := at + len(name) + sep + 1
			lines[i] = line[:valueStart] + rotateBytes(line[valueStart:])
			break
		}
	}
	return strings.Join(lines, "\n")
}

// rotateBytes rotates each ASCII letter within its case by 13 and each digit
// by 5, and leaves every other byte exactly as it is. It works on bytes
// rather than runes on purpose: strings.Map would replace an invalid UTF-8
// sequence with a valid replacement character, which the fuzzer found by
// making an invalid file valid.
func rotateBytes(s string) string {
	b := []byte(s)
	for i, c := range b {
		switch {
		case c >= 'a' && c <= 'z':
			b[i] = 'a' + (c-'a'+13)%26
		case c >= 'A' && c <= 'Z':
			b[i] = 'A' + (c-'A'+13)%26
		case c >= '0' && c <= '9':
			b[i] = '0' + (c-'0'+5)%10
		}
	}
	return string(b)
}
