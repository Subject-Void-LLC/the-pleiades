// Tests for the KDF, its encoding, and the decoy derivation.
//
// These run against the real argon2 implementation rather than a double,
// per RULE 0: the entire claim of this file is that a specific derivation
// produces a specific string and that a hostile string cannot reach that
// derivation, and a fake KDF would prove neither. Where a test needs many
// derivations it uses cheapParams, which changes the cost and not the code
// path.
package localauth_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/localauth"
)

// cheapParams is DefaultParams at a cost a test can afford in a loop. It is
// deliberately still a valid, in-range parameter set, so it exercises the
// same validation branches production input does.
var cheapParams = localauth.Params{
	Memory:  64,
	Time:    1,
	Threads: 1,
	SaltLen: 16,
	KeyLen:  32,
}

func TestHash_ThenVerify_RoundTrips(t *testing.T) {
	const password = "correct horse battery staple"

	encoded, err := localauth.Hash(password)
	if err != nil {
		t.Fatalf("Hash() error = %v", err)
	}

	ok, err := localauth.Verify(encoded, password)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if !ok {
		t.Error("Verify() = false for the password that produced the hash")
	}
}

func TestHash_NeverContainsThePassword(t *testing.T) {
	// The property that matters most and the cheapest one to regress: a
	// hash that embedded its input would still round-trip through Verify.
	const password = "a-very-distinctive-passphrase-value"

	encoded, err := localauth.Hash(password)
	if err != nil {
		t.Fatalf("Hash() error = %v", err)
	}
	if strings.Contains(encoded, password) {
		t.Errorf("Hash() output contains the plaintext password: %q", encoded)
	}
}

func TestHash_IsSaltedSoTwoHashesOfOnePasswordDiffer(t *testing.T) {
	const password = "the same password twice"

	first, err := localauth.Hash(password)
	if err != nil {
		t.Fatalf("Hash() error = %v", err)
	}
	second, err := localauth.Hash(password)
	if err != nil {
		t.Fatalf("Hash() error = %v", err)
	}

	if first == second {
		t.Error("two hashes of one password are identical, so the salt is not random")
	}
	// Both must still verify. A salt that is random but not stored would
	// pass the check above and fail here.
	for i, encoded := range []string{first, second} {
		ok, err := localauth.Verify(encoded, password)
		if err != nil {
			t.Fatalf("Verify(hash %d) error = %v", i, err)
		}
		if !ok {
			t.Errorf("Verify(hash %d) = false", i)
		}
	}
}

func TestVerify_RejectsTheWrongPassword(t *testing.T) {
	encoded, err := localauth.Hash("the real password")
	if err != nil {
		t.Fatalf("Hash() error = %v", err)
	}

	tests := []struct {
		name     string
		password string
	}{
		{"wrong entirely", "some other password"},
		{"empty", ""},
		{"one character short", "the real passwor"},
		{"one character long", "the real passwordd"},
		{"case changed", "The Real Password"},
		{"leading space", " the real password"},
		{"trailing space", "the real password "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ok, err := localauth.Verify(encoded, tt.password)
			if err != nil {
				t.Fatalf("Verify() error = %v", err)
			}
			if ok {
				t.Errorf("Verify() = true for %q", tt.password)
			}
		})
	}
}

func TestEncode_ProducesThePHCFormat(t *testing.T) {
	salt := []byte("0123456789abcdef")
	key := []byte("0123456789abcdef0123456789abcdef")

	got := localauth.Encode(localauth.DefaultParams, salt, key)

	const wantPrefix = "$argon2id$v=19$m=19456,t=2,p=1$"
	if !strings.HasPrefix(got, wantPrefix) {
		t.Errorf("Encode() = %q, want prefix %q", got, wantPrefix)
	}
	if n := strings.Count(got, "$"); n != 5 {
		t.Errorf("Encode() has %d dollar separators, want 5: %q", n, got)
	}
}

func TestDecode_RoundTripsWhatEncodeWrote(t *testing.T) {
	salt := []byte("0123456789abcdef")
	key := []byte("0123456789abcdef0123456789abcdef")
	encoded := localauth.Encode(cheapParams, salt, key)

	gotParams, gotSalt, gotKey, err := localauth.Decode(encoded)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if gotParams != cheapParams {
		t.Errorf("Decode() params = %+v, want %+v", gotParams, cheapParams)
	}
	if string(gotSalt) != string(salt) {
		t.Errorf("Decode() salt = %q, want %q", gotSalt, salt)
	}
	if string(gotKey) != string(key) {
		t.Errorf("Decode() key = %q, want %q", gotKey, key)
	}
}

