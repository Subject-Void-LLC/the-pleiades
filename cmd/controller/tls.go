// How callers reach this process: the one decision that says whether the
// controller terminates TLS itself, trusts an ingress to have done it, or
// provisions a certificate for itself because nobody gave it one.
//
// This used to be three cases and a refusal. The refusal was the fourth:
// with nothing configured, the controller would not start. That was the
// right instinct and the wrong default. It made the compose stack, which
// exists to be one command, require a preparatory command first, and it
// made the first thing a new operator saw a startup error about a variable
// they had never heard of. The replacement keeps every refusal that was
// protecting something real (a half-written pair, two arrangements at
// once) and turns the "nothing set" case into a certificate this process
// writes for itself.
//
// The line that is NOT crossed: there is still no configuration in which
// this process serves plain HTTP without an operator explicitly saying an
// ingress terminated TLS in front of it. Auto-generating is a convenience
// that keeps traffic encrypted and the session cookie working. It is not a
// fallback to no encryption, and it is not a substitute for a real
// certificate: a self-signed certificate ENCRYPTS but does not
// AUTHENTICATE, so a client that clicks through the warning cannot tell
// this server from one that interposed itself.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/tlscert"
)

// defaultAutocertDir is where a self-provisioned certificate lands when
// PLEIADES_TLS_AUTOCERT_DIR is unset.
//
// Relative, deliberately, exactly like this binary's other two relative
// defaults (DB_DSN's sqlite://controller.db and CONTROLLER_CREDENTIALS_DIR's
// "."). Dockerfile.controller sets WORKDIR /data and that is the one
// directory the image gives the running process write access to, so a
// relative default lands on the data volume with nothing to configure, and
// it keeps working under the read-only root filesystem docker-compose.yml
// sets. An absolute default of /data would have baked a container path into
// a binary that also runs on a laptop.
const defaultAutocertDir = "tls"

// autocertHostsVar names the environment variable holding extra subject
// alternative names, as a constant because both the resolver and the
// startup log have to name it and a typo in one of them would advertise a
// variable that does nothing.
const autocertHostsVar = "PLEIADES_TLS_AUTOCERT_HOSTS"

// upstreamVar names the variable that says an ingress in front of this
// process already terminated TLS.
const upstreamVar = "PLEIADES_TLS_TERMINATED_UPSTREAM"

// tlsMode names how a caller's traffic reaches this process.
type tlsMode int

const (
	// tlsModeServe terminates TLS here, from the certificate and key
	// TLS_CERT_FILE and TLS_KEY_FILE name. An operator configured this
	// deliberately, so it wins over everything else.
	tlsModeServe tlsMode = iota

	// tlsModeUpstream serves plain HTTP because an ingress in front of
	// this process already terminated TLS.
	tlsModeUpstream

	// tlsModeSelfProvisioned terminates TLS here too, from a self-signed
	// certificate this process generates and then reuses across restarts.
	// It is the default when an operator has configured neither of the
	// two arrangements above.
	tlsModeSelfProvisioned
)

// tlsSettings is the resolved answer, and the paths that go with it.
type tlsSettings struct {
	Mode     tlsMode
	CertFile string
	KeyFile  string

	// AutocertDir and AutocertOptions are populated only in
	// tlsModeSelfProvisioned: the directory the pair lives in, and the
	// subject alternative names it has to carry beyond the loopback set
	// internal/tlscert always includes.
	AutocertDir     string
	AutocertOptions tlscert.Options
}

// ServesTLS reports whether this process terminates TLS on its own
// listener.
//
// Two of the three modes do, so every caller that used to compare against
// tlsModeServe asks this instead. Comparing against one mode was correct
// while there was only one TLS mode and became a silent bug the moment
// there were two: the container healthcheck would have probed http:// on
// an https listener and reported every self-provisioned controller
// permanently unhealthy.
func (s tlsSettings) ServesTLS() bool { return s.Mode != tlsModeUpstream }

