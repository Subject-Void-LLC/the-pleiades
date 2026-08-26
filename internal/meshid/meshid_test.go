package meshid_test

import (
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/meshid"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"
)

// newAccount is the two-step every test here needs: an offline operator
// and an account it signed.
func newAccount(t *testing.T) (*meshid.Operator, *meshid.Account) {
	t.Helper()
	op, err := meshid.NewOperator("test-operator")
	if err != nil {
		t.Fatalf("NewOperator: %v", err)
	}
	acct, err := meshid.NewAccount(op, "pleiades")
	if err != nil {
		t.Fatalf("NewAccount: %v", err)
	}
	return op, acct
}

// TestTheChainIsOperatorSignedAccountSignedUser walks the whole hierarchy
// and asserts each link is signed by the key above it, which is the
// property the entire design rests on.
func TestTheChainIsOperatorSignedAccountSignedUser(t *testing.T) {
	op, acct := newAccount(t)

	opClaims, err := jwt.DecodeOperatorClaims(op.JWT)
	if err != nil {
		t.Fatalf("decoding operator jwt: %v", err)
	}
	if opClaims.Issuer != op.Subject {
		t.Errorf("operator jwt issuer = %q, want self-signed %q", opClaims.Issuer, op.Subject)
	}

	acctClaims, err := jwt.DecodeAccountClaims(acct.JWT)
	if err != nil {
		t.Fatalf("decoding account jwt: %v", err)
	}
	if acctClaims.Issuer != op.Subject {
		t.Errorf("account jwt issuer = %q, want the operator %q", acctClaims.Issuer, op.Subject)
	}

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
		t.Fatalf("decoding user jwt: %v", err)
	}

	// The user names the account it belongs to, and is signed by a SIGNING
	// key rather than by the account identity key. That distinction is the
	// point of the whole hierarchy: if these were equal, the Controller
	// would be holding the key that names the account.
	if userClaims.IssuerAccount != acct.Subject {
		t.Errorf("user IssuerAccount = %q, want %q", userClaims.IssuerAccount, acct.Subject)
	}
	if userClaims.Issuer == acct.Subject {
		t.Error("user jwt was signed by the ACCOUNT IDENTITY key; the whole point of a signing key is that the identity key stays offline")
	}
	if !acctClaims.SigningKeys.Contains(userClaims.Issuer) {
		t.Errorf("user jwt issuer %q is not one of the account's declared signing keys, so a broker would reject it", userClaims.Issuer)
	}
}

// TestTheOperatorKeyIsNeverNeededToIssue is the custody claim stated as a
// test: an Issuer built from nothing but the account's public key and a
// signing seed can mint. If this ever needs the operator, the deployment
// story changes completely.
func TestTheOperatorKeyIsNeverNeededToIssue(t *testing.T) {
	_, acct := newAccount(t)

	issuer, err := meshid.NewIssuer(acct.Subject, acct.SigningKeySeed)
	if err != nil {
		t.Fatalf("NewIssuer from an account public key and a signing seed alone: %v", err)
	}
	if _, err := issuer.Issue(meshid.FleetRunnerGrant("runner-1"), time.Hour); err != nil {
		t.Fatalf("Issue: %v", err)
	}
}

// TestIssuerRefusesAKeyOfTheWrongKind covers the mistake the hierarchy
// makes easy: every seed is an opaque string starting with "S", so handing
// over a user or operator seed instead of the account signing seed is a
// typo away, and the resulting failure would otherwise surface as a
// connect-time rejection on the broker naming none of this.
func TestIssuerRefusesAKeyOfTheWrongKind(t *testing.T) {
	_, acct := newAccount(t)

	userKP, err := nkeys.CreateUser()
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	userSeed, err := userKP.Seed()
	if err != nil {
		t.Fatalf("Seed: %v", err)
	}

	if _, err := meshid.NewIssuer(acct.Subject, userSeed); err == nil {
		t.Error("NewIssuer accepted a USER seed as an account signing key")
	}
	if _, err := meshid.NewIssuer("not-a-key", acct.SigningKeySeed); err == nil {
		t.Error("NewIssuer accepted a subject that is not an account public key")
	}
}

// TestACredentialMustExpire refuses the identity that cannot be revoked by
// waiting, which is the only revocation mechanism this stage has.
func TestACredentialMustExpire(t *testing.T) {
	_, acct := newAccount(t)
	issuer, err := meshid.NewIssuer(acct.Subject, acct.SigningKeySeed)
	if err != nil {
		t.Fatalf("NewIssuer: %v", err)
	}
	for _, d := range []time.Duration{0, -time.Second} {
		if _, err := issuer.Issue(meshid.FleetRunnerGrant("r"), d); err == nil {
			t.Errorf("Issue accepted an expiry of %v", d)
		}
	}
}

