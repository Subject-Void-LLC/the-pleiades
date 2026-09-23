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

// subjectMatches reports whether a NATS permission pattern covers a
// concrete subject, by NATS's own token rules: `*` matches exactly one
// token, and `>` matches ONE OR MORE remaining tokens and is legal only as
// the final token.
//
// This exists because the assertion it serves cannot be written with
// string equality, which is what the previous version of this test used.
// A hand-written list of expected grant entries compared by equality
// against the grant asserts only that the grant equals itself: this
// package shipped four grants whose subjects were each one wildcard away
// from the subject the driver actually sends, and that test passed on
// every one of them. Matching is the property that matters, so matching is
// what is asserted.
//
// The one rule the whole bug turned on is `>` matching one or more rather
// than zero, and TestSubjectMatchesFollowsNatsTokenRules pins it directly
// so this helper cannot drift into agreeing with a broken grant.
func subjectMatches(pattern, subject string) bool {
	p := strings.Split(pattern, ".")
	s := strings.Split(subject, ".")
	for i, tok := range p {
		if tok == ">" {
			// Final token only, and it needs at least one token left.
			return i == len(p)-1 && len(s) > i
		}
		if i >= len(s) {
			return false
		}
		if tok != "*" && tok != s[i] {
			return false
		}
	}
	return len(p) == len(s)
}

// TestSubjectMatchesFollowsNatsTokenRules pins the helper above, and its
// third case is the one this package got wrong in production: a pattern
// ending in `.>` does NOT cover the subject that stops at the token before
// it.
func TestSubjectMatchesFollowsNatsTokenRules(t *testing.T) {
	for _, tc := range []struct {
		pattern, subject string
		want             bool
		why              string
	}{
		{"a.b.c", "a.b.c", true, "an exact subject matches itself"},
		{"a.b.>", "a.b.c", true, "> matches one trailing token"},
		{"a.b.>", "a.b.c.d", true, "> matches several trailing tokens"},
		{"a.b.>", "a.b", false, "> matches one or more tokens, never zero: the defect this package shipped"},
		{"a.*.c", "a.b.c", true, "* matches exactly one token"},
		{"a.*.c", "a.b.x.c", false, "* never spans two tokens"},
		{"a.>.c", "a.b.c", false, "> in a non-final token is not a wildcard: the ControllerGrant defect"},
		{"a.b.c", "a.b", false, "a shorter subject does not match"},
		{"a.b", "a.b.c", false, "a longer subject does not match without a wildcard"},
	} {
		if got := subjectMatches(tc.pattern, tc.subject); got != tc.want {
			t.Errorf("subjectMatches(%q, %q) = %v, want %v: %s", tc.pattern, tc.subject, got, tc.want, tc.why)
		}
	}
}

// permits reports whether any entry in a grant's publish list covers
// subject.
func permits(entries []string, subject string) bool {
	for _, e := range entries {
		if subjectMatches(e, subject) {
			return true
		}
	}
	return false
}

