// Tests for the transport decision: which of the three arrangements a set of
// environment variables means, and what it refuses to guess at.
//
// These run from inside package main because resolveTLS is unexported, and
// because the decision it makes is not observable from outside without
// standing up a real server. What that decision then LOADS is covered next
// door in servingcert_test.go, and the half that is observable over a real
// listener, namely that the probe and the server agree about the scheme, is
// proven in healthcheck_test.go.
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/tlscert"
)

// clearTLSEnvironment removes every variable resolveTLS reads, so a case
// below describes its whole input rather than inheriting whatever the
// developer running the tests happens to have exported.
func clearTLSEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"TLS_CERT_FILE",
		"TLS_KEY_FILE",
		"PLEIADES_TLS_TERMINATED_UPSTREAM",
		"PLEIADES_TLS_AUTOCERT_DIR",
		autocertHostsVar,
	} {
		t.Setenv(name, "")
	}
}

// TestResolveTLS covers every combination of the transport variables,
// including the three that are deliberately errors.
//
// The error cases are the point of the function, not edge coverage. This
// controller writes a Secure, __Host- prefixed session cookie
// unconditionally, and a browser refuses such a cookie on a plain-HTTP
// origin without saying so, which surfaces as a rejected sign-in for a
// correct password. What changed in Phase 20 is only the case where an
// operator set NOTHING: that used to be a refusal to start, and is now a
// certificate this process writes for itself. Serving plain HTTP still
// requires somebody to state that an ingress terminated TLS.
func TestResolveTLS(t *testing.T) {
	tests := []struct {
		name     string
		cert     string
		key      string
		upstream string
		wantMode tlsMode
		wantErr  []string
	}{
		{
			name:     "a certificate pair terminates TLS here",
			cert:     "/etc/pleiades/tls/cert.pem",
			key:      "/etc/pleiades/tls/key.pem",
			wantMode: tlsModeServe,
		},
		{
			name:     "the explicit upstream statement serves plain HTTP",
			upstream: "1",
			wantMode: tlsModeUpstream,
		},
		{
			// The case this phase changed. An operator who has configured
			// nothing is not asking for plain HTTP; they have not gotten
			// that far, and refusing to start taught them a variable name
			// instead of showing them the product.
			name:     "nothing set provisions a self-signed certificate rather than refusing to start",
			wantMode: tlsModeSelfProvisioned,
		},
		{
			name:    "a certificate without its key is a startup error",
			cert:    "/etc/pleiades/tls/cert.pem",
			wantErr: []string{"TLS_CERT_FILE", "TLS_KEY_FILE"},
		},
		{
			name:    "a key without its certificate is a startup error",
			key:     "/etc/pleiades/tls/key.pem",
			wantErr: []string{"TLS_CERT_FILE", "TLS_KEY_FILE"},
		},
		{
			// Both arrangements at once is two different intentions written
			// down, exactly like DB_DSN and DB_PATH together, and guessing
			// which one an operator meant is how a deployment quietly serves
			// the wrong thing.
			name:     "both arrangements at once is a startup error rather than a silent precedence rule",
			cert:     "/etc/pleiades/tls/cert.pem",
			key:      "/etc/pleiades/tls/key.pem",
			upstream: "1",
			wantErr:  []string{"TLS_CERT_FILE", "PLEIADES_TLS_TERMINATED_UPSTREAM"},
		},
		{
			// A no, spelled the other way. It must not opt in to plain HTTP,
			// and it must not be an error either: an operator who wrote
			// "false" said something perfectly clear.
			name:     "the upstream flag set to a no falls through to self-provisioning",
			upstream: "false",
			wantMode: tlsModeSelfProvisioned,
		},
		{
			// The defect this case exists for. Under a bare `== "1"` check
			// this value was treated as UNSET, so the controller provisioned
			// a certificate and served HTTPS behind an ingress forwarding
			// plain HTTP: a total outage, from an operator who had written
			// their intention down correctly in the wrong spelling.
			name:     "the upstream flag spelled true means what it says",
			upstream: "true",
			wantMode: tlsModeUpstream,
		},
		{
			name:     "the upstream flag spelled yes means what it says",
			upstream: "yes",
			wantMode: tlsModeUpstream,
		},
		{
			name:     "the upstream flag spelled on means what it says",
			upstream: "on",
			wantMode: tlsModeUpstream,
		},
		{
			// Case and surrounding space are not intent. An environment file
			// written by hand has both.
			name:     "the upstream flag is read without regard to case or spacing",
			upstream: "  TRUE  ",
			wantMode: tlsModeUpstream,
		},
		{
			// The other half of the decision: a value that is neither a yes
			// nor a no is refused rather than guessed at, exactly like the
			// half-configured cases above it. This variable decides whether
			// traffic is encrypted, and "it looked like it might mean no" is
			// not a basis for that.
			name:     "an unrecognised value for the upstream flag is a startup error rather than a guess",
			upstream: "maybe",
			wantErr:  []string{"PLEIADES_TLS_TERMINATED_UPSTREAM", "maybe"},
		},
		{
			name:     "a value that only looks like a setting is refused too",
			upstream: "tls",
			wantErr:  []string{"PLEIADES_TLS_TERMINATED_UPSTREAM"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clearTLSEnvironment(t)
			t.Setenv("TLS_CERT_FILE", tc.cert)
			t.Setenv("TLS_KEY_FILE", tc.key)
			t.Setenv("PLEIADES_TLS_TERMINATED_UPSTREAM", tc.upstream)

			got, err := resolveTLS()
			if len(tc.wantErr) > 0 {
				if err == nil {
					t.Fatalf("resolveTLS() = %+v, want an error", got)
				}
				// The message has to name the variables, since that is the
				// only way an operator learns what to set.
				for _, want := range tc.wantErr {
					if !strings.Contains(err.Error(), want) {
						t.Fatalf("the error %q does not name %s", err, want)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveTLS() error = %v", err)
			}
			if got.Mode != tc.wantMode {
				t.Fatalf("resolveTLS() mode = %v, want %v", got.Mode, tc.wantMode)
			}
			if got.Mode == tlsModeServe && (got.CertFile != tc.cert || got.KeyFile != tc.key) {
				t.Fatalf("resolveTLS() = %q/%q, want %q/%q", got.CertFile, got.KeyFile, tc.cert, tc.key)
			}
			// Whether TLS is terminated here is the question every caller
			// asks, and getting it wrong for the new mode would make the
			// container healthcheck probe the wrong scheme forever.
			if wantTLS := tc.wantMode != tlsModeUpstream; got.ServesTLS() != wantTLS {
				t.Errorf("ServesTLS() = %v for mode %v, want %v", got.ServesTLS(), got.Mode, wantTLS)
			}
		})
	}
}

// TestResolveTLS_SelfProvisionedPathsFollowTheDirectory proves the probe
// and the server look in the same place, which is the only reason
// resolveTLS computes these paths instead of the provisioning code doing
// it: the healthcheck subcommand runs the resolver and never generates
// anything.
func TestResolveTLS_SelfProvisionedPathsFollowTheDirectory(t *testing.T) {
	clearTLSEnvironment(t)

	// The default first, which is what lands on the container's data
	// volume because Dockerfile.controller sets WORKDIR /data.
	got, err := resolveTLS()
	if err != nil {
		t.Fatalf("resolveTLS(): %v", err)
	}
	if want := filepath.Join(defaultAutocertDir, tlscert.CertFileName); got.CertFile != want {
		t.Errorf("default CertFile = %q, want %q", got.CertFile, want)
	}
	// The KEY is in the serving bundle, not in a key.pem beside the
	// certificate. internal/tlscert publishes the pair as one file so that
	// replacing it is one atomic rename; cert.pem is the certificate alone,
	// which is what an operator copies out to clients and what must never
	// carry a private key.
	if want := filepath.Join(defaultAutocertDir, tlscert.BundleFileName); got.KeyFile != want {
		t.Errorf("default KeyFile = %q, want %q", got.KeyFile, want)
	}

	t.Setenv("PLEIADES_TLS_AUTOCERT_DIR", "/var/lib/pleiades/tls")
	got, err = resolveTLS()
	if err != nil {
		t.Fatalf("resolveTLS(): %v", err)
	}
	if want := "/var/lib/pleiades/tls/" + tlscert.CertFileName; got.CertFile != want {
		t.Errorf("configured CertFile = %q, want %q", got.CertFile, want)
	}
	if got.AutocertDir != "/var/lib/pleiades/tls" {
		t.Errorf("AutocertDir = %q, want the configured directory", got.AutocertDir)
	}
}

// TestAutocertOptions proves the two sources of extra subject alternative
// names both arrive, and that they land on the two different sides of the
// required/optional split.
//
// The split is the part that can regress silently. Both lists end up on the
// certificate either way, so a mistake here is invisible until the second
// restart, when a container's new hostname either does or does not throw
// away a certificate somebody already trusted.
func TestAutocertOptions(t *testing.T) {
	clearTLSEnvironment(t)

	hostname, hostErr := os.Hostname()
	if hostErr != nil {
		t.Skipf("this machine cannot report its own hostname: %v", hostErr)
	}

	t.Setenv(autocertHostsVar, "controller, pleiades.example.test ,10.9.8.7")
	got, err := autocertOptions()
	if err != nil {
		t.Fatalf("autocertOptions(): %v", err)
	}

	// Configured names are requirements: adding one must replace a stored
	// certificate that does not carry it.
	for _, want := range []string{"controller", " pleiades.example.test ", "10.9.8.7"} {
		if !containsString(got.ExtraNames, want) {
			t.Errorf("ExtraNames = %q, missing the configured name %q", got.ExtraNames, want)
		}
	}
	// The machine's own name is not, because in a container it is the
	// container id and changes on every recreate.
	if !containsString(got.OptionalNames, hostname) {
		t.Errorf("OptionalNames = %q, missing this machine's hostname %q", got.OptionalNames, hostname)
	}
	if containsString(got.ExtraNames, hostname) {
		t.Errorf("the hostname %q is in ExtraNames, so a container getting a new id would replace a certificate an operator already trusted", hostname)
	}
}

// containsString reports whether names holds want exactly.
//
// Exact rather than trimmed, deliberately: this asserts the raw split, so
// the test records that trimming and de-duplication are internal/tlscert's
// job. Two layers both trying to clean the same value is how one of them
// ends up doing it differently.
func containsString(names []string, want string) bool {
	for _, name := range names {
		if name == want {
			return true
		}
	}
	return false
}
