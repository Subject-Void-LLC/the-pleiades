// Validating the one URL that decides how this process reaches the mesh.
//
// Nothing in this module validated NATS_URL before Phase 96d, on either
// side. No url.Parse existed anywhere in the composition roots or in the
// three packages that dial, and the Helm chart typed externalNats.url as a
// bare string with no pattern. The first thing that looked at the value at
// all was nats.Connect.
//
// That matters more than a typo usually does, because the scheme is not
// cosmetic: it selects the TRANSPORT. "nats" is plaintext TCP, "tls" is
// TCP with the connection encrypted, and "ws"/"wss" tunnel the protocol
// over WebSocket. A value that silently reads as a different scheme than
// the operator meant is a value that silently stops encrypting.

package topology

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"sort"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/tlscert"
)

// natsSchemes are the URL schemes nats.go actually implements, and
// therefore the only ones this module accepts.
//
// The list is an allowlist rather than a denylist deliberately. A denylist
// admits every scheme nobody thought to forbid, and the failure it lets
// through is the quiet one: nats.go's own parser defaults a bare host to
// plaintext, so a mistyped "tsl://" or a copied "https://" does not error,
// it downgrades.
var natsSchemes = map[string]string{
	"nats": "plaintext TCP",
	"tls":  "TCP with TLS",
	"ws":   "WebSocket, unencrypted",
	"wss":  "WebSocket over TLS",
}

// ValidateNatsURL checks that raw names one broker, over a scheme nats.go
// implements, and reports whether that scheme encrypts.
//
// It accepts a comma separated list, which nats.go supports for a cluster,
// and requires every member to agree about encryption: a list mixing
// "tls" and "nats" would leave which one is used to chance, and the
// insecure member is the one that decides what an attacker gets.
func ValidateNatsURL(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return fmt.Errorf("no NATS URL was given: set NATS_URL, for example nats://localhost:4222")
	}

	var encrypted, plaintext []string
	for _, member := range strings.Split(raw, ",") {
		member = strings.TrimSpace(member)
		if member == "" {
			return fmt.Errorf("NATS URL %q has an empty entry: a comma separated list must not have a trailing or doubled comma", raw)
		}

		parsed, err := url.Parse(member)
		if err != nil {
			return fmt.Errorf("NATS URL %q could not be parsed: %w", member, err)
		}
		if parsed.Scheme == "" {
			return fmt.Errorf(
				"NATS URL %q names no scheme: nats.go would treat it as plaintext TCP, so an omitted scheme silently means unencrypted. Write one of %s",
				member, knownSchemes())
		}
		if _, ok := natsSchemes[parsed.Scheme]; !ok {
			// "localhost:4222" is the likeliest mistake here and it does
			// NOT reach the empty-scheme branch above: url.Parse reads
			// everything before the colon as a scheme and the rest as an
			// opaque body, so the scheme comes back as "localhost". Saying
			// so is worth a branch, because the generic message would tell
			// an operator that "localhost" is an unimplemented transport.
			if parsed.Host == "" && parsed.Opaque != "" {
				return fmt.Errorf(
					"NATS URL %q looks like a bare host and port. It needs a scheme: write nats://%s for plaintext or tls://%s to encrypt. Without one, nats.go would treat it as plaintext",
					member, member, member)
			}
			return fmt.Errorf(
				"NATS URL %q uses the scheme %q, which nats.go does not implement. Write one of %s",
				member, parsed.Scheme, knownSchemes())
		}
		if parsed.Host == "" {
			return fmt.Errorf("NATS URL %q names a scheme but no host", member)
		}

		if NatsURLIsEncrypted(parsed.Scheme) {
			encrypted = append(encrypted, member)
		} else {
			plaintext = append(plaintext, member)
		}
	}

	if len(encrypted) > 0 && len(plaintext) > 0 {
		return fmt.Errorf(
			"NATS URL %q mixes encrypted and plaintext entries (%s against %s): which one a client uses is not something the caller controls, so the plaintext member decides what an attacker sees. Use one kind",
			raw, strings.Join(encrypted, " "), strings.Join(plaintext, " "))
	}
	return nil
}

