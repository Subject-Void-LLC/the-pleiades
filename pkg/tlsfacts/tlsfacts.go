// Package tlsfacts holds the tls.certificate.* fact family: what the
// certificate a device presented says, recorded by each facts gatherer that
// reaches a device over TLS (net.gnmi.facts and net.restconf.facts, and the
// config methods that record the same facts), whichever protocol carried it.
// They are stable facts, so a changed certificate is a change in the device's
// fact history (and drift, Phase 41d), and a certificate near its last day is
// said on the run that reads it, before a pin refuses its replacement.
//
// The certificate comes from the read's own verified handshakes
// (devicetls.WithPresented), never from a connection of this package's own,
// so what is recorded is what the read trusted. It is in pkg/ because a
// Collection imports only pkg/.
package tlsfacts

import (
	"fmt"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// The tls.certificate.* facts.
const (
	FactFingerprint    = "tls.certificate.fingerprint"
	FactKeyFingerprint = "tls.certificate.key_fingerprint"
	FactSubject        = "tls.certificate.subject"
	FactIssuer         = "tls.certificate.issuer"
	FactNotAfter       = "tls.certificate.not_after"
)

// FactKeys are the tls.certificate.* facts in the order a read records them.
var FactKeys = []string{FactFingerprint, FactKeyFingerprint, FactSubject, FactIssuer, FactNotAfter}

// Returned is when the tls.certificate.* facts are recorded, for their
// documentation.
const Returned = "when the device was reached over TLS and presented one certificate, as a stat and a fact"

// Returns documents the tls.certificate.* facts, for a method's manifest and
// its catalog entry alike.
func Returns() []collection.ReturnField {
	return []collection.ReturnField{
		{Name: FactFingerprint, Type: "string", Returned: Returned, Description: "The SHA-256 of the certificate the device presented, sha256:<hex>, the form a certificate pin (tls_fingerprint) takes: it changes whenever the certificate does, renewed or not."},
		{Name: FactKeyFingerprint, Type: "string", Returned: Returned, Description: "The SHA-256 of its public key (SubjectPublicKeyInfo), spki-sha256:<hex>, the form a key pin takes: it stays the same across a certificate the device issues again for the same key."},
		{Name: FactSubject, Type: "string", Returned: Returned, Description: "The certificate's subject, as text, with no control or format character and at most 512 bytes."},
		{Name: FactIssuer, Type: "string", Returned: Returned, Description: "The certificate's issuer, as text, bounded as the subject is: the subject again for a self-signed certificate."},
		{Name: FactNotAfter, Type: "string", Returned: Returned, Description: "The last moment the certificate is valid, RFC 3339 in UTC. A read within 30 days of it, or past it, warns, in words that fit how the device is trusted: a certificate pin refuses the renewed certificate until it is pinned too."},
	}
}

// Record records what the one certificate seen (on seen, the read's verified
// handshakes) says as stats and facts, and returns the warnings it calls for
// under settings (the device's TLS as the read used it, named device): one
// past or within devicetls.ExpiryWindow of its last day, as of now, and each
// pin of a list nothing presented. A read that saw no certificate (plaintext,
// or no handshake) records nothing, and one that saw more than one records
// none, saying why, since facts from an address more than one server answers
// would flap between them.
func Record(rc sdk.RunbookContext, device string, settings devicetls.Settings, seen *devicetls.Presented, now time.Time) ([]string, error) {
	certs, more := seen.Certificates()
	switch {
	case len(certs) == 0:
		return nil, nil
	case len(certs) > 1 || more:
		return []string{fmt.Sprintf("device %q presented more than one certificate during this read (%d seen), so no %s fact is recorded: more than one server answers at its address", device, len(certs), "tls.certificate")}, nil
	}
	c := devicetls.Describe(certs[0])
	values := map[string]any{
		FactFingerprint: c.Fingerprint, FactKeyFingerprint: c.KeyFingerprint,
		FactSubject: c.Subject, FactIssuer: c.Issuer, FactNotAfter: c.NotAfterText(),
	}
	for _, key := range FactKeys {
		if err := rc.SetStat(key, values[key]); err != nil {
			return nil, err
		}
		if err := rc.EmitFact(key, values[key]); err != nil {
			return nil, err
		}
	}
	var warnings []string
	if w := settings.ExpiryWarning(device, certs[0], now); w != "" {
		warnings = append(warnings, w)
	}
	return append(warnings, settings.RetireWarnings(device, certs[0])...), nil
}
