// Package meshid: the scoped signing key that issues Runner credentials
// (broker-enforced Runner identity, 0.5.0).
//
// NATS expands the {{name()}} template in a permission only for a user
// signed by a scoped signing key, and such a user must carry no permissions
// of its own. So a Runner's credential is signed by a key the account
// delegates to the role "runner", whose template holds the whole runner
// grant, with the two identity subjects written as {{name()}}: a Runner can
// publish facts and journal batches only under its own name. Measured against
// nats-server 2.14.4: the plain form refuses every publish, the scoped form
// enforces the name.
//
// The scoped seed is held apart from the account's signing seed, so a stolen
// Runner signer mints Runner credentials and nothing else. Storage is the
// caller's: internal/meshkey seals the seed under the envelope, beside the
// account seed, and this package does no custody.
package meshid

import (
	"fmt"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"
)

// RunnerRole is the role the account names its runner scope by.
const RunnerRole = "runner"

// runnerIdentitySubjects are the two runner subjects the broker expands to
// the Runner's own name. They replace the fleet-wide facts and journal
// subjects in the runner template.
var runnerIdentitySubjects = map[string]string{
	topology.FactsSubjectAll():   "facts.{{name()}}.>",
	topology.JournalSubjectAll(): "journal.{{name()}}.>",
}

// RunnerTemplate is the permission template a scoped Runner credential is
// granted: FleetRunnerGrant's publish list with the facts and journal
// subjects limited to the Runner's own name, and its subscribe list as it
// is.
func RunnerTemplate() jwt.UserPermissionLimits {
	g := FleetRunnerGrant(RunnerRole)
	var limits jwt.UserPermissionLimits
	limits.NatsLimits = jwt.NatsLimits{Subs: jwt.NoLimit, Data: jwt.NoLimit, Payload: jwt.NoLimit}
	for i, s := range g.Pub {
		if templated, ok := runnerIdentitySubjects[s]; ok {
			g.Pub[i] = templated
		}
	}
	limits.Pub.Allow = append(jwt.StringList{}, g.Pub...)
	limits.Sub.Allow = append(jwt.StringList{}, g.Sub...)
	return limits
}

// newRunnerScope mints the scoped signing key for the account's Runner role:
// its public key is the scope's key, its seed is returned for storage.
func newRunnerScope() (*jwt.UserScope, []byte, error) {
	kp, err := nkeys.CreateAccount()
	if err != nil {
		return nil, nil, fmt.Errorf("meshid: creating the runner signing key: %w", err)
	}
	pub, err := kp.PublicKey()
	if err != nil {
		return nil, nil, fmt.Errorf("meshid: reading the runner signing key's public key: %w", err)
	}
	seed, err := kp.Seed()
	if err != nil {
		return nil, nil, fmt.Errorf("meshid: reading the runner signing key's seed: %w", err)
	}
	scope := jwt.NewUserScope()
	scope.Key = pub
	scope.Role = RunnerRole
	scope.Template = RunnerTemplate()
	scope.Description = "Runner credentials: publish facts and journal batches under the Runner's own name only"
	return scope, seed, nil
}

// RunnerIssuer mints Runner credentials under the account's runner scope.
// Its credentials carry no permissions of their own: the scope's template
// grants them, which is what makes the {{name()}} expansion apply.
type RunnerIssuer struct {
	accountSubject string
	signer         nkeys.KeyPair
}

// NewRunnerIssuer returns the issuer for accountSubject's runner scope,
// from the scope's seed.
func NewRunnerIssuer(accountSubject string, runnerSeed []byte) (*RunnerIssuer, error) {
	kp, err := nkeys.FromSeed(runnerSeed)
	if err != nil {
		return nil, fmt.Errorf("meshid: reading the runner signing key's seed: %w", err)
	}
	return &RunnerIssuer{accountSubject: accountSubject, signer: kp}, nil
}

// Issue mints a Runner credential named name, valid for expiry. The name is
// the identity the broker enforces, so it must be a single subject token: no
// dots, spaces, wildcards or path separators.
func (r *RunnerIssuer) Issue(name string, expiry time.Duration) (Credential, error) {
	if err := validRunnerName(name); err != nil {
		return Credential{}, err
	}
	userKP, err := nkeys.CreateUser()
	if err != nil {
		return Credential{}, fmt.Errorf("meshid: creating runner user key: %w", err)
	}
	userPub, err := userKP.PublicKey()
	if err != nil {
		return Credential{}, fmt.Errorf("meshid: reading runner user public key: %w", err)
	}
	claims := jwt.NewUserClaims(userPub)
	claims.Name = name
	claims.IssuerAccount = r.accountSubject
	// A scoped user carries no permissions or limits of its own; the scope's
	// template grants them. NewUserClaims sets a default limit, which the
	// library refuses on a scoped user (ValidateScopedSigner).
	claims.UserPermissionLimits = jwt.UserPermissionLimits{}
	expires := time.Now().Add(expiry)
	claims.Expires = expires.Unix()
	token, err := claims.Encode(r.signer)
	if err != nil {
		return Credential{}, fmt.Errorf("meshid: encoding runner user jwt: %w", err)
	}
	seed, err := userKP.Seed()
	if err != nil {
		return Credential{}, fmt.Errorf("meshid: reading runner user seed: %w", err)
	}
	creds, err := jwt.FormatUserConfig(token, seed)
	if err != nil {
		return Credential{}, fmt.Errorf("meshid: formatting runner creds: %w", err)
	}
	return Credential{Creds: creds, Subject: userPub, Expires: expires}, nil
}

// validRunnerName refuses a name that is not one NATS subject token, since
// the name becomes part of the subjects the Runner may publish on.
func validRunnerName(name string) error {
	if name == "" {
		return fmt.Errorf("meshid: a runner needs a name")
	}
	for _, r := range name {
		switch {
		case r == '.', r == '*', r == '>', r == ' ', r == '/', r == '\\', r < 0x21, r == 0x7f:
			return fmt.Errorf("meshid: runner name %q is not a single subject token", name)
		}
	}
	return nil
}
