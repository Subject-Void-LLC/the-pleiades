// Package shacrypt_test checks the SHA-512 crypt hashes this package
// produces against known answers from other implementations.
//
// Every vector below was generated once, outside this test, and pasted
// in: most with "openssl passwd -6 -salt <salt> <password>" (OpenSSL
// 3.0.2), and the empty password through the system crypt(3) (libxcrypt,
// called from perl and python3), because that OpenSSL release refuses an
// empty password. The tests never run openssl themselves, so they need
// nothing installed and cannot drift with its version.
package shacrypt_test

import (
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/shacrypt"
)

// block64 is a 64-byte password, the SHA-512 block size, so the long
// password vectors cross the "whole blocks, then the rest" boundary the
// specification's steps 9, 10 and 16 handle.
const block64 = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ-_"

// utf8Password is "pässwörd 日本語 🔑" spelled as bytes, so the test pins
// the exact 25 bytes openssl hashed rather than whatever normalization an
// editor might apply to the characters.
const utf8Password = "p\xc3\xa4ssw\xc3\xb6rd \xe6\x97\xa5\xe6\x9c\xac\xe8\xaa\x9e \xf0\x9f\x94\x91"

// hashShape is the form every default-rounds hash takes: the "$6$"
// identifier, a salt of 1 to 16 crypt-alphabet characters, and 86
// characters of encoded digest.
var hashShape = regexp.MustCompile(`^\$6\$[./0-9A-Za-z]{1,16}\$[./0-9A-Za-z]{86}$`)

// knownAnswers are the vectors HashWithSalt must reproduce exactly. The
// source field says where each expected value came from.
var knownAnswers = []struct {
	name     string
	password string
	salt     string
	want     string
	source   string
}{
	{
		name:     "specification example",
		password: "Hello world!",
		salt:     "saltstring",
		want:     "$6$saltstring$svn8UoSVapNtMuq1ukKS4tPQd8iKwSMHWjl/O817G3uBnIFNjnQJuesI68u4OTLiBFdcbYEdFCoEOfaS35inz1",
		source:   "the specification's own test vector; openssl gives the same",
	},
	{
		name:     "salt longer than 16 is truncated",
		password: "This is just a test",
		salt:     "toolongsaltstring",
		want:     "$6$toolongsaltstrin$lQ8jolhgVRVhY4b5pZKaysCLi0QBxGoNeKQzQ3glMhwllF7oGDZxUhx1yxdYcz/e1JSbq3y6JMxxl8audkUEm0",
		source:   "openssl; equal to the specification's rounds=5000 vector for the same input",
	},
	{
		name:     "empty password",
		password: "",
		salt:     "EmptyPassword./9",
		want:     "$6$EmptyPassword./9$qq8mWcxJ0a5xB2DMxn6VVxqc6RQBSL2haC4.2mrlZal3LqyH316fYvQbcsZ6ykammVL8jFUuU1Jc/UN6W0sIV.",
		source:   "libxcrypt crypt(3) through perl and python3; openssl 3.0.2 refuses an empty password",
	},
	{
		name:     "password of exactly 64 bytes",
		password: block64,
		salt:     "Exactly64Bytes..",
		want:     "$6$Exactly64Bytes..$MCFi3OJo9812sw54q6EgD.gNhCIpPz11erAlfpaVdRpFKymxT30q6H0pxWaOxG3uLbpie.Yhaxb4BO2pKENUj1",
		source:   "openssl",
	},
	{
		name:     "password of 130 bytes",
		password: block64 + block64 + "01",
		salt:     "LongPassword/130",
		want:     "$6$LongPassword/130$hnp2E8nc82xRZUvnc4u1/fkC5nH3pwmMfHDYAFeZxRngiWmejGup0soY4dC8U3RJwgydRqwirbJ6CyJvHAi2k0",
		source:   "openssl",
	},
	{
		name:     "multi-byte UTF-8 password",
		password: utf8Password,
		salt:     "UTF8utf8UTF8utf8",
		want:     "$6$UTF8utf8UTF8utf8$GZ1mhLFhhBddKE/Fd6xVaLuImbxNwemg4q6amot5uglsOiXd3HTK7rXd1nQXVnsdJ40jS3pKw3aRa7r5a8Ppc1",
		source:   "openssl",
	},
	{
		name:     "one-character salt",
		password: "correct horse",
		salt:     "a",
		want:     "$6$a$K7/Hb91R/qRj.Cjr1Ny4yk.8fDqYs1MvXOFyOXimHJcZBXqVi2lnHD1xBvaqXzK3HBpzNTKIte9995.Ray7nn/",
		source:   "openssl; libxcrypt crypt(3) gives the same",
	},
	{
		name:     "two-character salt",
		password: "battery staple",
		salt:     "ab",
		want:     "$6$ab$1A88wf2kEZIPLGMvwmVvhm2AmPfuvrWUCxepcqRYMryZeU81QuO/Ifk1NcMTJJ.3S8TwHzRI3sIo3FeJ.yEh2.",
		source:   "openssl",
	},
	{
		name:     "random-looking 24-character password, 16-character salt",
		password: "k7#Qz!9vR2@mW4$pL8^tN6&x",
		salt:     "Xq3/Lm.9Tz0bRk7W",
		want:     "$6$Xq3/Lm.9Tz0bRk7W$WiI5.B.6GKES2nyNlOg5w6Zk/N4eO34D0C4NFahKC5G4g7p21kT.w9k90PRzwFqPog.8TS.PlGRgre4UC7/L81",
		source:   "openssl; libxcrypt crypt(3) gives the same",
	},
}

