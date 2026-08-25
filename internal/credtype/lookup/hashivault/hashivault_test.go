package hashivault_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype/lookup/hashivault"
)

// The Vault source against a real HTTP server that answers like Vault.
//
// What this suite is and is not. It is a real net/http client speaking real
// HTTP to a real server over a real socket, which is what makes the URL
// this package builds, the headers it sends and the statuses it classifies
// genuinely observable. It is NOT the RULE 0 evidence that this reads a
// real Vault: that is release_gate_container_test.go, against the actual
// hashicorp/vault image, and the two exist for different reasons. This one
// can express a malformed response and a 403 cheaply; that one proves the
// path and the JSON shape are the ones Vault actually uses.

// vaultStub is a scripted Vault. It records what it was asked so the tests
// can assert on the request rather than only on the answer.
type vaultStub struct {
	*httptest.Server

	// lastPath, lastQuery and lastHeaders are the most recent request.
	lastPath    string
	lastQuery   string
	lastToken   string
	lastNS      string
	status      int
	body        string
	requestSeen int
}

// newVaultStub starts a server answering every read with body.
func newVaultStub(t *testing.T, body string) *vaultStub {
	t.Helper()
	s := &vaultStub{status: http.StatusOK, body: body}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.lastPath = r.URL.Path
		s.lastQuery = r.URL.RawQuery
		s.lastToken = r.Header.Get("X-Vault-Token")
		s.lastNS = r.Header.Get("X-Vault-Namespace")
		s.requestSeen++
		w.WriteHeader(s.status)
		_, _ = w.Write([]byte(s.body))
	}))
	t.Cleanup(s.Close)
	return s
}

// kv2 renders the response shape a v2 key/value read returns.
func kv2(fields map[string]any) string {
	b, _ := json.Marshal(map[string]any{"data": map[string]any{"data": fields}})
	return string(b)
}

// kv1 renders the response shape a v1 read returns.
func kv1(fields map[string]any) string {
	b, _ := json.Marshal(map[string]any{"data": fields})
	return string(b)
}

// sourceFor builds a Lookup over the stub, with the given extra inputs.
func sourceFor(t *testing.T, stub *vaultStub, extra map[string]string) credtype.Lookup {
	t.Helper()
	inputs := map[string]string{"url": stub.URL, "token": "s.roottoken"}
	for k, v := range extra {
		inputs[k] = v
	}
	lookup, err := hashivault.Factory{}.New(inputs)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return lookup
}

// refFor builds a reference from binding metadata.
func refFor(t *testing.T, metadata map[string]string) string {
	t.Helper()
	ref, err := hashivault.Factory{}.Reference(metadata)
	if err != nil {
		t.Fatalf("Reference() error = %v", err)
	}
	return ref
}

// TestAV2ReadHitsTheDataPathAndReturnsTheKey is the base case, and it
// asserts the REQUEST as well as the answer: the /data/ segment is the one
// behavioural difference between the two engine versions.
func TestAV2ReadHitsTheDataPathAndReturnsTheKey(t *testing.T) {
	stub := newVaultStub(t, kv2(map[string]any{"token": "the-secret", "other": "not-this"}))
	lookup := sourceFor(t, stub, nil)

	got, err := lookup.Resolve(context.Background(), refFor(t, map[string]string{
		"secret_backend": "kv", "secret_path": "prod/api", "secret_key": "token",
	}))
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got != "the-secret" {
		t.Errorf("Resolve() = %q, want the value under the named key", got)
	}
	if want := "/v1/kv/data/prod/api"; stub.lastPath != want {
		t.Errorf("requested %q, want %q", stub.lastPath, want)
	}
	if stub.lastToken != "s.roottoken" {
		t.Errorf("X-Vault-Token = %q, want the source credential's token", stub.lastToken)
	}
	if stub.lastNS != "" {
		t.Errorf("X-Vault-Namespace = %q, want none when the source sets none", stub.lastNS)
	}
}

