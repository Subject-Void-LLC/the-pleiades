// Package meshid mints the NATS identities this mesh authenticates with.
//
// # The key hierarchy is the security boundary, not the JWT format
//
// NATS decentralized authentication has three levels, and which process
// holds which key is the whole design. Getting the format right and the
// custody wrong buys nothing.
//
//   - The OPERATOR key signs account JWTs. It is used approximately never
//     and stays offline: nsc on an air-gapped machine, or an HSM. This
//     package can create one, because a test and a bootstrap command need
//     to, but the Controller must never hold one. An operator key can mint
//     a new account, and a new account is a new tenant of the mesh.
//
//   - The ACCOUNT IDENTITY key names the account and signs nothing in
//     normal operation. It stays offline too, because re-minting the
//     account is the only thing it is for.
//
//   - The ACCOUNT SIGNING key is the only one the Controller holds. NATS
//     supports signing keys distinct from the account identity key exactly
//     so the issuer's key can be revoked and rolled without re-minting the
//     account, which is what makes it rotatable and therefore what makes
//     it safe to keep online.
//
// Write that down wherever this is deployed: the Controller holding an
// operator key would mean a Controller compromise is a mesh compromise
// rather than an account compromise, and no amount of JWT expiry would
// contain it.
//
// # Why a library rather than the nsc binary
//
// Every step here is pure Go through nats-io/jwt/v2 and nkeys, with no
// external tooling on any path. That is a requirement rather than a
// preference: minting a credential has to work inside the Controller
// process, at the edge, in a container that ships one static binary, and
// in a test. Shelling out to nsc would make identity issuance depend on a
// second artifact being installed, which is the same class of dependency
// PLAN.md Section 17.4 refuses for PFX unlocking.
//
// # What this package deliberately does not do
//
// It does not connect to NATS, it does not read configuration, and it does
// not decide policy. It turns keys and a permission set into a signed
// credential and nothing else, so the one place that decides WHICH
// permissions a given Runner gets can be tested without a broker, and so
// this package can be exercised entirely in memory.
//
// Custody is likewise elsewhere. The account signing key is sealed at rest
// by internal/crypto's existing envelope service under the one
// MASTER_ENCRYPTION_KEY, in its own entity rather than as a Credential
// row: a Credential is a tenant-owned secret with a mandatory organization
// edge and an injector document, and a control plane signing key is none
// of those things.
package meshid

import (
	"errors"
	"fmt"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"
)

// ErrNotASeed reports a seed that is not the kind of key it was asked to
// be, which is the mistake most likely to be made when wiring this by
// hand: an account seed and an operator seed are both opaque strings
// beginning with "S", and confusing them is exactly the failure the key
// hierarchy above exists to prevent.
var ErrNotASeed = errors.New("meshid: not a valid seed of the expected kind")

// Operator is the offline root of trust. It exists in this package so a
// bootstrap command and a test can create one, never so a server process
// can hold one.
type Operator struct {
	// Subject is the operator's public key, which is what a server's own
	// configuration trusts.
	Subject string
	// JWT is the self-signed operator claim.
	JWT string

	kp nkeys.KeyPair
}

// Account is one tenant of the mesh. Its identity key signs nothing after
// creation; its SIGNING key is what issues users.
type Account struct {
	// Subject is the account's public key, which is the value a user JWT
	// names as its issuer account and the key a resolver stores the
	// account JWT under.
	Subject string
	// JWT is the account claim, signed by the operator.
	JWT string
	// SigningKeySeed is the seed of the rotatable signing key the
	// Controller is expected to hold. It is returned rather than kept
	// because this package does not do custody.
	SigningKeySeed []byte

	kp nkeys.KeyPair
}

// NewOperator creates an operator and self-signs its JWT.
//
// name is a label only. NATS identifies an operator by its public key, so
// two operators with the same name are two different operators, and
// renaming one changes nothing about what it can sign.
func NewOperator(name string) (*Operator, error) {
	kp, err := nkeys.CreateOperator()
	if err != nil {
		return nil, fmt.Errorf("meshid: creating operator key: %w", err)
	}
	pub, err := kp.PublicKey()
	if err != nil {
		return nil, fmt.Errorf("meshid: reading operator public key: %w", err)
	}

	claims := jwt.NewOperatorClaims(pub)
	claims.Name = name
	token, err := claims.Encode(kp)
	if err != nil {
		return nil, fmt.Errorf("meshid: encoding operator jwt: %w", err)
	}
	return &Operator{Subject: pub, JWT: token, kp: kp}, nil
}

// Seed returns the operator's own seed, for a bootstrap command that has
// to write it somewhere offline. Nothing in a server process should call
// this.
func (o *Operator) Seed() ([]byte, error) {
	return o.kp.Seed()
}

// NewAccount creates an account under op, gives it a dedicated signing
// key, and signs the account JWT with the operator key.
//
// The signing key is added at creation rather than later because an
// account whose only issuer is its own identity key cannot rotate: the
// thing being rotated would be the account's name. Creating it up front
// means the Controller never has a reason to hold the identity key at all,
// which is the property this whole package is arranged around.
func NewAccount(op *Operator, name string) (*Account, error) {
	if op == nil {
		return nil, errors.New("meshid: an account needs an operator to sign it")
	}
	kp, err := nkeys.CreateAccount()
	if err != nil {
		return nil, fmt.Errorf("meshid: creating account key: %w", err)
	}
	pub, err := kp.PublicKey()
	if err != nil {
		return nil, fmt.Errorf("meshid: reading account public key: %w", err)
	}

	signing, err := nkeys.CreateAccount()
	if err != nil {
		return nil, fmt.Errorf("meshid: creating account signing key: %w", err)
	}
	signingPub, err := signing.PublicKey()
	if err != nil {
		return nil, fmt.Errorf("meshid: reading signing key public key: %w", err)
	}
	signingSeed, err := signing.Seed()
	if err != nil {
		return nil, fmt.Errorf("meshid: reading signing key seed: %w", err)
	}

	claims := jwt.NewAccountClaims(pub)
	claims.Name = name
	claims.SigningKeys.Add(signingPub)
	token, err := claims.Encode(op.kp)
	if err != nil {
		return nil, fmt.Errorf("meshid: encoding account jwt: %w", err)
	}

	return &Account{Subject: pub, JWT: token, SigningKeySeed: signingSeed, kp: kp}, nil
}

// accountSigner parses a signing key seed into a usable key pair, refusing
// anything that is not an account-kind seed.
//
// The check is not ceremony. nkeys will happily sign with a user or
// operator key pair, and a user JWT signed by the wrong kind of key fails
// at CONNECT time on the broker with an error that names none of this, so
// the refusal belongs here where the mistake is still legible.
func accountSigner(seed []byte) (nkeys.KeyPair, error) {
	kp, err := nkeys.FromSeed(seed)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNotASeed, err)
	}
	pub, err := kp.PublicKey()
	if err != nil {
		return nil, fmt.Errorf("meshid: reading public key from seed: %w", err)
	}
	if !nkeys.IsValidPublicAccountKey(pub) {
		return nil, fmt.Errorf("%w: seed is not an account seed (its public key is %q)", ErrNotASeed, pub[:1])
	}
	return kp, nil
}