// TestFleetRunnerGrantCoversEveryEnumeratedOperation pins the surface Phase
// 101b's recon measured on the wire, rather than the one an architecture
// diagram would predict. A permission set is deny-by-default, so anything
// missing here is a Runner that authenticates and then fails at the first
// operation nobody wrote down.
//
// Every entry below is the subject the DRIVER sends, taken from nats.go's
// own templates rather than from this package's idea of them, and it is
// checked by NATS matching rather than by equality. The three JetStream
// consumer entries are the ones that were wrong: nats.go's templates are
// "CONSUMER.INFO.%s.%s" and "CONSUMER.MSG.NEXT.%s.%s"
// (jetstream/api.go:58,61), which end at the consumer name, and the grant
// demanded a token after it.
func TestFleetRunnerGrantCoversEveryEnumeratedOperation(t *testing.T) {
	g := meshid.FleetRunnerGrant("runner-1")

	required := map[string]string{
		topology.LogSubject("job-1"):                                             "publishes a job log line for every execution",
		topology.ResultSubject("job-1"):                                          "publishes a WAL result when RUNNER_WAL_DIR is set",
		topology.JournalSubject("job-1"):                                         "publishes the run journal for every task it executes",
		topology.DeadLetterSubject(topology.DispatchSubject("d")):                "republishes a dead letter after MaxDeliver",
		"$KV.Pleiades_Locks.device-1":                                            "takes the per-device execution lease, including a raw TTL-refresh publish",
		"$KV.Pleiades_Dedup.job-1_device-1":                                      "reads and writes consumer-side duplicate suppression",
		"$JS.API.INFO":                                                           "nats.go calls AccountInfo unconditionally when preparing a KV bucket",
		"$JS.API.STREAM.INFO.PLEIADES":                                           "binds the stream at startup",
		"$JS.API.STREAM.CREATE.PLEIADES":                                         "creates the stream if absent, which topology documents as deliberate",
		"$JS.API.STREAM.INFO.KV_Pleiades_Locks":                                  "binds the lock bucket",
		"$JS.API.STREAM.INFO.KV_Pleiades_Dedup":                                  "binds the dedup bucket",
		"$JS.API.DIRECT.GET.KV_Pleiades_Locks.device-1":                          "a contended Acquire reads through DIRECT.GET because the bucket sets AllowDirect",
		"$JS.API.CONSUMER.CREATE.PLEIADES.runner-agent.pleiades.jobs.dispatch.>": "creates its own dispatch consumer, filter subject included in the API subject",
		"$JS.API.CONSUMER.INFO.PLEIADES.runner-agent":                            "the liveness heartbeat probes it every ten seconds",
		"$JS.API.CONSUMER.MSG.NEXT.PLEIADES.runner-agent":                        "pulls work",
		"$JS.ACK.PLEIADES.runner-agent.1.2.3.4.5.6":                              "Ack, Nak and Term are core publishes to the reply subject",
		"$JS.API.CONSUMER.CREATE.PLEIADES.runner-check.pleiades.jobs.check.>":    "creates its own check consumer at startup, and exits if it cannot",
		"$JS.API.CONSUMER.INFO.PLEIADES.runner-check":                            "the check loop's liveness heartbeat probes it",
		"$JS.API.CONSUMER.MSG.NEXT.PLEIADES.runner-check":                        "pulls checks",
		"$JS.ACK.PLEIADES.runner-check.1.2.3.4.5.6":                              "settles a check",
		topology.DeadLetterSubject(topology.CheckSubject("d")):                   "republishes a check's dead letter after MaxDeliver",
		topology.MeshRenewSubject():                                              "asks for a fresh credential before this one lapses, or goes silently idle one window after starting",
	}

	for subject, why := range required {
		if !permits(g.Pub, subject) {
			t.Errorf("FleetRunnerGrant does not permit %q, which the Runner needs because it %s\ngrant was %v", subject, why, g.Pub)
		}
	}
}