// TestAV1ReadOmitsTheDataSegment is the other half. Reading a v1 mount
// through a v2 path is a 404 against a real Vault, so getting this wrong
// presents as "the secret is missing" rather than as a version mistake.
func TestAV1ReadOmitsTheDataSegment(t *testing.T) {
	stub := newVaultStub(t, kv1(map[string]any{"token": "v1-secret"}))
	lookup := sourceFor(t, stub, map[string]string{"api_version": "v1"})

	got, err := lookup.Resolve(context.Background(), refFor(t, map[string]string{
		"secret_backend": "kv", "secret_path": "prod/api", "secret_key": "token",
	}))
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got != "v1-secret" {
		t.Errorf("Resolve() = %q, want the v1 value", got)
	}
	if want := "/v1/kv/prod/api"; stub.lastPath != want {
		t.Errorf("requested %q, want %q with no data segment", stub.lastPath, want)
	}
}

// TestTheDefaultMountAndNamespaceHeader covers the two inputs that change
// the request without changing the reference.
func TestTheDefaultMountAndNamespaceHeader(t *testing.T) {
	stub := newVaultStub(t, kv2(map[string]any{"token": "x"}))
	lookup := sourceFor(t, stub, map[string]string{"namespace": "team-a"})

	if _, err := lookup.Resolve(context.Background(), refFor(t, map[string]string{
		"secret_path": "prod/api", "secret_key": "token",
	})); err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	// A binding naming no mount reads the mount a default Vault creates,
	// rather than failing on the common case.
	if want := "/v1/secret/data/prod/api"; stub.lastPath != want {
		t.Errorf("requested %q, want the default mount %q", stub.lastPath, want)
	}
	if stub.lastNS != "team-a" {
		t.Errorf("X-Vault-Namespace = %q, want the source's namespace", stub.lastNS)
	}
}

// TestAVersionedReadAsksForThatVersion covers the one query parameter, and
// its refusal on a v1 mount, which has no versions at all.
func TestAVersionedReadAsksForThatVersion(t *testing.T) {
	stub := newVaultStub(t, kv2(map[string]any{"token": "old"}))
	lookup := sourceFor(t, stub, nil)

	if _, err := lookup.Resolve(context.Background(), refFor(t, map[string]string{
		"secret_path": "prod/api", "secret_key": "token", "secret_version": "3",
	})); err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if stub.lastQuery != "version=3" {
		t.Errorf("query = %q, want version=3", stub.lastQuery)
	}

	v1 := sourceFor(t, stub, map[string]string{"api_version": "v1"})
	_, err := v1.Resolve(context.Background(), refFor(t, map[string]string{
		"secret_path": "prod/api", "secret_key": "token", "secret_version": "3",
	}))
	if !errors.Is(err, credtype.ErrLookupReference) {
		t.Fatalf("a versioned read of a v1 mount error = %v, want a reference refusal", err)
	}
}

// TestStatusesAreClassifiedSeparately is what decides where an operator
// looks. One error for all three would send them to the wrong place.
func TestStatusesAreClassifiedSeparately(t *testing.T) {
	tests := []struct {
		name   string
		status int
		want   string
	}{
		{name: "forbidden names the token", status: http.StatusForbidden, want: "not permitted"},
		{name: "unauthorized names the token", status: http.StatusUnauthorized, want: "not permitted"},
		{name: "not found names the path", status: http.StatusNotFound, want: "nothing is stored"},
		{name: "a server error names the status", status: http.StatusBadGateway, want: "answered 502"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stub := newVaultStub(t, "{}")
			stub.status = tt.status
			lookup := sourceFor(t, stub, nil)

			_, err := lookup.Resolve(context.Background(), refFor(t, map[string]string{
				"secret_path": "prod/api", "secret_key": "token",
			}))
			if !errors.Is(err, credtype.ErrLookupReference) {
				t.Fatalf("Resolve() error = %v, want a reference refusal", err)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Resolve() error = %v, want it to say %q", err, tt.want)
			}
		})
	}
}