// resolveTLS decides how this process serves.
//
// The whole table, in the order it is checked:
//
//  1. TLS_CERT_FILE and TLS_KEY_FILE both set, and no upstream claim:
//     serve them. An operator configured this on purpose and it always
//     wins.
//  2. PLEIADES_TLS_TERMINATED_UPSTREAM=1 and neither file: serve plain
//     HTTP, because an ingress already terminated TLS.
//  3. Both arrangements at once: a startup error.
//  4. Exactly one of the pair: a startup error.
//  5. Nothing set: generate a self-signed certificate, persist it, reuse
//     it on the next start, and serve HTTPS.
//
// The pair-or-neither rule is the same shape MASTER_ENCRYPTION_KEY_PREVIOUS
// and MASTER_ENCRYPTION_KEY_PREVIOUS_VERSION already use, and the
// both-arrangements-at-once case is the same shape DB_DSN and DB_PATH use:
// an operator who wrote down two different intentions is told, rather than
// left to discover which one this code happened to prefer.
//
// Why case 5 generates rather than refuses, which is the decision this
// function exists to record. The session cookie carries Secure and the
// __Host- prefix unconditionally, and a browser silently refuses such a
// cookie on a plain-HTTP origin. Loopback is the one exception every
// browser makes, and it keys on the host STRING, so it does not cover a
// hostname in /etc/hosts that resolves to 127.0.0.1, a container name, or
// any LAN address. A controller serving plain HTTP unattended would
// therefore render "Those credentials were not accepted" to an operator who
// typed the right password. Refusing to start says that out loud, and was
// the first answer here. It also meant `docker compose up` could not work
// without a preparatory command, which is a real cost paid by everybody to
// protect against a mistake nobody was making: the operator who has
// configured nothing is not asking for plain HTTP, they have not gotten
// that far. Generating keeps the encrypted transport and the working cookie
// and asks nothing of them.
//
// Nothing here reads X-Forwarded-Proto, or any other forwarded header, and
// that omission is deliberate. This codebase already refuses to trust
// X-Forwarded-For in two places (internal/api's callerKey and
// internal/ui/web's loginCallerKey), for the reason that applies just as
// well here: a header a client can set is a claim a client can forge, and
// trusting one requires knowing which proxy is in front of this process,
// which nothing here knows. The answer is an explicit setting an operator
// states once, not a header the process believes on every request.
func resolveTLS() (tlsSettings, error) {
	certFile := os.Getenv("TLS_CERT_FILE")
	keyFile := os.Getenv("TLS_KEY_FILE")
	upstream, err := upstreamTerminated()
	if err != nil {
		return tlsSettings{}, err
	}

	switch {
	case certFile != "" && keyFile != "" && upstream:
		return tlsSettings{}, fmt.Errorf(
			"TLS_CERT_FILE and TLS_KEY_FILE name a certificate to serve while %s claims an ingress already terminated TLS; set one arrangement or the other, never both", upstreamVar)
	case certFile != "" && keyFile != "":
		return tlsSettings{Mode: tlsModeServe, CertFile: certFile, KeyFile: keyFile}, nil
	case certFile != "" || keyFile != "":
		return tlsSettings{}, fmt.Errorf(
			"TLS_CERT_FILE and TLS_KEY_FILE must be set together, or not at all; one without the other is not a servable configuration")
	case upstream:
		return tlsSettings{Mode: tlsModeUpstream}, nil
	default:
		// The paths are computed here rather than after generation so the
		// healthcheck subcommand, which runs this same resolver and never
		// generates anything, knows where to look for the certificate the
		// server is presenting. They stay relative when the directory is:
		// the probe runs in the same container and the same working
		// directory as the server, which is what makes a relative default
		// safe on both sides.
		//
		// The two do not name the same file, and that is deliberate.
		// internal/tlscert publishes the pair it serves as ONE file, so that
		// replacing it is a single atomic rename and several controllers can
		// share the directory with no lock between them; the private key
		// lives in that bundle. cert.pem is the certificate on its own, which
		// is what an operator copies out as a trust anchor and what must
		// never carry a private key.
		dir := getenv("PLEIADES_TLS_AUTOCERT_DIR", defaultAutocertDir)
		opts, err := autocertOptions()
		if err != nil {
			return tlsSettings{}, err
		}
		return tlsSettings{
			Mode:            tlsModeSelfProvisioned,
			CertFile:        filepath.Join(dir, tlscert.CertFileName),
			KeyFile:         filepath.Join(dir, tlscert.BundleFileName),
			AutocertDir:     dir,
			AutocertOptions: opts,
		}, nil
	}
}