// TestControllerGrantCoversEveryEnumeratedOperation is the assertion that
// did not exist at all until Phase 101c, which is why ControllerGrant
// shipped with a stream permission that could never match: it used `>` in
// a non-final token ("$JS.API.STREAM.>.PLEIADES"), so the Controller had
// no usable stream right and would have died at startup on a real
// operator-mode broker.
func TestControllerGrantCoversEveryEnumeratedOperation(t *testing.T) {
	g := meshid.ControllerGrant("controller-1")

	required := map[string]string{
		topology.DispatchSubject("device-1"):                             "fans a job out to one device",
		topology.CheckSubject("device-1"):                                "fans a check out to one device",
		topology.JobRequestedSubject():                                   "publishes the launch itself",
		topology.DeadLetterSubject(topology.JobRequestedSubject()):       "dead-letters a job.requested that failed MaxDeliver times",
		topology.DeadLetterSubject(topology.JournalSubject("job-1")):     "dead-letters a run journal batch that failed MaxDeliver times",
		"$KV.Pleiades_Locks.device-1":                                    "holds the scheduler and leader election leases",
		"$JS.API.INFO":                                                   "AccountInfo when preparing a bucket",
		"$JS.API.STREAM.INFO.PLEIADES":                                   "reads the stream before provisioning",
		"$JS.API.STREAM.CREATE.PLEIADES":                                 "creates the stream, as the only StreamProvisioner",
		"_INBOX.abc123.1":                                                "answers a renewal request, which is a publish to the requester's own reply inbox rather than to any pleiades subject",
		"$JS.API.STREAM.UPDATE.PLEIADES":                                 "reshapes the stream when the outage budget changes",
		"$JS.API.STREAM.INFO.KV_Pleiades_Locks":                          "binds the lock bucket",
		"$JS.API.STREAM.CREATE.KV_Pleiades_Locks":                        "creates the lock bucket",
		"$JS.API.CONSUMER.CREATE.PLEIADES.abc123.pleiades.jobs.logs.tok": "the SSE log viewer creates a server-named ephemeral consumer",
		"$JS.API.CONSUMER.INFO.PLEIADES.abc123":                          "and probes it",
		"$JS.API.CONSUMER.MSG.NEXT.PLEIADES.abc123":                      "and pulls from it",
		"$JS.API.CONSUMER.DELETE.PLEIADES.abc123":                        "and deletes it when the viewer disconnects",
		topology.ControlSubject("job-1"):                                 "signals a cancel to whichever Runner is executing that job",
	}

	for subject, why := range required {
		if !permits(g.Pub, subject) {
			t.Errorf("ControllerGrant does not permit %q, which the Controller needs because it %s\ngrant was %v", subject, why, g.Pub)
		}
	}
}

// TestFleetRunnerGrantCoversEverySubscription is the SUBSCRIBE half, and
// it did not need to exist until job cancel: before it, a Runner
// subscribed to nothing but its own reply inbox.
//
// It is separate from the publish table above rather than folded into it,
// because the two permission sets are genuinely different lists and NATS
// checks them separately. Getting a subscribe wrong is also the quieter
// failure of the two: a denied publish comes back to the publisher, while
// a denied subscription produces no error anywhere at all. A Runner
// missing this entry would connect, pull work, execute normally, and
// simply never stop when somebody cancelled a job, with every log line
// and every in-process test still reporting success.
func TestFleetRunnerGrantCoversEverySubscription(t *testing.T) {
	g := meshid.FleetRunnerGrant("runner-1")

	required := map[string]string{
		"_INBOX.abc.123":                 "every JetStream reply, including the dispatch it pulls, arrives on an inbox",
		topology.ControlSubject("job-1"): "listens for a cancel of the one job it is currently executing",
	}

	for subject, why := range required {
		if !permits(g.Sub, subject) {
			t.Errorf("FleetRunnerGrant does not permit SUBSCRIBING to %q, which the Runner needs because it %s\ngrant was %v", subject, why, g.Sub)
		}
	}
}