// TestKnownAnswerInputsAreWhatTheyClaim guards the vectors themselves:
// a password built by concatenation or spelled in bytes is checked to be
// the length and kind its name says, so a typo cannot quietly turn the
// long or multi-byte case into a different one.
func TestKnownAnswerInputsAreWhatTheyClaim(t *testing.T) {
	if len(block64) != 64 {
		t.Fatalf("block64 is %d bytes, want 64", len(block64))
	}
	if n := len(block64 + block64 + "01"); n != 130 {
		t.Fatalf("long password is %d bytes, want 130", n)
	}
	if !utf8.ValidString(utf8Password) || len(utf8Password) != 25 || utf8.RuneCountInString(utf8Password) != 14 {
		t.Fatalf("utf8Password is not the 25-byte, 14-rune string openssl hashed")
	}
}

// TestHashWithSalt_KnownAnswers proves HashWithSalt matches the other
// implementations byte for byte, which is what cloud-init and a Linux
// login will compare against.
func TestHashWithSalt_KnownAnswers(t *testing.T) {
	for _, tc := range knownAnswers {
		t.Run(tc.name, func(t *testing.T) {
			got, err := shacrypt.HashWithSalt(tc.password, tc.salt)
			if err != nil {
				t.Fatalf("HashWithSalt: %v", err)
			}
			if got != tc.want {
				t.Fatalf("HashWithSalt(%q, %q)\n got  %s\n want %s (from %s)", tc.password, tc.salt, got, tc.want, tc.source)
			}
		})
	}
}

// TestHashWithSalt_RefusesBadSalt proves each documented refusal returns
// an error and no hash, including a bad character that truncation would
// have dropped, and that every message carries the package prefix.
func TestHashWithSalt_RefusesBadSalt(t *testing.T) {
	for _, salt := range []string{
		"",                  // empty
		"salt$string",       // the field separator
		"rounds=5000",       // a rounds field is not supported
		"with space",        // outside the alphabet
		"s\xc3\xa4lt",       // non-ASCII
		"nul\x00byte",       // a NUL byte
		"0123456789abcdef!", // bad only past the 16th character
	} {
		got, err := shacrypt.HashWithSalt("password", salt)
		if err == nil {
			t.Errorf("HashWithSalt(_, %q) = %q, want an error", salt, got)
			continue
		}
		if got != "" {
			t.Errorf("HashWithSalt(_, %q) returned %q alongside its error, want nothing", salt, got)
		}
		if !strings.HasPrefix(err.Error(), "shacrypt: ") {
			t.Errorf("error %q does not start with the package prefix", err)
		}
	}
}

// TestHash_Shape proves Hash's output is "$6$", a 16-character salt from
// the crypt alphabet, and an 86-character digest, with no rounds field.
func TestHash_Shape(t *testing.T) {
	got, err := shacrypt.Hash("a password")
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	if !regexp.MustCompile(`^\$6\$[./0-9A-Za-z]{16}\$[./0-9A-Za-z]{86}$`).MatchString(got) {
		t.Fatalf("Hash = %q, want $6$ + 16-character salt + $ + 86-character digest", got)
	}
}

// TestHash_FreshSaltEachCall proves two hashes of the same password
// differ, which is the point of drawing a new salt every time.
func TestHash_FreshSaltEachCall(t *testing.T) {
	first, err := shacrypt.Hash("same password")
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	second, err := shacrypt.Hash("same password")
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	if first == second || saltOf(first) == saltOf(second) {
		t.Fatalf("two Hash calls gave %q and %q, want different salts", first, second)
	}
}

// TestHash_ReproducibleFromItsOwnSalt proves Hash is HashWithSalt with a
// random salt: rehashing with the salt Hash chose gives the same string,
// and Verify accepts the password against it.
func TestHash_ReproducibleFromItsOwnSalt(t *testing.T) {
	const password = "k7#Qz!9vR2@mW4$pL8^tN6&x"
	got, err := shacrypt.Hash(password)
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	again, err := shacrypt.HashWithSalt(password, saltOf(got))
	if err != nil {
		t.Fatalf("HashWithSalt: %v", err)
	}
	if again != got {
		t.Fatalf("HashWithSalt with Hash's salt = %q, want %q", again, got)
	}
	if !shacrypt.Verify(password, got) {
		t.Fatalf("Verify refused the password Hash was given")
	}
}

