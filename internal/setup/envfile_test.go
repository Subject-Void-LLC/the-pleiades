// Tests for reading and rewriting a compose env file.
package setup_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
	"github.com/Subject-Void-LLC/the-pleiades/internal/setup"
)

// testKey is a valid master key in the form a file carries it.
var testKey = crypto.EncodeKey([]byte(strings.Repeat("t", 32)))

// testJWT is a valid JWT secret.
var testJWT = crypto.EncodeKey([]byte(strings.Repeat("j", 32)))

// TestParseEnvFile_KeepsEveryLineItDoesNotOwnExactly proves an operator's own
// settings, comments, spacing and line endings survive a read and write
// untouched, which is what makes it safe to add keys to a file they already
// keep.
func TestParseEnvFile_KeepsEveryLineItDoesNotOwnExactly(t *testing.T) {
	inputs := []string{
		"",
		"\n",
		"# only a comment",
		"COMPOSE_PROFILES=ops\n  # indented comment\n\nOTHER = spaced value # trailing\n",
		"WINDOWS=line\r\nMASTER_ENCRYPTION_KEY=" + testKey + "\r\nLAST=one",
		"QUOTED=\"a \\\" quote\"\nSINGLE='x'\nMASTER_ENCRYPTION_KEY=\n",
		"no equals sign at all\nexport UNOWNED=fine\n",
	}
	for _, in := range inputs {
		f, err := setup.ParseEnvFile([]byte(in))
		if err != nil {
			t.Fatalf("ParseEnvFile(%q) error = %v", in, err)
		}
		if got := string(f.Bytes()); got != in {
			t.Errorf("ParseEnvFile(%q).Bytes() = %q; an unchanged file must render to exactly the bytes it was read from", in, got)
		}
	}
}

// TestParseEnvFile_ReadsTheOwnedValues pins what Get returns, including that
// an empty value reads as absent, as the controller treats it.
func TestParseEnvFile_ReadsTheOwnedValues(t *testing.T) {
	f, err := setup.ParseEnvFile([]byte(strings.Join([]string{
		"MASTER_ENCRYPTION_KEY=" + testKey,
		"MASTER_ENCRYPTION_KEY_VERSION=v2",
		"MASTER_ENCRYPTION_KEY_PREVIOUS=",
		"JWT_SECRET=" + testJWT,
		"PLEIADES_MAX_OUTAGE=20m",
		"ROTATE_ENCRYPTION_KEYS=false",
	}, "\n")))
	if err != nil {
		t.Fatalf("ParseEnvFile() error = %v", err)
	}
	want := map[string]string{
		setup.VarMasterKey: testKey, setup.VarMasterKeyVersion: "v2",
		setup.VarJWTSecret: testJWT, setup.VarMaxOutage: "20m", setup.VarRotate: "false",
	}
	for name, value := range want {
		if got, ok := f.Get(name); !ok || got != value {
			t.Errorf("Get(%s) = %q, %v; want %q", name, got, ok, value)
		}
	}
	if got, ok := f.Get(setup.VarPreviousKey); ok {
		t.Errorf("Get(%s) = %q, true; an empty value must read as absent", setup.VarPreviousKey, got)
	}
}

// TestParseEnvFile_RefusesEveryFormComposeMightReadDifferently is the list of
// shapes the parser refuses on an owned line, each because docker compose
// has its own rule for it that this parser would otherwise have to copy
// exactly. None of the refusals may contain the value.
func TestParseEnvFile_RefusesEveryFormComposeMightReadDifferently(t *testing.T) {
	secret := "zZ9" + testKey[3:]
	cases := map[string]string{
		"double quoted":            `MASTER_ENCRYPTION_KEY="` + secret + `"`,
		"single quoted":            `MASTER_ENCRYPTION_KEY='` + secret + `'`,
		"a dollar sign":            "JWT_SECRET=" + testJWT + "$HOME",
		"an inline comment":        "MASTER_ENCRYPTION_KEY=" + secret + " # mine",
		"a backslash":              "JWT_SECRET=" + testJWT + "\\n",
		"an export prefix":         "export MASTER_ENCRYPTION_KEY=" + secret,
		"a leading space":          " MASTER_ENCRYPTION_KEY=" + secret,
		"a space before equals":    "MASTER_ENCRYPTION_KEY =" + secret,
		"the colon spelling":       "MASTER_ENCRYPTION_KEY: " + secret,
		"a duplicate":              "MASTER_ENCRYPTION_KEY=" + testKey + "\nMASTER_ENCRYPTION_KEY=" + secret,
		"a short key":              "MASTER_ENCRYPTION_KEY=" + crypto.EncodeKey([]byte(strings.Repeat("s", 16))),
		"a key that is not base64": "MASTER_ENCRYPTION_KEY=" + strings.Repeat("!", 44),
		"a short JWT secret":       "JWT_SECRET=only-twenty-characters",
		"a version with a dollar":  "MASTER_ENCRYPTION_KEY_VERSION=v$1",
		"rotate set to yes":        "ROTATE_ENCRYPTION_KEYS=yes",
		"an outage budget of zero": "PLEIADES_MAX_OUTAGE=0s",
		"a multi-line value above": "NOTES=\"first line\nMASTER_ENCRYPTION_KEY=" + secret + "\nlast line\"",
		"invalid UTF-8":            "MASTER_ENCRYPTION_KEY=" + testKey + "\n\xff\xfe",
		"a NUL byte":               "OTHER=a\x00b",
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := setup.ParseEnvFile([]byte(in))
			if err == nil {
				t.Fatalf("ParseEnvFile accepted %s", name)
			}
			if !errors.Is(err, setup.ErrEnvFile) {
				t.Errorf("error %v does not wrap ErrEnvFile", err)
			}
			for _, v := range []string{secret, testKey, testJWT} {
				if strings.Contains(err.Error(), v) {
					t.Fatalf("the refusal %q contains a value from the file", err)
				}
			}
		})
	}
}

// TestEnvFile_SetReplacesInPlaceAndAppendsOtherwise proves Set rewrites an
// owned line where it stands, fills an empty one, adds a missing one at the
// end, and leaves everything else alone.
func TestEnvFile_SetReplacesInPlaceAndAppendsOtherwise(t *testing.T) {
	f, err := setup.ParseEnvFile([]byte("# mine\nMASTER_ENCRYPTION_KEY=\nKEEP=me"))
	if err != nil {
		t.Fatalf("ParseEnvFile() error = %v", err)
	}
	f.Set(setup.VarMasterKey, testKey)
	f.Set(setup.VarJWTSecret, testJWT)
	f.AddComment("a note")

	want := "# mine\nMASTER_ENCRYPTION_KEY=" + testKey + "\nKEEP=me\nJWT_SECRET=" + testJWT + "\n# a note\n"
	if got := string(f.Bytes()); got != want {
		t.Fatalf("Bytes() = %q, want %q", got, want)
	}
	again, err := setup.ParseEnvFile(f.Bytes())
	if err != nil {
		t.Fatalf("re-reading what Set wrote: %v", err)
	}
	if got, _ := again.Get(setup.VarJWTSecret); got != testJWT {
		t.Fatalf("re-read JWT_SECRET = %q, want %q", got, testJWT)
	}
}