// upstreamTerminated reads the one variable that turns TLS off on this
// listener, and refuses a value it does not understand.
//
// The refusal is the whole point, and it replaces a check that read
// `os.Getenv(...) == "1"`. Under that check an operator who wrote
// PLEIADES_TLS_TERMINATED_UPSTREAM=true got the opposite of what they asked
// for: the value was treated as unset, so the controller provisioned a
// certificate and served HTTPS behind an ingress forwarding plain HTTP, and
// every request failed. The operator had written their intention down and
// the code ignored it, which is the worst of the three possible outcomes.
//
// Both spellings of yes and both of no are accepted, because "true", "yes"
// and "on" are all things people really write in an environment file and
// none of them is ambiguous. Anything else is a startup error rather than a
// guess: this resolver already refuses half-configured intent (a certificate
// without its key, two arrangements at once) and a value nobody can parse is
// the same kind of statement.
func upstreamTerminated() (bool, error) {
	raw := os.Getenv(upstreamVar)
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "":
		return false, nil
	case "1", "true", "yes", "on":
		return true, nil
	case "0", "false", "no", "off":
		return false, nil
	default:
		return false, fmt.Errorf(
			"%s is set to %q, which is neither a yes (1, true, yes, on) nor a no (0, false, no, off); it decides whether this controller serves plain HTTP, so it is not a value to guess at",
			upstreamVar, raw)
	}
}

// autocertOptions is every subject alternative name a self-provisioned
// certificate carries beyond the loopback set internal/tlscert always
// includes, split into the ones an operator asked for and the one this
// process discovered about itself.
//
// PLEIADES_TLS_AUTOCERT_HOSTS is REQUIRED (ExtraNames): the DNS record in
// front of this controller, a load balancer's name, the address a colleague
// uses. An operator reaching the controller by a name the certificate does
// not carry gets a browser rejection for a name mismatch, which is a
// DIFFERENT failure from the one auto-provisioning fixes and looks
// identical to them, so adding a name here has to take effect on the next
// restart. internal/tlscert replaces a stored certificate missing a
// required name rather than reusing it.
//
// The host's own name is OPTIONAL, and that is not a hedge. Inside a
// container the hostname is the container ID, which changes every time
// `docker compose down` is followed by `up`. Treating it as required made
// every restart replace a perfectly good certificate, so every operator was
// asked to trust a new one again, which is exactly what reuse exists to
// prevent. It is still put ON the certificate, because reaching a host by
// its own name is a real thing to want; it just never invalidates one.
//
// A failure to read the hostname is not an error. It is one name out of
// several on a certificate that still covers loopback, so the honest
// response is to carry on without it rather than to refuse to start over a
// nicety.
// A value this environment variable cannot hold is a startup error naming
// the variable, not a signing failure from inside crypto/x509 several steps
// later. A DNS name on a certificate is encoded as ASCII and nothing else,
// so an international domain pasted in its display form, or a hostname with
// a non-breaking space in it, used to fail with "x509: SAN dNSName is
// malformed", which names neither the value nor the setting it came from.
// internal/tlscert decides what a usable name is; this function adds the one
// piece it cannot know, which is where the value came from.
func autocertOptions() (tlscert.Options, error) {
	var opts tlscert.Options
	if hostname, err := os.Hostname(); err == nil && hostname != "" {
		opts.OptionalNames = append(opts.OptionalNames, hostname)
	}
	if extra := os.Getenv(autocertHostsVar); extra != "" {
		// Split only. Blank entries, duplicates and case differences are
		// internal/tlscert's to drop, so a value written with spaces after
		// the commas behaves the same as one written without.
		opts.ExtraNames = append(opts.ExtraNames, strings.Split(extra, ",")...)
	}

	if err := tlscert.ValidateNames(opts.ExtraNames); err != nil {
		return tlscert.Options{}, fmt.Errorf("%s: %w", autocertHostsVar, err)
	}
	// The machine's own hostname is checked too, and NOT reported as a
	// configuration error, because nobody configured it here: a machine
	// named in a way a certificate cannot carry is a fact about the machine.
	// Dropping the name keeps the controller starting, which is the same
	// call the failure-to-read-the-hostname path already makes.
	if err := tlscert.ValidateNames(opts.OptionalNames); err != nil {
		opts.OptionalNames = nil
	}
	return opts, nil
}