// TestRunnerCannotPublishACancel proves the control channel only runs one
// way.
//
// The Runner's subscribe grant names the whole control space, because a
// grant cannot know which job that Runner will be given. Its publish grant
// must not: a Runner that could publish on this subject could stop any job
// anywhere in the fleet, which is a privilege nothing in its work needs
// and a real escalation from a single compromised worker.
func TestRunnerCannotPublishACancel(t *testing.T) {
	g := meshid.FleetRunnerGrant("runner-1")

	if permits(g.Pub, topology.ControlSubject("job-1")) {
		t.Errorf("FleetRunnerGrant permits PUBLISHING a cancel, so one Runner could stop every job in the fleet\ngrant was %v", g.Pub)
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

// TestAccountKindsDifferOnlyInJetStream pins the distinction Phase 101c
// had to introduce, and pins it without Docker so the reason is legible
// from the unit suite rather than only from a container gate.
//
// Both facts were measured against a real nats-server 2.14.4 and each is
// fatal in its own direction. An application account WITHOUT JetStream in
// its claims authenticates perfectly and then fails every operation this
// platform performs with "jetstream not enabled for account". A system
// account WITH JetStream stops the server booting outright: "Not allowed
// to enable JetStream on the system account". The two constructors exist
// so that difference is stated at the call site.
func TestAccountKindsDifferOnlyInJetStream(t *testing.T) {
	op, err := meshid.NewOperator("kinds-op")
	if err != nil {
		t.Fatalf("minting the operator: %v", err)
	}

	app, err := meshid.NewAccount(op, "app")
	if err != nil {
		t.Fatalf("minting the application account: %v", err)
	}
	appClaims, err := jwt.DecodeAccountClaims(app.JWT)
	if err != nil {
		t.Fatalf("decoding the application account jwt: %v", err)
	}
	if !appClaims.Limits.IsJSEnabled() {
		t.Error("NewAccount produced an account with JetStream disabled; every stream, bucket and consumer this platform uses would be refused with \"jetstream not enabled for account\"")
	}

	sys, err := meshid.NewSystemAccount(op, "SYS")
	if err != nil {
		t.Fatalf("minting the system account: %v", err)
	}
	sysClaims, err := jwt.DecodeAccountClaims(sys.JWT)
	if err != nil {
		t.Fatalf("decoding the system account jwt: %v", err)
	}
	if sysClaims.Limits.IsJSEnabled() {
		t.Error("NewSystemAccount produced an account with JetStream enabled; nats-server refuses to start at all with \"Not allowed to enable JetStream on the system account\"")
	}

	// Both are still real accounts under the same operator, which is the
	// half a JetStream-only assertion would miss.
	if sys.Subject == app.Subject {
		t.Error("the system and application accounts share a subject")
	}
	for _, a := range []*meshid.Account{app, sys} {
		if len(a.SigningKeySeed) == 0 {
			t.Errorf("account %q has no signing key seed, so it could never rotate", a.Subject)
		}
	}
}

// TestOperatorSeedIsUsableOffline covers the one accessor that exists
// purely for a bootstrap command: the operator seed has to round-trip into
// a key pair that can sign, or the offline half of the trust root cannot
// be stored anywhere.
func TestOperatorSeedIsUsableOffline(t *testing.T) {
	op, err := meshid.NewOperator("seed-op")
	if err != nil {
		t.Fatalf("minting the operator: %v", err)
	}
	seed, err := op.Seed()
	if err != nil {
		t.Fatalf("reading the operator seed: %v", err)
	}
	kp, err := nkeys.FromSeed(seed)
	if err != nil {
		t.Fatalf("the operator seed does not parse back into a key pair: %v", err)
	}
	pub, err := kp.PublicKey()
	if err != nil {
		t.Fatalf("reading the public key back: %v", err)
	}
	if pub != op.Subject {
		t.Fatalf("the seed round-tripped to %q, want the operator's own subject %q", pub, op.Subject)
	}
}

// TestTheFleetWindowOutlivesTheEdgeWindow pins the relationship between
// the two default expiries, because the number that matters is not either
// one on its own.
//
// A credential that lapses does not degrade, it evicts: the broker drops
// the connection and nats.go stops reconnecting after the same
// authentication error twice. So the fleet window has to be the one that
// survives a deployment nothing renews, and the edge window has to be the
// short one, since that is the case a short window is FOR. Getting these
// the wrong way round produces a fleet that goes quiet on a timer and an
// engagement credential that stays useful for a month, which is exactly
// backwards on both counts.
func TestTheFleetWindowOutlivesTheEdgeWindow(t *testing.T) {
	if meshid.DefaultUserExpiry <= 0 {
		t.Error("DefaultUserExpiry must be positive; Issue refuses a non-positive expiry outright")
	}
	if meshid.DefaultFleetExpiry <= 0 {
		t.Error("DefaultFleetExpiry must be positive; Issue refuses a non-positive expiry outright")
	}
	if meshid.DefaultFleetExpiry <= meshid.DefaultUserExpiry {
		t.Errorf("DefaultFleetExpiry (%v) is not longer than DefaultUserExpiry (%v); a fleet Runner would go silently idle one edge window after starting",
			meshid.DefaultFleetExpiry, meshid.DefaultUserExpiry)
	}
}

// TestBothDefaultExpiriesAreAcceptedByIssue is the guard that neither
// constant can be set to something the minting path itself refuses.
//
// Issue rejects a non-positive expiry, and a constant is exactly the kind
// of value that gets edited without anyone re-running the one call that
// would notice.
func TestBothDefaultExpiriesAreAcceptedByIssue(t *testing.T) {
	op, err := meshid.NewOperator("expiry-defaults")
	if err != nil {
		t.Fatalf("minting the operator: %v", err)
	}
	acct, err := meshid.NewAccount(op, "PLEIADES")
	if err != nil {
		t.Fatalf("minting the account: %v", err)
	}
	issuer, err := meshid.NewIssuer(acct.Subject, acct.SigningKeySeed)
	if err != nil {
		t.Fatalf("building the issuer: %v", err)
	}

	for name, window := range map[string]time.Duration{
		"DefaultUserExpiry":  meshid.DefaultUserExpiry,
		"DefaultFleetExpiry": meshid.DefaultFleetExpiry,
	} {
		t.Run(name, func(t *testing.T) {
			cred, err := issuer.Issue(meshid.FleetRunnerGrant("defaults"), window)
			if err != nil {
				t.Fatalf("Issue with %s (%v): %v", name, window, err)
			}
			if !cred.Expires.After(time.Now()) {
				t.Errorf("%s produced a credential that is already expired at %v", name, cred.Expires)
			}
		})
	}
}

// TestControllerGrantCoversEverySubscription is the Controller's half of
// the renewal exchange.
//
// It had no subscription table at all before Phase 101c, for the same
// reason it had no publish table until that stage: the Controller's grant
// was written once and never asserted against, which is how it shipped
// with a stream permission that could never match.
func TestControllerGrantCoversEverySubscription(t *testing.T) {
	g := meshid.ControllerGrant("controller-1")

	required := map[string]string{
		"_INBOX.abc.123":            "every JetStream reply it waits on arrives on an inbox",
		topology.MeshRenewSubject(): "serves credential renewal, which is the only thing standing between a fleet Runner and going silently idle one credential window after it starts",
	}

	for subject, why := range required {
		if !permits(g.Sub, subject) {
			t.Errorf("ControllerGrant does not permit SUBSCRIBING to %q, which the Controller needs because it %s\ngrant was %v", subject, why, g.Sub)
		}
	}
}

// TestRunnerCannotAnswerARenewalRequest proves the renewal exchange only
// runs one way, the same shape TestRunnerCannotPublishACancel proves for
// cancellation.
//
// A Runner must be able to ASK for a credential and must not be able to
// hear other Runners asking. A Runner that could subscribe here could
// answer a request before the Controller did, and while it could not mint
// anything the broker would accept, since it holds no signing key, it
// could hand every renewing Runner in the fleet a credential that fails.
// That turns one compromised worker into a fleet-wide outage on a timer,
// which is a strictly larger blast radius than the worker itself.
func TestRunnerCannotAnswerARenewalRequest(t *testing.T) {
	g := meshid.FleetRunnerGrant("runner-1")

	if !permits(g.Pub, topology.MeshRenewSubject()) {
		t.Fatalf("FleetRunnerGrant cannot publish %q, so a Runner could never renew and this test's negative half is meaningless", topology.MeshRenewSubject())
	}
	if permits(g.Sub, topology.MeshRenewSubject()) {
		t.Errorf("FleetRunnerGrant permits SUBSCRIBING to %q; a compromised Runner could answer the fleet's renewals with credentials nothing accepts\ngrant was %v",
			topology.MeshRenewSubject(), g.Sub)
	}
}
