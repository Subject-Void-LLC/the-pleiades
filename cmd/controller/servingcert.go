// Obtaining the certificate this process serves, before anything else in
// this binary opens a port or a database.
//
// One function covers all three transport arrangements, and it returns the
// MATERIAL rather than a pair of paths for the listener to re-read. That is
// the fix for a real crash rather than a tidying-up: two controllers sharing
// an auto-provisioning directory could have one of them verify a pair, log
// that it was listening, and then have the standard library re-read those
// two files after the other controller replaced them, dying on
// "tls: private key does not match public key". What is verified here and
// what is served are now the same bytes.
//
// The other thing this file exists to do is talk. Every one of the three
// arrangements has a state that works badly and silently: a self-signed
// certificate nobody notices is temporary, an expired configured certificate
// failing every handshake with nothing in the log, and plain HTTP behind an
// ingress that turned out not to terminate TLS after all. Each gets a line
// naming what is wrong and what replaces it.
package main

import (
	"crypto/tls"
	"crypto/x509"
	"log/slog"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/tlscert"
)

// prepareServingCertificate loads, or provisions, the exact certificate and
// key this process will serve, and returns them.
//
// It is called after the logger is installed and before anything opens a
// database or joins an election, so a controller that cannot obtain a
// certificate fails before it touches shared state, exactly like every other
// startup dependency in this binary.
//
// Returning the MATERIAL rather than leaving the listener to re-read two
// paths is the point, and it fixes a crash rather than tidying anything up.
// Two controllers sharing an auto-provisioning directory could have one of
// them verify a pair, log that it was listening, and then have
// ListenAndServeTLS re-read those two files after the other controller
// replaced them, dying on "tls: private key does not match public key". What
// is verified here and what is served are now the same bytes.
//
// A nil return with a nil error is tlsModeUpstream: there is no certificate
// because there is no TLS on this listener.
func prepareServingCertificate(cfg tlsSettings, logger *slog.Logger) (*tls.Certificate, error) {
	switch cfg.Mode {
	case tlsModeUpstream:
		logger.Warn("serving plain HTTP because an ingress in front of this process is stated to terminate TLS",
			slog.String("stated_by", upstreamVar+"=1"),
			slog.String("requirement",
				"the session cookie is Secure and __Host- prefixed, so a browser silently drops it unless the ingress in front of this process really does serve HTTPS"))
		return nil, nil
	case tlsModeServe:
		return loadConfiguredCertificate(cfg, logger)
	default:
		return provisionServingCertificate(cfg, logger)
	}
}

// loadConfiguredCertificate serves the pair an operator named in
// TLS_CERT_FILE and TLS_KEY_FILE.
//
// It reads the pair at startup rather than leaving it to the listener, for
// two reasons. A path that names nothing becomes a startup error naming the
// file instead of a listener that dies a moment after the process reports
// itself up. And a certificate that is expired, or not valid yet, gets said
// out loud.
//
// It never falls back to generating one, and that restraint is the whole
// decision. Substituting a self-signed certificate for an operator's expired
// one would change this server's identity behind their back: every client
// that was told to expect the real certificate would start failing for a
// different reason, and the deployment would look like it recovered. Serving
// the expired certificate fails honestly. What was missing was any word
// about it, which is what turns a five-minute diagnosis into an afternoon.
func loadConfiguredCertificate(cfg tlsSettings, logger *slog.Logger) (*tls.Certificate, error) {
	// Logged BEFORE the read, because the read is the first thing in this
	// process that touches a path an operator chose, and a path that is a
	// named pipe or an unresponsive network mount is exactly where a startup
	// stops with nothing written down. internal/tlscert refuses anything
	// that is not an ordinary file, so this line and that guard are two
	// halves of the same fix.
	logger.Info("reading the configured serving certificate",
		slog.String("cert_file", cfg.CertFile),
		slog.String("key_file", cfg.KeyFile))

	cert, err := tlscert.Load(cfg.CertFile, cfg.KeyFile)
	if err != nil {
		return nil, err
	}

	if problem := tlscert.ValidityProblem(cert.Leaf); problem != "" {
		// ERROR, not WARN. Every handshake against this listener is going to
		// fail, so this is not a degraded state, it is an outage with a
		// known cause, and the log line is the only place that cause exists.
		logger.Error("the configured serving certificate is not usable right now and is being served anyway",
			slog.String("problem", problem),
			slog.String("cert_file", cfg.CertFile),
			slog.String("expires", cert.Leaf.NotAfter.UTC().Format(rfc3339Seconds)),
			slog.String("valid_for", certificateNames(cert.Leaf)),
			slog.String("why_not_replaced",
				"replacing it would serve a different identity than the one your clients were told to expect; install a renewed certificate at TLS_CERT_FILE and restart"))
	}

	logger.Info("serving TLS from the configured certificate",
		slog.String("cert_file", cfg.CertFile),
		slog.String("key_file", cfg.KeyFile),
		slog.String("expires", cert.Leaf.NotAfter.UTC().Format(rfc3339Seconds)),
		slog.String("valid_for", certificateNames(cert.Leaf)))
	return &cert.Pair, nil
}