// TestDecode_FailsClosed is the sharpest test in this package.
//
// The encoded string arrives from a DATABASE ROW, so anyone who can write
// one row chooses what this parser is handed. A permissive branch here is
// not a parsing bug: an out-of-range memory parameter reaching argon2.IDKey
// is an allocation of the attacker's choosing inside the control plane, and
// a parse that yielded a zero value instead of an error would turn a
// corrupted row into "no password set", which is an authentication bypass.
func TestDecode_FailsClosed(t *testing.T) {
	tests := []struct {
		name    string
		encoded string
	}{
		{"empty", ""},
		{"not a phc string", "hunter2"},
		{"too few fields", "$argon2id$v=19$m=19456,t=2,p=1$c2FsdA"},
		{"too many fields", "$argon2id$v=19$m=19456,t=2,p=1$c2FsdA$a2V5$extra"},
		{"no leading dollar", "argon2id$v=19$m=19456,t=2,p=1$c2FsdHNhbHRzYWx0c2E$a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2U"},
		{"bcrypt", "$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy"},
		{"argon2i not argon2id", "$argon2i$v=19$m=19456,t=2,p=1$c2FsdHNhbHRzYWx0c2E$a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2U"},
		{"wrong version", "$argon2id$v=16$m=19456,t=2,p=1$c2FsdHNhbHRzYWx0c2E$a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2U"},
		{"non-numeric memory", "$argon2id$v=19$m=lots,t=2,p=1$c2FsdHNhbHRzYWx0c2E$a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2U"},
		{"memory bomb", "$argon2id$v=19$m=4294967295,t=2,p=1$c2FsdHNhbHRzYWx0c2E$a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2U"},
		{"zero memory", "$argon2id$v=19$m=0,t=2,p=1$c2FsdHNhbHRzYWx0c2E$a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2U"},
		{"zero passes", "$argon2id$v=19$m=19456,t=0,p=1$c2FsdHNhbHRzYWx0c2E$a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2U"},
		{"zero parallelism", "$argon2id$v=19$m=19456,t=2,p=0$c2FsdHNhbHRzYWx0c2E$a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2U"},
		{"absurd passes", "$argon2id$v=19$m=19456,t=999,p=1$c2FsdHNhbHRzYWx0c2E$a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2U"},
		{"memory below 8x parallelism", "$argon2id$v=19$m=8,t=2,p=4$c2FsdHNhbHRzYWx0c2E$a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2U"},
		{"trailing junk in cost field", "$argon2id$v=19$m=19456,t=2,p=1junk$c2FsdHNhbHRzYWx0c2E$a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2U"},
		{"salt not base64", "$argon2id$v=19$m=19456,t=2,p=1$not!base64!$a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2U"},
		{"key not base64", "$argon2id$v=19$m=19456,t=2,p=1$c2FsdHNhbHRzYWx0c2E$not!base64!"},
		{"salt too short", "$argon2id$v=19$m=19456,t=2,p=1$c2E$a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2U"},
		{"key too short", "$argon2id$v=19$m=19456,t=2,p=1$c2FsdHNhbHRzYWx0c2E$a2V5"},
		{"embedded NUL", "$argon2id$v=19$m=19456,t=2,p=1$c2FsdHNhbHRzYWx0c2E\x00$a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2U"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, _, err := localauth.Decode(tt.encoded); err == nil {
				t.Errorf("Decode(%q) = nil error, want a refusal", tt.encoded)
			}

			// The same input must also be refused through Verify, which is
			// the door a login actually comes in by. A parser that fails
			// while its caller succeeds anyway would be the bypass this
			// whole test exists to prevent.
			ok, err := localauth.Verify(tt.encoded, "any password at all")
			if ok {
				t.Errorf("Verify(%q) = true", tt.encoded)
			}
			if err == nil {
				t.Errorf("Verify(%q) = nil error, want a refusal", tt.encoded)
			}
		})
	}
}

