// Tests for SigningKeyPublic, and for the account constructors' refusal of
// a missing operator.
//
// SigningKeyPublic is what internal/meshkey calls to name the signing key it
// is saving, so internal/meshkey's tests reach it too. Coverage is counted
// per package, though, and the property that matters is this package's
// claim, so it is tested here against the account JWT itself.
package meshid_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/meshid"
	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"
)

// TestSigningKeyPublicNamesTheKeyTheBrokerChecks proves the answer is the
// one custody needs.
//
// An operator checking a deployment asks one question: is the key this
// Controller holds one the account JWT allows to issue users? So the
// returned key has to be listed among the account JWT's signing keys, has
// to differ from the account's identity key, and has to be the issuer a
// broker sees on every user the seed signs. A wrong answer here would tell
// an operator the deployment is sound while every connect is refused.
func TestSigningKeyPublicNamesTheKeyTheBrokerChecks(t *testing.T) {
	_, acct := newAccount(t)

	pub, err := meshid.SigningKeyPublic(acct.SigningKeySeed)
	if err != nil {
		t.Fatalf("SigningKeyPublic: %v", err)
	}

	// The account JWT is what the broker trusts, so its signing key list
	// is the authority on whether this key may issue users.
	acctClaims, err := jwt.DecodeAccountClaims(acct.JWT)
	if err != nil {
		t.Fatalf("decoding the account jwt: %v", err)
	}
	if !acctClaims.SigningKeys.Contains(pub) {
		t.Fatalf("SigningKeyPublic returned %q, which the account jwt does not list as a signing key", pub)
	}
	if pub == acct.Subject {
		t.Fatal("SigningKeyPublic returned the account's identity key, not its signing key")
	}

	// A user minted with the same seed must name that key as its issuer,
	// because that is the value the broker checks against the list above.
	issuer, err := meshid.NewIssuer(acct.Subject, acct.SigningKeySeed)
	if err != nil {
		t.Fatalf("NewIssuer: %v", err)
	}
	cred, err := issuer.Issue(meshid.FleetRunnerGrant("runner-1"), meshid.DefaultUserExpiry)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	token, err := jwt.ParseDecoratedJWT(cred.Creds)
	if err != nil {
		t.Fatalf("parsing the creds file: %v", err)
	}
	userClaims, err := jwt.DecodeUserClaims(token)
	if err != nil {
		t.Fatalf("decoding the user jwt: %v", err)
	}
	if userClaims.Issuer != pub {
		t.Errorf("a user signed with this seed names issuer %q, but SigningKeyPublic said %q", userClaims.Issuer, pub)
	}
}

// TestSigningKeyPublicRefusesASeedOfTheWrongKind covers the refusal its doc
// comment gives a reason for: nkeys signs with a user or operator key as
// happily as with an account key, and the user JWT that results fails at
// connect time with an error that names none of this.
//
// Every refusal must wrap ErrNotASeed, so a caller can tell a wrong key
// from a broken one, and must never quote the seed, which is the secret.
func TestSigningKeyPublicRefusesASeedOfTheWrongKind(t *testing.T) {
	op, _ := newAccount(t)
	operatorSeed, err := op.Seed()
	if err != nil {
		t.Fatalf("reading the operator seed: %v", err)
	}
	user, err := nkeys.CreateUser()
	if err != nil {
		t.Fatalf("creating a user key: %v", err)
	}
	userSeed, err := user.Seed()
	if err != nil {
		t.Fatalf("reading the user seed: %v", err)
	}

	for _, tc := range []struct {
		name string
		seed []byte
	}{
		{"a user seed", userSeed},
		{"an operator seed", operatorSeed},
		{"text that is not a seed", []byte("not a seed at all")},
		{"no seed", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pub, err := meshid.SigningKeyPublic(tc.seed)
			if !errors.Is(err, meshid.ErrNotASeed) {
				t.Fatalf("SigningKeyPublic error = %v, want it to wrap ErrNotASeed", err)
			}
			if pub != "" {
				t.Errorf("SigningKeyPublic returned %q alongside its error, want nothing", pub)
			}
			if len(tc.seed) > 0 && strings.Contains(err.Error(), string(tc.seed)) {
				t.Error("the error quotes the seed, which is the secret it was handed")
			}
		})
	}
}

// TestAnAccountNeedsAnOperatorToSignIt covers both constructors' refusal
// of a nil operator.
//
// An account JWT is only trusted because the operator signed it, so an
// account built without one would be a claim nothing vouches for. The
// refusal has to be an error rather than a panic, since the operator comes
// from a bootstrap step that can fail.
func TestAnAccountNeedsAnOperatorToSignIt(t *testing.T) {
	for name, create := range map[string]func(*meshid.Operator, string) (*meshid.Account, error){
		"NewAccount":       meshid.NewAccount,
		"NewSystemAccount": meshid.NewSystemAccount,
	} {
		t.Run(name, func(t *testing.T) {
			acct, err := create(nil, "pleiades")
			if err == nil {
				t.Fatal("an account was created with no operator to sign it")
			}
			if acct != nil {
				t.Errorf("%s returned an account alongside its error", name)
			}
		})
	}
}
