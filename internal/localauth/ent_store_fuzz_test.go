// Fuzzing of Authenticate's email argument, through the real store.
//
// The address on a login form is the one input an unauthenticated caller
// controls completely, and it reaches three things: the normalizer, a SQL
// query, and the choice between a real verification and the decoy. This
// target drives all three through the same entStore production builds, over
// a real SQLite database, and checks the three properties the phase asked
// for: nothing panics, a match only ever lands on the row whose address IS
// the normalized input, and every refusal takes the unknown-address path
// (one decoy derivation, the bare sentinel, no row touched), so a malformed
// address cannot become an account oracle.
//
// The path is observed by counting (ObserveDecoys), never with a clock: a
// timing assertion inside a fuzz body fails on a loaded machine rather than
// on a defect. What the decoy path COSTS is proven separately, by
// TestAuthenticate_UnknownAccountIsNotFasterThanAKnownOne and by the
// release gate's timing band against the real binary.
package localauth_test

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/localcredential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/localauth"
)

// Addresses built from code points rather than typed as escapes, so the
// source stays plain ASCII and every character is named where it is used.
var (
	// nfcJose is "jose" with its accent precomposed (U+00E9).
	nfcJose = "jos" + string(rune(0xE9)) + "@example.test"
	// nfdJose is the same word decomposed: "jose" and a combining acute
	// accent (U+0301). It looks identical and is a different key.
	nfdJose = "jose" + string(rune(0x301)) + "@example.test"
	// replacementRow holds U+FFFD, the character strings.ToLower writes in
	// place of an invalid byte. A row like this can be created today, which
	// is what makes a repairing normalizer a way in.
	replacementRow = "a" + string(utf8.RuneError) + "b@example.test"
)

// fuzzAccounts are seeded straight through ent, bypassing internal/access,
// so rows a normalizer might be tricked into matching exist to be matched.
// All share one password, so any match succeeds and the matched row can be
// read back and compared.
var fuzzAccounts = []string{
	"admin@example.test",
	"kelvin@example.test",
	nfcJose,
	nfdJose,
	replacementRow,
}

// unsafeAddress reports whether a submitted address is malformed in a way
// that must never authenticate: invalid UTF-8, or a control or text
// direction character anywhere once the edges are trimmed. It is written
// out here rather than calling the product's own check, so the test is a
// second opinion rather than the implementation agreeing with itself.
func unsafeAddress(email string) bool {
	if !utf8.ValidString(email) {
		return true
	}
	return strings.ContainsFunc(strings.TrimSpace(email), func(r rune) bool {
		switch {
		case unicode.IsControl(r):
			return true
		case r == 0x061c, r == 0x200e, r == 0x200f:
			return true
		case r >= 0x202a && r <= 0x202e, r >= 0x2066 && r <= 0x2069:
			return true
		}
		return false
	})
}

func FuzzAuthenticateEmail(f *testing.F) {
	store, client := newStore(f, localauth.DefaultLockoutPolicy)
	ctx := context.Background()

	var decoys atomic.Int64
	localauth.ObserveDecoys(store, func() { decoys.Add(1) })

	for _, addr := range fuzzAccounts {
		seedUser(f, client, addr)
		if err := store.SetPassword(ctx, addr, testPassword, false); err != nil {
			f.Fatalf("SetPassword(%q) error = %v", addr, err)
		}
	}

	for _, addr := range fuzzAccounts {
		f.Add(addr)
	}
	for _, seed := range []string{
		"ADMIN@EXAMPLE.TEST",
		"  admin@example.test\t",
		string(rune(0x85)) + "admin@example.test",
		"admin@example.test\x00",
		"adm\x00in@example.test",
		"a\xffb@example.test",
		"admin\xff@example.test",
		"adm\tin@example.test",
		"admin\x7f@example.test",
		"adm" + string(rune(0x85)) + "in@example.test",
		"admin" + string(rune(0x202e)) + "@example.test",
		"admin" + string(rune(0x301)) + "@example.test",
		"adm" + string(rune(0x130)) + "n@example.test",
		string(rune(0x212A)) + "elvin@example.test",
		"",
		"nonsense",
		strings.Repeat("a", 4096) + "@example.test",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, email string) {
		before := decoys.Load()
		account, err := store.Authenticate(ctx, email, testPassword)
		ran := decoys.Load() - before

		if err == nil {
			// A match must land on the row whose address IS the normalized
			// input. Account.Subject is built from the input, so it cannot
			// answer this; the row read back by id can.
			row, getErr := client.User.Get(ctx, account.UserID)
			if getErr != nil {
				t.Fatalf("Authenticate(%q) matched user %d, which cannot be read back: %v", email, account.UserID, getErr)
			}
			want, normErr := localauth.NormalizeEmail(email)
			if normErr != nil || row.Email != want {
				t.Fatalf("Authenticate(%q) signed in as %q, but the address normalizes to %q (err %v)",
					email, row.Email, want, normErr)
			}
			if unsafeAddress(email) {
				t.Fatalf("a malformed address %q signed in as %q; it must fail as an unknown address does", email, row.Email)
			}
			if ran != 0 {
				t.Fatalf("a successful sign-in also ran %d decoy derivations", ran)
			}
			return
		}

		// Every refusal is the bare sentinel. A wrapped error carries a
		// distinguishing message, and a store error (a query the database
		// refused) is a different path that skipped the decoy.
		if err != localauth.ErrInvalidCredentials {
			t.Fatalf("Authenticate(%q) error = %v, want the bare ErrInvalidCredentials", email, err)
		}
		// Every seeded account shares the password used here, so no
		// refusal can be a wrong password: it must be the unknown-address
		// path, which runs exactly one decoy derivation.
		if ran != 1 {
			t.Fatalf("Authenticate(%q) was refused after %d decoy derivations, want exactly 1, "+
				"the unknown-address path; any other count is a path an attacker can time", email, ran)
		}
		if n, countErr := client.LocalCredential.Query().
			Where(localcredential.FailedAttemptsGT(0)).Count(ctx); countErr != nil || n != 0 {
			t.Fatalf("Authenticate(%q) moved %d accounts' failure counters (err %v); a refusal for an "+
				"address matching no account touched one that exists", email, n, countErr)
		}
	})
}