func TestNeedsRehash(t *testing.T) {
	salt := []byte("0123456789abcdef")
	key := []byte("0123456789abcdef0123456789abcdef")

	tests := []struct {
		name    string
		encoded string
		want    bool
	}{
		{"current policy", localauth.Encode(localauth.DefaultParams, salt, key), false},
		{"stale cost", localauth.Encode(cheapParams, salt, key), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := localauth.NeedsRehash(tt.encoded)
			if err != nil {
				t.Fatalf("NeedsRehash() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("NeedsRehash() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNeedsRehash_PropagatesAnUnusableHash(t *testing.T) {
	// It must not answer "no rehash needed" for a string it could not
	// parse, which would leave a corrupted row in place forever.
	if _, err := localauth.NeedsRehash("not a hash"); err == nil {
		t.Error("NeedsRehash() = nil error for an unparsable hash")
	}
}

func TestHash_RefusesEmptyAndOversizedInput(t *testing.T) {
	if _, err := localauth.Hash(""); err == nil {
		t.Error("Hash(\"\") = nil error, want a refusal")
	}

	// The cap exists because Argon2 hashes the whole password on every
	// pass, so an unbounded field on an unauthenticated endpoint is a CPU
	// and memory amplifier.
	oversized := strings.Repeat("x", 1025)
	if _, err := localauth.Hash(oversized); err == nil {
		t.Error("Hash(oversized) = nil error, want a refusal")
	}
}

func TestVerify_RefusesAnOversizedPasswordWithoutDeriving(t *testing.T) {
	encoded, err := localauth.Hash("a real password here")
	if err != nil {
		t.Fatalf("Hash() error = %v", err)
	}

	// Not an error: the stored value is fine, the submitted one is not, so
	// this is an ordinary authentication failure that costs nothing.
	ok, err := localauth.Verify(encoded, strings.Repeat("x", 4096))
	if err != nil {
		t.Errorf("Verify(oversized) error = %v, want nil", err)
	}
	if ok {
		t.Error("Verify(oversized) = true")
	}
}

func TestDecoyHash_IsUsableAndMatchesNothing(t *testing.T) {
	decoy := localauth.DecoyHash()

	if _, _, _, err := localauth.Decode(decoy); err != nil {
		t.Fatalf("Decode(DecoyHash()) error = %v; the decoy must be a real hash or it costs the wrong amount of time", err)
	}
	// Stable across calls, so a second login does not pay for a second
	// derivation of the decoy itself.
	if again := localauth.DecoyHash(); again != decoy {
		t.Error("DecoyHash() returned a different value on the second call")
	}

	for _, guess := range []string{"", "password", "admin", decoy} {
		ok, err := localauth.Verify(decoy, guess)
		if err != nil {
			t.Fatalf("Verify(decoy, %q) error = %v", guess, err)
		}
		if ok {
			t.Errorf("Verify(decoy, %q) = true; the decoy must be unmatchable", guess)
		}
	}
}

func TestDecoyHash_UsesCurrentParameters(t *testing.T) {
	// If the decoy were derived at a different cost from a real credential,
	// the timing it exists to equalize would be unequal in the other
	// direction, which is the same oracle with the sign flipped.
	params, _, _, err := localauth.Decode(localauth.DecoyHash())
	if err != nil {
		t.Fatalf("Decode(DecoyHash()) error = %v", err)
	}
	if params != localauth.DefaultParams {
		t.Errorf("decoy params = %+v, want DefaultParams %+v", params, localauth.DefaultParams)
	}
}

// FuzzDecode is the property the fixed table above cannot cover: that no
// input at all reaches argon2.IDKey with parameters it did not validate,
// and that nothing panics on the way.
//
// A panic here is a remote denial of service reachable from a database row,
// and a missing range check is an attacker-chosen allocation.
func FuzzDecode(f *testing.F) {
	salt := []byte("0123456789abcdef")
	key := []byte("0123456789abcdef0123456789abcdef")
	f.Add(localauth.Encode(localauth.DefaultParams, salt, key))
	f.Add(localauth.Encode(cheapParams, salt, key))
	f.Add("$argon2id$v=19$m=4294967295,t=2,p=1$c2FsdA$a2V5")
	f.Add("$argon2id$v=19$m=,t=,p=$$")
	f.Add("$$$$$")
	f.Add("")

	f.Fuzz(func(t *testing.T, encoded string) {
		params, gotSalt, gotKey, err := localauth.Decode(encoded)
		if err != nil {
			return
		}

		// Anything Decode accepted is about to be handed to the KDF, so
		// every bound the caller relies on must hold here.
		if params.Threads < 1 || params.Threads > 16 {
			t.Fatalf("Decode accepted parallelism %d", params.Threads)
		}
		if params.Time < 1 || params.Time > 16 {
			t.Fatalf("Decode accepted %d passes", params.Time)
		}
		if params.Memory < 8 || params.Memory > 1<<21 {
			t.Fatalf("Decode accepted %d KiB of memory", params.Memory)
		}
		if params.Memory < uint32(params.Threads)*8 {
			t.Fatalf("Decode accepted memory %d below 8x parallelism %d; argon2 panics on this",
				params.Memory, params.Threads)
		}
		if len(gotSalt) < 8 || len(gotSalt) > 64 {
			t.Fatalf("Decode accepted a %d byte salt", len(gotSalt))
		}
		if len(gotKey) < 16 || len(gotKey) > 64 {
			t.Fatalf("Decode accepted a %d byte key", len(gotKey))
		}

		// And the accepted string must survive a verification without
		// panicking, which is the actual call a login makes.
		if _, err := localauth.Verify(encoded, "fuzz"); err != nil {
			t.Fatalf("Decode accepted %q but Verify rejected it: %v", encoded, err)
		}
	})
}

// FuzzVerify covers the other input, the submitted password, which is
// attacker-controlled by definition.
func FuzzVerify(f *testing.F) {
	f.Add("password")
	f.Add("")
	f.Add("\x00\x00\x00")
	f.Add(strings.Repeat("é", 512))

	encoded, err := localauth.Hash("the fuzz corpus password")
	if err != nil {
		f.Fatalf("Hash() error = %v", err)
	}

	f.Fuzz(func(t *testing.T, password string) {
		// No panic, and no accidental match: nothing but the real password
		// may verify, whatever bytes it is made of.
		ok, err := localauth.Verify(encoded, password)
		if err != nil {
			t.Fatalf("Verify() error = %v on a valid hash", err)
		}
		if ok && password != "the fuzz corpus password" {
			t.Fatalf("Verify() = true for %q", password)
		}
	})
}