// NatsURLIsEncrypted reports whether a scheme encrypts the connection.
//
// It is exported so a composition root can say so in its startup log
// rather than leaving an operator to infer it from a URL, which is the
// difference between a deployment that knows it is unencrypted and one
// that assumes it is not.
func NatsURLIsEncrypted(scheme string) bool {
	return scheme == "tls" || scheme == "wss"
}

// NatsURLScheme returns the scheme of the first entry in raw, or the empty
// string if there is none.
func NatsURLScheme(raw string) string {
	first := strings.TrimSpace(strings.SplitN(raw, ",", 2)[0])
	parsed, err := url.Parse(first)
	if err != nil {
		return ""
	}
	return parsed.Scheme
}

// knownSchemes renders the allowlist for an error message, in a stable
// order so the same mistake always produces the same text.
func knownSchemes() string {
	rendered := make([]string, 0, len(natsSchemes))
	for scheme, meaning := range natsSchemes {
		rendered = append(rendered, fmt.Sprintf("%s (%s)", scheme, meaning))
	}
	sort.Strings(rendered)
	return strings.Join(rendered, ", ")
}

// TLSFromEnv builds the client TLS configuration for a mesh connection
// from the environment, and refuses arrangements that contradict
// themselves.
//
// It mirrors the convention cmd/controller/tls.go already established for
// the Controller's own listener: a half-configured or self-contradictory
// arrangement is a STARTUP ERROR rather than a warning, because the
// failure it prevents is a process that comes up looking healthy while
// encrypting nothing.
//
// The two contradictions it refuses are the two that are silent. A CA file
// supplied for a plaintext URL means somebody believes this connection is
// encrypted and it is not. A tls:// or wss:// URL with no CA file is NOT
// refused, because a publicly signed broker legitimately verifies against
// the system pool, but it is reported so the choice is visible.
//
// caPath is read from NATS_CA_FILE. It returns nil when there is nothing
// to configure, which the caller passes straight to Connect as no option
// at all.
func TLSFromEnv(rawURL, caPath string, logger *slog.Logger) (*tls.Config, error) {
	if logger == nil {
		logger = slog.Default()
	}
	encrypted := NatsURLIsEncrypted(NatsURLScheme(rawURL))

	if caPath == "" {
		if encrypted {
			logger.Info("verifying the mesh broker against the system certificate pool",
				"reason", "NATS_CA_FILE is not set",
				"note", "set NATS_CA_FILE to trust a private authority instead")
		}
		return nil, nil
	}

	if !encrypted {
		return nil, fmt.Errorf(
			"NATS_CA_FILE names a certificate authority but NATS_URL uses the %q scheme, which does not encrypt. One of the two is wrong, and the dangerous reading is that this connection is protected when it is not: use tls:// or wss:// to encrypt, or unset NATS_CA_FILE",
			NatsURLScheme(rawURL))
	}

	// The guard gosec cannot see, and the one that actually matters: a
	// directory, a device or a named pipe here is a configuration mistake
	// rather than a certificate, and reading one would either hang or
	// produce a confusing parse error instead of naming the real problem.
	info, err := os.Stat(caPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read NATS_CA_FILE %q: %w", caPath, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("NATS_CA_FILE %q is not a regular file", caPath)
	}

	// #nosec G304 -- the path is this deployment's own trust material,
	// named by an operator in NATS_CA_FILE and read once at startup. It is
	// never a value from a request. This is the same justification
	// internal/tlscert's read.go records for reading a configured
	// certificate path, and the alternative is not verifying the broker's
	// certificate at all.
	pem, err := os.ReadFile(caPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read NATS_CA_FILE %q: %w", caPath, err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("NATS_CA_FILE %q contains no PEM certificate this build could parse", caPath)
	}

	// Consumed rather than written. WithTLS's own doc comment has told
	// callers since Phase 96d to take the configuration from
	// internal/tlscert rather than hand-rolling one, because a hand-rolled
	// one is where InsecureSkipVerify gets typed and where the version
	// floor gets forgotten; this is that function taking its own advice.
	// The floor arrives with it, stated once for every direction rather
	// than restated here as a second opinion.
	//
	// internal/archtest's TestOnlyTlscertBuildsAMeshTLSConfig is what
	// keeps this true: it fails the build if any file under
	// internal/topology constructs a tls.Config of its own.
	return tlscert.ClientConfig(pool), nil
}