// TestNoErrorEverCarriesTheTokenOrTheValue is the property that matters
// most in this file, because these errors reach a job record.
func TestNoErrorEverCarriesTheTokenOrTheValue(t *testing.T) {
	const token = "s.a-very-secret-token"
	const value = "the-secret-value"

	cases := []struct {
		name     string
		body     string
		status   int
		metadata map[string]string
	}{
		{name: "a forbidden read", body: "{}", status: http.StatusForbidden},
		{name: "a missing key", body: kv2(map[string]any{"other": value}), status: http.StatusOK},
		{name: "an object value", body: kv2(map[string]any{"token": map[string]any{"a": value}}), status: http.StatusOK},
		{name: "a malformed body", body: "not json at all", status: http.StatusOK},
		{name: "a v1 body read as v2", body: kv1(map[string]any{"token": value}), status: http.StatusOK},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub := newVaultStub(t, tc.body)
			stub.status = tc.status
			lookup, err := hashivault.Factory{}.New(map[string]string{"url": stub.URL, "token": token})
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}

			_, err = lookup.Resolve(context.Background(), refFor(t, map[string]string{
				"secret_path": "prod/api", "secret_key": "token",
			}))
			if err == nil {
				t.Fatal("Resolve() succeeded where the case expects a failure")
			}
			if strings.Contains(err.Error(), token) {
				t.Errorf("the error carries the source credential's token: %v", err)
			}
			if strings.Contains(err.Error(), value) {
				t.Errorf("the error carries the secret value: %v", err)
			}
		})
	}
}