// provisionServingCertificate writes or reuses the self-signed certificate
// this process serves in tlsModeSelfProvisioned, and says so out loud.
//
// The log line is at WARN and it is emitted on every single start,
// generated or reused. That is deliberate and it is the part of this
// feature most likely to be "tidied up" later. A self-signed certificate
// encrypts the connection and does not authenticate the server: a browser
// warns, and an operator who clicks through has no way to tell this
// controller from an impostor on the same address. A convenience that
// quietly degrades a security property has to keep saying so, and it has to
// name the two settings that replace it, or the deployment that was meant
// to be temporary is the one still running in a year.
func provisionServingCertificate(cfg tlsSettings, logger *slog.Logger) (*tls.Certificate, error) {
	// Before the call, for the same reason loadConfiguredCertificate logs
	// before its read: everything below this line touches a directory an
	// operator chose, and a start-up that stops there must not stop silently.
	// The field names are the ones the WARN below uses, so an operator reads
	// one vocabulary rather than two: serving_file is the file with the key in
	// it, trust_anchor_file is the copy without one. Here they are the paths
	// this is ABOUT to touch rather than paths that already exist, which is
	// the point of logging before the read.
	logger.Info("provisioning the serving certificate",
		slog.String("dir", cfg.AutocertDir),
		slog.String("serving_file", cfg.KeyFile),
		slog.String("trust_anchor_file", cfg.CertFile))

	cert, err := tlscert.Ensure(cfg.AutocertDir, cfg.AutocertOptions)
	if err != nil {
		return nil, err
	}

	provisioning := "reused"
	if cert.Generated {
		provisioning = "generated"
	}

	attrs := []any{
		slog.String("provisioning", provisioning),
		// The file the material really came from, named for what it is. It is
		// NOT called cert_file, because on the ordinary path it is
		// serving.pem, which holds the private key: an operator following a
		// field named cert_file into a `curl --cacert`, a ConfigMap or a copy
		// to a colleague would have handed this server's private key to
		// something that only ever wanted the certificate.
		slog.String("serving_file", cert.KeyFile),
		slog.String("expires", cert.Leaf.NotAfter.UTC().Format(rfc3339Seconds)),
		slog.String("valid_for", certificateNames(cert.Leaf)),
		slog.String("stored_in", "PLEIADES_TLS_AUTOCERT_DIR="+cfg.AutocertDir),
	}
	if anchor, ok := cert.AnchorFile(); ok {
		// The same certificate with no key in it: the one file to copy out
		// and hand a client as a trust anchor. Absent when there is no
		// key-free copy, rather than falling back to a path that has a key in
		// it.
		attrs = append(attrs, slog.String("trust_anchor_file", anchor))
	}
	if cert.Reason != "" {
		// The reason a certificate was replaced is computed precisely and
		// exists nowhere else. Dropping it, which is what this code used to
		// do, leaves "why does it regenerate on every restart?"
		// unanswerable from the logs.
		attrs = append(attrs, slog.String("replaced_because", cert.Reason))
	}

	// The headline follows what is actually being served. Material an
	// operator left in this directory is served exactly as it is, and calling
	// that "a self-signed certificate this controller provisioned for itself"
	// was simply false: it names the wrong author, implies the wrong
	// limitation, and points at settings that would not change it.
	headline := "serving TLS from a self-signed certificate this controller provisioned for itself"
	if cert.SelfProvisioned {
		attrs = append(attrs,
			slog.String("limitation",
				"self-signed: it encrypts traffic but does not authenticate this server, so browsers warn and a client that clicks through cannot tell this controller from an impostor"),
			slog.String("replace_it",
				"set TLS_CERT_FILE and TLS_KEY_FILE to serve a certificate from an authority your clients already trust, or "+upstreamVar+"=1 if an ingress in front of this process terminates TLS"),
			slog.String("add_names", autocertHostsVar+" adds subject alternative names, comma separated"))
	} else {
		headline = "serving TLS from certificate material this controller found in its auto-provisioning directory and did not write"
		attrs = append(attrs,
			slog.String("limitation",
				"this material is served exactly as it is and is never renewed or replaced, so nothing here will stop it expiring"),
			slog.String("replace_it",
				"set TLS_CERT_FILE and TLS_KEY_FILE to name this material deliberately, so it is read from where you put it rather than found in the directory this controller provisions into"))
	}
	logger.Warn(headline, attrs...)

	if cert.Warning != "" {
		// A state that is being served but is not what this controller
		// intended: an operator's own material found in this directory and
		// therefore never renewed, or a certificate that needed replacing in
		// a directory that could not be written. Both are worth serving and
		// neither is worth hiding. The warning names its own files, so this
		// adds the directory rather than repeating one of them under a label
		// that may not fit it.
		logger.Error("the serving certificate is not in the state this controller would have kept it in",
			slog.String("problem", cert.Warning),
			slog.String("dir", cfg.AutocertDir))
	}
	return &cert.Pair, nil
}

// rfc3339Seconds is the timestamp layout every certificate fact is logged
// in, written once so two log lines cannot describe the same expiry
// differently.
const rfc3339Seconds = "2006-01-02T15:04:05Z07:00"

// certificateNames renders every subject alternative name on a certificate
// as one comma separated string.
//
// Read off the parsed certificate rather than off the configuration,
// because those are two different facts: the configuration is what was
// asked for, and this is what a client will actually be shown and
// accepted for.
func certificateNames(leaf *x509.Certificate) string {
	names := make([]string, 0, len(leaf.DNSNames)+len(leaf.IPAddresses))
	names = append(names, leaf.DNSNames...)
	for _, ip := range leaf.IPAddresses {
		names = append(names, ip.String())
	}
	return strings.Join(names, ", ")
}
