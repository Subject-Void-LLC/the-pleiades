// Issuing a user credential from an account signing key.

package meshid

import (
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"
)

// DefaultUserExpiry is how long an EDGE credential is valid for when a
// caller does not say.
//
// Twelve hours is chosen against a real window rather than for roundness.
// It is longer than any single dispatch this platform runs, so a
// credential never expires underneath work already in flight, and short
// enough that a leaked one is useless by the next working day. It is
// deliberately NOT tuned to a job's duration: re-minting per job would put
// the issuance path on the dispatch path, which is exactly the coupling
// this package refused when it rejected auth callout.
//
// This is the window a Smart Hands engagement wants, and it is the one
// this constant is named for. It is NOT the right window for a fleet
// Runner, which is what DefaultFleetExpiry exists to say out loud.
const DefaultUserExpiry = 12 * time.Hour

// DefaultFleetExpiry is how long a fleet Runner's credential is valid for
// when a deployment does not say.
//
// This constant exists because the obvious answer was measured and was
// wrong. DefaultUserExpiry's own text used to claim twelve hours suited a
// Runner, on the reasoning that a Runner holds one identity across many
// jobs. What that misses is what happens at the END of the window:
// TestReleaseGate_AnExpiringCredentialEvictsALiveConnection shows the
// broker evicting a live connection when its credential lapses, and
// nats.go then abandons reconnection after the same authentication error
// twice regardless of MaxReconnects(-1). A Runner on a twelve hour
// credential therefore stops taking work twelve hours after it starts,
// and it stops QUIETLY: the process is healthy, it is connected to
// nothing, and it looks exactly like a Runner with no jobs.
//
// Thirty days is a deliberately unambitious number, and the reasoning is
// about what a deployment can be relied on to do rather than about
// cryptography. A credential must outlive any window in which nothing
// renews it, because the failure mode of a lapse is a silently idle
// fleet, and the failure mode of a long window is a bearer token that
// stays useful if it leaks. Those are not symmetric: the first is
// invisible and the second requires a compromise first.
//
// It is a CEILING rather than a target. Where the renewal path exists, a
// credential is replaced long before this, and topology.WithCredentialSource
// is what lets a live connection pick the replacement up without a
// restart (TestReleaseGate_ARenewedCredentialKeepsALiveConnectionWorking).
// This is what the fleet falls back to when nothing renews, which is the
// state every deployment is in until it is configured otherwise.
//
// Shorten it wherever something does renew. Do not shorten it on the
// reasoning that shorter is safer, without first checking that a renewal
// actually runs, because the thing that breaks is not loud.
const DefaultFleetExpiry = 30 * 24 * time.Hour

// Issuer mints user credentials for one account.
//
// It holds a signing key, which is the only key in the hierarchy a server
// process is allowed to hold, and it holds it in memory only: reading it
// out of storage and sealing it at rest belong to the caller, because this
// package does not do custody.
type Issuer struct {
	accountSubject string
	signer         nkeys.KeyPair
}

// NewIssuer builds an Issuer from an account's public key and the seed of
// one of that account's signing keys.
//
// It does NOT verify that the seed is actually one of the account's
// declared signing keys, because that fact lives in the account JWT, which
// this constructor is not given. The broker performs exactly that check at
// connect time and rejects the user if it fails. That division is
// deliberate: duplicating the check here would let it drift, and the
// authoritative answer is the server's.
func NewIssuer(accountSubject string, signingKeySeed []byte) (*Issuer, error) {
	if !nkeys.IsValidPublicAccountKey(accountSubject) {
		return nil, fmt.Errorf("%w: %q is not an account public key", ErrNotASeed, accountSubject)
	}
	kp, err := accountSigner(signingKeySeed)
	if err != nil {
		return nil, err
	}
	return &Issuer{accountSubject: accountSubject, signer: kp}, nil
}

// Credential is one minted identity, in the two forms a caller needs.
type Credential struct {
	// Creds is the .creds file body: the user JWT and its seed, in the
	// decorated format nats.UserCredentials reads. This is the whole
	// secret, and it is what a Runner is given.
	Creds []byte
	// Subject is the user's public key, which is safe to log and is how a
	// server names this identity in its own logs and in a revocation
	// list.
	Subject string
	// Expires is when the broker will stop accepting it.
	Expires time.Time
}

// Issue mints a credential carrying g, valid for expiry.
//
// A fresh user key pair is generated per call and its seed goes into the
// returned creds file. Nothing retains it, including this package: a
// credential that cannot be re-derived is a credential whose only copy is
// the one the holder was given, which is what makes revoking it by expiry
// meaningful.
func (i *Issuer) Issue(g Grant, expiry time.Duration) (Credential, error) {
	if expiry <= 0 {
		return Credential{}, errors.New("meshid: a credential needs a positive expiry; an identity that never expires cannot be revoked by waiting")
	}

	userKP, err := nkeys.CreateUser()
	if err != nil {
		return Credential{}, fmt.Errorf("meshid: creating user key: %w", err)
	}
	userPub, err := userKP.PublicKey()
	if err != nil {
		return Credential{}, fmt.Errorf("meshid: reading user public key: %w", err)
	}

	claims := jwt.NewUserClaims(userPub)
	claims.IssuerAccount = i.accountSubject
	expires := time.Now().Add(expiry)
	claims.Expires = expires.Unix()
	g.apply(claims)

	token, err := claims.Encode(i.signer)
	if err != nil {
		return Credential{}, fmt.Errorf("meshid: encoding user jwt: %w", err)
	}

	seed, err := userKP.Seed()
	if err != nil {
		return Credential{}, fmt.Errorf("meshid: reading user seed: %w", err)
	}
	creds, err := jwt.FormatUserConfig(token, seed)
	if err != nil {
		return Credential{}, fmt.Errorf("meshid: formatting creds: %w", err)
	}

	return Credential{Creds: creds, Subject: userPub, Expires: expires}, nil
}