// TestScalarValuesAreRenderedAndStructuresRefused covers what an injector
// can actually be given.
func TestScalarValuesAreRenderedAndStructuresRefused(t *testing.T) {
	tests := []struct {
		name    string
		stored  any
		want    string
		wantErr bool
	}{
		{name: "a string", stored: "plain", want: "plain"},
		{name: "an empty string is refused upstream", stored: "", want: ""},
		{name: "a number", stored: 8200, want: "8200"},
		{name: "a boolean", stored: true, want: "true"},
		{name: "an object", stored: map[string]any{"a": 1}, wantErr: true},
		{name: "a list", stored: []any{1, 2}, wantErr: true},
		{name: "a null", stored: nil, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stub := newVaultStub(t, kv2(map[string]any{"token": tt.stored}))
			lookup := sourceFor(t, stub, nil)

			got, err := lookup.Resolve(context.Background(), refFor(t, map[string]string{
				"secret_path": "p", "secret_key": "token",
			}))
			if tt.wantErr {
				if !errors.Is(err, credtype.ErrLookupReference) {
					t.Fatalf("Resolve() error = %v, want a reference refusal", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Resolve() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("Resolve() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestAnOversizedResponseIsRefusedRatherThanRead covers the body bound. The
// response comes from a server named in a database row, so its size is not
// this platform's to trust.
func TestAnOversizedResponseIsRefusedRatherThanRead(t *testing.T) {
	stub := newVaultStub(t, kv2(map[string]any{"token": strings.Repeat("A", 2<<20)}))
	lookup := sourceFor(t, stub, nil)

	_, err := lookup.Resolve(context.Background(), refFor(t, map[string]string{
		"secret_path": "p", "secret_key": "token",
	}))
	if !errors.Is(err, credtype.ErrLookupReference) {
		t.Fatalf("Resolve() error = %v, want a reference refusal", err)
	}
	if !strings.Contains(err.Error(), "larger than") {
		t.Errorf("Resolve() error = %v, want it to name the bound", err)
	}
}

// TestTheFactoryRefusesAnUnusableSourceCredential covers every way a source
// credential can be wrong, reported to whoever wrote it rather than to
// whoever launched the job.
func TestTheFactoryRefusesAnUnusableSourceCredential(t *testing.T) {
	const token = "s.secret"

	tests := []struct {
		name   string
		inputs map[string]string
	}{
		{name: "no url", inputs: map[string]string{"token": token}},
		{name: "an unparseable url", inputs: map[string]string{"url": "http://[::1", "token": token}},
		{name: "a file scheme", inputs: map[string]string{"url": "file:///etc/passwd", "token": token}},
		{name: "no host", inputs: map[string]string{"url": "https://", "token": token}},
		{name: "no token", inputs: map[string]string{"url": "https://vault.example.com"}},
		{name: "an unknown api version", inputs: map[string]string{"url": "https://v.example.com", "token": token, "api_version": "v3"}},
		{name: "a cacert that is not PEM", inputs: map[string]string{"url": "https://v.example.com", "token": token, "cacert": "not a certificate"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := hashivault.Factory{}.New(tt.inputs)
			if err == nil {
				t.Fatal("New() accepted an unusable source credential")
			}
			if strings.Contains(err.Error(), token) {
				t.Errorf("the error carries the token: %v", err)
			}
		})
	}
}

// TestAValidSourceIsAccepted is the positive control for the table above. A
// factory that refused everything would pass it.
func TestAValidSourceIsAccepted(t *testing.T) {
	lookup, err := hashivault.Factory{}.New(map[string]string{
		"url": "https://vault.example.com:8200", "token": "s.t",
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if lookup.Name() != "hashivault_kv" {
		t.Errorf("Name() = %q, want the AWX namespace", lookup.Name())
	}
}

// TestTheFactoryIsUsableAsThePortItClaims is the compile-time and
// behavioural check that this really is a credtype.LookupFactory, which is
// what lets a deployment register it without this package being known to
// the resolver.
func TestTheFactoryIsUsableAsThePortItClaims(t *testing.T) {
	var f credtype.LookupFactory = hashivault.Factory{}
	if f.Namespace() != "hashivault_kv" {
		t.Errorf("Namespace() = %q, want the AWX namespace", f.Namespace())
	}
	lookups, err := credtype.NewLookupsWith(nil, []credtype.LookupFactory{f})
	if err != nil {
		t.Fatalf("NewLookupsWith() error = %v", err)
	}
	if _, ok := lookups.Factory("hashivault_kv"); !ok {
		t.Error("the factory is not selectable by its own namespace")
	}
}

// TestNamingItByReferenceStringSaysWhyThatCannotWork covers the error an
// AWX import produces if it carries the string form: this source IS
// implemented, and reporting it as unimplemented would send an operator to
// wait for something they already have.
func TestNamingItByReferenceStringSaysWhyThatCannotWork(t *testing.T) {
	lookups, err := credtype.NewLookupsWith(nil, []credtype.LookupFactory{hashivault.Factory{}})
	if err != nil {
		t.Fatalf("NewLookupsWith() error = %v", err)
	}

	_, err = lookups.Resolve(context.Background(), "api_token", "hashivault_kv:secret/data/prod")
	if !errors.Is(err, credtype.ErrLookupRowOnly) {
		t.Fatalf("Resolve() error = %v, want a row-only refusal", err)
	}
	if errors.Is(err, credtype.ErrLookupNotImplemented) {
		t.Error("a built source was reported as not implemented")
	}
}

// TestBindingMetadataIsRefusedBeforeAnyRequest covers everything wrong with
// a binding, none of which should cost a network round trip.
func TestBindingMetadataIsRefusedBeforeAnyRequest(t *testing.T) {
	tests := []struct {
		name     string
		metadata map[string]string
	}{
		{name: "no path", metadata: map[string]string{"secret_key": "token"}},
		{name: "no key", metadata: map[string]string{"secret_path": "p"}},
		{name: "a traversal in the path", metadata: map[string]string{"secret_path": "prod/../../sys/mounts", "secret_key": "token"}},
		{name: "a dot element", metadata: map[string]string{"secret_path": "prod/./api", "secret_key": "token"}},
		{name: "an empty path element", metadata: map[string]string{"secret_path": "prod//api", "secret_key": "token"}},
		{name: "a slash in the mount", metadata: map[string]string{"secret_backend": "kv/extra", "secret_path": "p", "secret_key": "token"}},
		{name: "a null byte", metadata: map[string]string{"secret_path": "p\x00q", "secret_key": "token"}},
		{name: "a version that is not a number", metadata: map[string]string{"secret_path": "p", "secret_key": "token", "secret_version": "latest"}},
		{name: "a zero version", metadata: map[string]string{"secret_path": "p", "secret_key": "token", "secret_version": "0"}},
		{name: "an absurd version", metadata: map[string]string{"secret_path": "p", "secret_key": "token", "secret_version": "99999999999"}},
	}

	stub := newVaultStub(t, kv2(map[string]any{"token": "x"}))
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := stub.requestSeen
			if _, err := (hashivault.Factory{}).Reference(tt.metadata); !errors.Is(err, credtype.ErrLookupReference) {
				t.Fatalf("Reference() error = %v, want a reference refusal", err)
			}
			if stub.requestSeen != before {
				t.Error("a malformed binding reached the network")
			}
		})
	}
}

// TestAReferenceThisSourceDidNotWriteIsRefused covers the decode side. A
// row-backed source's Resolve is only ever handed what Reference produced,
// so anything else is a wiring error rather than data to be tolerant of.
func TestAReferenceThisSourceDidNotWriteIsRefused(t *testing.T) {
	stub := newVaultStub(t, kv2(map[string]any{"token": "x"}))
	lookup := sourceFor(t, stub, nil)

	for _, raw := range []string{
		"",
		"secret/data/prod#token",
		`{"mount":"kv","path":"p","key":"k","surprise":1}`,
		`{"mount":"kv","path":"","key":"k"}`,
		`{"mount":"","path":"p","key":"k"}`,
		`{"mount":"kv","path":"../sys","key":"k"}`,
	} {
		t.Run(fmt.Sprintf("%q", raw), func(t *testing.T) {
			if _, err := lookup.Resolve(context.Background(), raw); !errors.Is(err, credtype.ErrLookupReference) {
				t.Fatalf("Resolve(%q) error = %v, want a reference refusal", raw, err)
			}
		})
	}
}

// TestAPrivateChainIsVerifiedRatherThanTrusted is the TLS half, and the
// negative control is the deliverable: the SAME server is refused without
// its certificate authority and accepted with it, so the test proves
// verification is happening rather than that a request succeeded.
func TestAPrivateChainIsVerifiedRatherThanTrusted(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(kv2(map[string]any{"token": "over-tls"})))
	}))
	t.Cleanup(server.Close)

	ref := refFor(t, map[string]string{"secret_path": "p", "secret_key": "token"})

	// Without the authority, the chain does not verify and the read fails.
	withoutCA, err := hashivault.Factory{}.New(map[string]string{"url": server.URL, "token": "s.t"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := withoutCA.Resolve(context.Background(), ref); err == nil {
		t.Fatal("a server with an unknown certificate authority was trusted")
	}

	// With it, the same read succeeds. This is what makes the refusal above
	// evidence of verification rather than of an unrelated failure.
	withCA, err := hashivault.Factory{}.New(map[string]string{
		"url": server.URL, "token": "s.t", "cacert": certificatePEM(t, server),
	})
	if err != nil {
		t.Fatalf("New() with a CA error = %v", err)
	}
	got, err := withCA.Resolve(context.Background(), ref)
	if err != nil {
		t.Fatalf("Resolve() over TLS error = %v", err)
	}
	if got != "over-tls" {
		t.Errorf("Resolve() = %q, want the value read over a verified chain", got)
	}
}

// certificatePEM renders the test server's own certificate as PEM, which is
// what an operator would paste into the cacert input.
func certificatePEM(t *testing.T, server *httptest.Server) string {
	t.Helper()
	cert := server.Certificate()
	if cert == nil {
		t.Fatal("the TLS test server has no certificate")
	}
	return string(pemEncode(t, cert.Raw))
}