// TestFleetRunnerGrantCoversTheInboxSpace is the single most load-bearing
// assertion in this file.
//
// A Runner subscribes to no pleiades subject at all: it pulls dispatch by
// publishing a request and receiving on a reply inbox. A grant listing
// every application subject perfectly and omitting the inbox produces a
// Runner that connects, publishes happily, and never receives one job.
// That failure looks like a broken consumer rather than a broken
// permission, which is why it is pinned here rather than left to the
// container gate to discover.
func TestFleetRunnerGrantCoversTheInboxSpace(t *testing.T) {
	g := meshid.FleetRunnerGrant("runner-1")
	found := false
	for _, s := range g.Sub {
		if s == "_INBOX.>" {
			found = true
		}
	}
	if !found {
		t.Fatalf("FleetRunnerGrant Sub = %v, which does not cover the reply inbox space", g.Sub)
	}
}

// TestFleetRunnerGrantCoversEveryEnumeratedOperation pins the surface Phase
// 101b's recon actually measured on the wire, rather than the one an
// architecture diagram would predict. A permission set is deny-by-default,
// so anything missing here is a Runner that authenticates and then fails
// at the first operation nobody wrote down.
func TestFleetRunnerGrantCoversEveryEnumeratedOperation(t *testing.T) {
	g := meshid.FleetRunnerGrant("runner-1")

	// Each entry is one thing the Runner really does, with the reason it
	// is not obvious noted where it is not obvious.
	required := map[string]string{
		topology.LogSubjectAll():                                  "publishes a job log line for every execution",
		topology.ResultSubjectAll():                               "publishes a WAL result when RUNNER_WAL_DIR is set",
		topology.DeadLetterSubject(topology.DispatchSubjectAll()): "republishes a dead letter after MaxDeliver",
		"$KV.Pleiades_Locks.>":                                    "takes the per-device execution lease, including a raw TTL-refresh publish",
		"$KV.Pleiades_Dedup.>":                                    "reads and writes consumer-side duplicate suppression",
		"$JS.API.INFO":                                            "nats.go calls AccountInfo unconditionally when preparing a KV bucket",
		"$JS.API.STREAM.INFO.PLEIADES":                            "binds the stream at startup",
		"$JS.API.STREAM.CREATE.PLEIADES":                          "creates the stream if absent, which topology documents as deliberate",
		"$JS.API.STREAM.INFO.KV_Pleiades_Locks":                   "binds the lock bucket",
		"$JS.API.STREAM.INFO.KV_Pleiades_Dedup":                   "binds the dedup bucket",
		"$JS.API.DIRECT.GET.KV_Pleiades_Locks.>":                  "a contended Acquire reads through DIRECT.GET because the bucket sets AllowDirect",
		"$JS.API.CONSUMER.CREATE.PLEIADES.runner-agent.>":         "creates its own dispatch consumer, filter subject included in the API subject",
		"$JS.API.CONSUMER.INFO.PLEIADES.runner-agent.>":           "the liveness heartbeat probes it every ten seconds",
		"$JS.API.CONSUMER.MSG.NEXT.PLEIADES.runner-agent.>":       "pulls work",
		"$JS.ACK.PLEIADES.runner-agent.>":                         "Ack, Nak and Term are core publishes to the reply subject",
	}

	have := make(map[string]bool, len(g.Pub))
	for _, s := range g.Pub {
		have[s] = true
	}
	for subject, why := range required {
		if !have[subject] {
			t.Errorf("FleetRunnerGrant does not permit %q, which the Runner needs because it %s", subject, why)
		}
	}
}

// TestGrantsNameNoWildcardedSubjectByAccident guards the mistake this
// package walked into once already: the per-job subject builders SANITIZE
// their argument, so LogSubject(">") returns a literal token rather than a
// wildcard and a grant built that way silently permits nothing useful.
func TestGrantsNameNoWildcardedSubjectByAccident(t *testing.T) {
	for _, g := range []meshid.Grant{
		meshid.FleetRunnerGrant("r"),
		meshid.ControllerGrant("c"),
	} {
		for _, s := range append(append([]string{}, g.Pub...), g.Sub...) {
			if strings.Contains(s, "unnamed-") {
				t.Errorf("grant %q contains %q, which is a sanitized token where a wildcard was meant", g.Name, s)
			}
		}
	}
}
