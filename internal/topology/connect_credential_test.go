package topology_test

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	"github.com/nats-io/nkeys"
)

// TestWithCredentialsCarriesTheBytesUntouched pins the option's whole
// contract: it takes the credential as BYTES and holds exactly those,
// never a path.
//
// The distinction is the security property, not a style choice. A path
// would mean the key material has to exist as a file for the lifetime of
// the process, on a Runner that already refuses to put a secret in argv or
// the environment. The bytes come from the Controller and stay in memory.
func TestWithCredentialsCarriesTheBytesUntouched(t *testing.T) {
	creds := []byte("-----BEGIN NATS USER JWT-----\nabc\n------END NATS USER JWT------\n")
	got := topology.SettingsForTest(topology.WithCredentials(creds))
	if string(got) != string(creds) {
		t.Fatalf("WithCredentials carried %q, want the bytes it was given, %q", got, creds)
	}
	if topology.SettingsForTest() != nil {
		t.Fatal("a dial with no options carried a credential")
	}
}

// TestCredentialOptionRejectsAMalformedCredential covers the error
// branches the container gate cannot reach: it only ever passes a
// well-formed credential, because a malformed one never gets as far as a
// broker.
//
// Both failures have to be clean Go errors naming what could not be read.
// The alternative, which is what an unchecked parse gives, is a dial that
// succeeds locally and fails as an authorization error against the server,
// where the operator sees a permissions problem rather than a corrupt
// credential file.
func TestCredentialOptionRejectsAMalformedCredential(t *testing.T) {
	for _, tc := range []struct {
		name  string
		creds []byte
		want  string
	}{
		// Both of these fail on the SEED rather than on the JWT, which
		// is worth pinning because it is the opposite of what the
		// function's own order of operations suggests:
		// nkeys.ParseDecoratedJWT does not validate anything, it only
		// strips the armor around a token, so it returns garbage
		// happily. The seed parse is the only reader that actually
		// refuses a malformed credential, and it is therefore the one
		// standing between a corrupt file and a confusing authorization
		// failure against the broker.
		{"empty", nil, "key"},
		{"not a credential at all", []byte("hello"), "key"},
		{
			name: "a jwt with no seed",
			creds: []byte("-----BEGIN NATS USER JWT-----\n" +
				"eyJ0eXAiOiJKV1QiLCJhbGciOiJlZDI1NTE5LW5rZXkifQ.e30.x\n" +
				"------END NATS USER JWT------\n"),
			want: "key",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opt, err := topology.CredentialOptionForTest(tc.creds)
			if err == nil {
				t.Fatalf("credentialOption(%q) returned an option and no error; a malformed credential must be refused before any dial", tc.creds)
			}
			if opt != nil {
				t.Error("credentialOption returned both an option and an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("credentialOption error = %v, want it to name the %s it could not read", err, tc.want)
			}
			// The one thing the error must never carry is the credential.
			if len(tc.creds) > 0 && strings.Contains(err.Error(), string(tc.creds)) {
				t.Errorf("credentialOption error = %v, which quotes the credential body back", err)
			}
		})
	}
}

// TestFleetWildcardSubjectsAreRealWildcards guards the mistake this
// package already made once. The per-job subject builders SANITIZE their
// argument, so LogSubject(">") returns a hashed literal token rather than
// a wildcard, and a grant built that way permits nothing. LogSubjectAll
// and ResultSubjectAll exist precisely so a caller never has to reach for
// that, and they were added with no direct test.
func TestFleetWildcardSubjectsAreRealWildcards(t *testing.T) {
	for _, tc := range []struct {
		name string
		got  string
		want string
	}{
		{"logs", topology.LogSubjectAll(), "pleiades.jobs.logs.>"},
		{"results", topology.ResultSubjectAll(), "pleiades.jobs.results.>"},
		{"dispatch", topology.DispatchSubjectAll(), "pleiades.jobs.dispatch.>"},
	} {
		if tc.got != tc.want {
			t.Errorf("%s wildcard = %q, want %q", tc.name, tc.got, tc.want)
		}
		if !strings.HasSuffix(tc.got, ".>") {
			t.Errorf("%s wildcard = %q, which is not a wildcard at all", tc.name, tc.got)
		}
	}
	// And the sanitizing builders must NOT be usable this way, which is
	// the other half of the same rule.
	if strings.HasSuffix(topology.LogSubject(">"), ".>") {
		t.Error("LogSubject(\">\") produced a real wildcard; the builders are supposed to sanitize, and a grant built from one would over-permit")
	}
}

// TestCredentialOptionAcceptsAWellFormedCredential covers the success
// path, which the container gate exercises end to end but which this
// package cannot otherwise reach: internal/meshid is the only thing that
// mints a credential, and it imports this package, so this package can
// never import it back.
//
// A credential is assembled here directly from nkeys instead. That works
// because of the finding pinned above: ParseDecoratedJWT does not
// validate, it only strips the armor, so the token half can be any text
// while the seed half has to be a genuine user seed.
func TestCredentialOptionAcceptsAWellFormedCredential(t *testing.T) {
	kp, err := nkeys.CreateUser()
	if err != nil {
		t.Fatalf("creating a user key: %v", err)
	}
	seed, err := kp.Seed()
	if err != nil {
		t.Fatalf("reading the seed: %v", err)
	}
	creds := []byte("-----BEGIN NATS USER JWT-----\nnot.a.real.token\n------END NATS USER JWT------\n\n" +
		"-----BEGIN USER NKEY SEED-----\n" + string(seed) + "\n------END USER NKEY SEED------\n")

	opt, err := topology.CredentialOptionForTest(creds)
	if err != nil {
		t.Fatalf("a well-formed credential was refused: %v", err)
	}
	if opt == nil {
		t.Fatal("credentialOption returned no option and no error")
	}
}

// TestConnectRefusesAMalformedCredentialBeforeDialling proves the refusal
// happens at the right MOMENT, not just that it happens.
//
// Connect validates the URL, then builds the credential option, then
// dials. A malformed credential must fail in the middle step, before any
// socket is opened, or a Runner with a corrupt credential file spends
// ConnectWaitTimeout retrying against a broker that was never going to
// accept it and reports a connectivity problem instead of a file problem.
// The URL here points at a port nothing is listening on, so if the
// refusal did NOT happen first, this test would take the full connect
// timeout rather than returning immediately.
func TestConnectRefusesAMalformedCredentialBeforeDialling(t *testing.T) {
	start := time.Now()
	conn, err := topology.Connect(context.Background(), "nats://127.0.0.1:1",
		slog.New(slog.NewTextHandler(io.Discard, nil)), "credential-test",
		topology.WithCredentials([]byte("not a credential")))
	if err == nil {
		conn.Close()
		t.Fatal("Connect accepted a malformed credential")
	}
	if !strings.Contains(err.Error(), "credential") {
		t.Errorf("Connect error = %v, want it to name the credential rather than the connection", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("Connect took %v to refuse a malformed credential, which means it dialled first", elapsed)
	}
}
