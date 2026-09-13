// Issuing a user credential from an account signing key.

package meshid

import (
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"
)

// DefaultUserExpiry is how long a minted credential is valid for when a
// caller does not say.
//
// Twelve hours is chosen against a real window rather than for roundness.
// It is longer than any single dispatch this platform runs, so a
// credential never expires underneath work already in flight, and short
// enough that a leaked one is useless by the next working day. It is
// deliberately NOT tuned to a job's duration: a Runner holds one identity
// across many jobs, and re-minting per job would put the issuance path on
// the dispatch path, which is exactly the coupling this phase refused when
// it rejected auth callout.
//
// A Smart Hands engagement wants a much shorter one, passed explicitly.
// That is the case this parameter exists for.
const DefaultUserExpiry = 12 * time.Hour

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