// TestVerify proves Verify accepts every known answer and refuses a wrong
// password, a hash of another scheme, a rounds field, and a malformed or
// altered hash.
func TestVerify(t *testing.T) {
	for _, tc := range knownAnswers {
		if !shacrypt.Verify(tc.password, tc.want) {
			t.Errorf("Verify(%q, %q) = false, want true", tc.password, tc.want)
		}
		if shacrypt.Verify(tc.password+"x", tc.want) {
			t.Errorf("Verify accepted a wrong password against %q", tc.want)
		}
	}

	spec := knownAnswers[0].want
	for _, tc := range []struct {
		name string
		hash string
	}{
		{"empty", ""},
		{"SHA-256 crypt", "$5$saltstring$5B8vYYiY.CVt1RlTTf8KbXBH3hsxY/GNooZaBBGWEc5"},
		{"no digest separator", "$6$saltstring"},
		{"rounds field", "$6$rounds=5000$toolongsaltstrin$lQ8jolhgVRVhY4b5pZKaysCLi0QBxGoNeKQzQ3glMhwllF7oGDZxUhx1yxdYcz/e1JSbq3y6JMxxl8audkUEm0"},
		{"empty salt", "$6$$svn8UoSVapNtMuq1ukKS4tPQd8iKwSMHWjl/O817G3uBnIFNjnQJuesI68u4OTLiBFdcbYEdFCoEOfaS35inz1"},
		{"truncated digest", spec[:len(spec)-1]},
		{"extra character", spec + "."},
		{"altered digest", spec[:len(spec)-1] + "2"},
	} {
		password := "Hello world!"
		if tc.name == "rounds field" {
			password = "This is just a test"
		}
		if shacrypt.Verify(password, tc.hash) {
			t.Errorf("%s: Verify(%q, %q) = true, want false", tc.name, password, tc.hash)
		}
	}
}

// FuzzHashWithSalt checks, for any password and salt, that HashWithSalt
// either refuses a salt the documented rule refuses or returns a hash of
// the documented shape carrying the truncated salt, that it gives the same
// answer twice, and that Verify accepts it. Each input is also forced into
// a valid salt so the hashing path is fuzzed even when the raw salt is not
// valid.
func FuzzHashWithSalt(f *testing.F) {
	for _, tc := range knownAnswers {
		f.Add(tc.password, tc.salt)
	}
	f.Add("password", "bad$salt")
	f.Add("", "")
	f.Fuzz(func(t *testing.T, password, salt string) {
		got, err := shacrypt.HashWithSalt(password, salt)
		if validSalt(salt) != (err == nil) {
			t.Fatalf("HashWithSalt(_, %q) error = %v, but the salt's validity is %t", salt, err, validSalt(salt))
		}
		if err == nil {
			checkHash(t, password, salt, got)
		}

		// Map every byte of the raw salt into the alphabet, as Hash does
		// with random bytes, so the hashing path runs on this input too.
		const alphabet = "./0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
		forced := []byte("." + salt)
		for i, b := range forced {
			forced[i] = alphabet[b&0x3f]
		}
		got, err = shacrypt.HashWithSalt(password, string(forced))
		if err != nil {
			t.Fatalf("HashWithSalt refused the alphabet-only salt %q: %v", forced, err)
		}
		checkHash(t, password, string(forced), got)
	})
}

// checkHash asserts that got, the hash of password under salt, has the
// documented shape, carries salt cut to 16 characters, is the same when
// computed again, and verifies.
func checkHash(t *testing.T, password, salt, got string) {
	t.Helper()
	if !hashShape.MatchString(got) {
		t.Fatalf("hash %q does not have the documented shape", got)
	}
	if want := salt[:min(len(salt), 16)]; saltOf(got) != want {
		t.Fatalf("hash %q carries salt %q, want %q", got, saltOf(got), want)
	}
	if again, _ := shacrypt.HashWithSalt(password, salt); again != got {
		t.Fatalf("hashing again gave %q, want %q", again, got)
	}
	if !shacrypt.Verify(password, got) {
		t.Fatalf("Verify refused the hash HashWithSalt just made: %q", got)
	}
}

// validSalt is the test's own statement of the salt rule HashWithSalt
// documents: not empty, and every byte in ./0-9A-Za-z.
func validSalt(salt string) bool {
	if salt == "" {
		return false
	}
	for i := 0; i < len(salt); i++ {
		c := salt[i]
		ok := c == '.' || c == '/' || ('0' <= c && c <= '9') || ('A' <= c && c <= 'Z') || ('a' <= c && c <= 'z')
		if !ok {
			return false
		}
	}
	return true
}

// saltOf returns the salt field of a "$6$<salt>$<hash>" string.
func saltOf(hash string) string {
	salt, _, _ := strings.Cut(strings.TrimPrefix(hash, "$6$"), "$")
	return salt
}

// BenchmarkHashWithSalt measures one default-rounds hash, which is the
// cost a caller pays per account it seeds.
func BenchmarkHashWithSalt(b *testing.B) {
	for b.Loop() {
		if _, err := shacrypt.HashWithSalt("k7#Qz!9vR2@mW4$pL8^tN6&x", "Xq3/Lm.9Tz0bRk7W"); err != nil {
			b.Fatal(err)
		}
	}
}
