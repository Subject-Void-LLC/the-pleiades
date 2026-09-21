// Package credential defines the Crawl-tier CredentialStore port: the
// minimal value type and interface the platform uses to resolve a
// device's SSH username, password, or private key by device name, plus
// the redaction (this file) and masking (mask.go) helpers that keep
// those secrets out of logs and debug output.
//
// This is deliberately smaller than PLAN.md Section 17's full
// Postgres/Vault-backed CredentialStore (Walk/Run tier, behind unbuilt
// Phase 22). It follows the same "adapter behind a port, Crawl gets a
// file-backed one, Walk gets a database-backed one later" split
// internal/inventory already uses for its Repository interface
// (internal/inventory/file_repository.go is the Crawl-tier adapter,
// internal/inventory/ent_repository.go the Walk-tier one), applied here
// to credentials instead of inventory state. file_store.go is this
// package's Crawl-tier adapter.
package credential

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
)

// Credential is what identifies and authenticates to a device. Exactly
// one of Password or PrivateKeyPEM is normally set (a device is either
// password-authenticated or key-authenticated), but this type does not
// enforce that: the transport package built in a parallel slice of this
// phase decides which field to actually use when connecting, and a
// FileStore-backed Lookup may legitimately return both if an operator
// stored both for the same device.
type Credential struct {
	// Username is the account name to authenticate as. It is not a
	// secret and is shown in full by String and GoString below.
	Username string
	// Password is the password to authenticate with, or empty if this
	// credential is not password-authenticated.
	Password string
	// PrivateKeyPEM is the PEM-encoded private key to authenticate with,
	// or empty (nil) if this credential is not key-authenticated.
	PrivateKeyPEM []byte
	// Passphrase decrypts PrivateKeyPEM when the key itself is
	// passphrase-protected, and unlocks PFXBase64 when that is what this
	// credential carries instead. It is meaningless if both are empty.
	Passphrase string
	// CertificatePEM is the PEM-encoded X.509 client certificate this
	// credential presents, or empty (nil) if it authenticates some other
	// way. Its key half is PrivateKeyPEM, because a certificate's private
	// key is an ordinary PEM private key body and needed no field of its
	// own.
	//
	// This is the one field on this type that is NOT a secret: a
	// certificate is handed to whoever asks during a TLS handshake. It
	// lives here because it is useless without PrivateKeyPEM, not because
	// it needs protecting, and String below says so rather than marking it
	// redacted and implying otherwise.
	CertificatePEM []byte
	// PFXBase64 is a base64-encoded PKCS#12 bundle carrying a certificate
	// and its private key together, unlocked by Passphrase at the point of
	// use.
	//
	// It is an alternative to the CertificatePEM and PrivateKeyPEM pair
	// rather than a supplement. Base64 rather than []byte because this
	// value's whole journey is through map[string]string (Flatten below,
	// then the wire, then InjectSecrets), and choosing the encoding once
	// here beats every reader choosing one.
	PFXBase64 string
}

// redactedMarker replaces a secret field's real value in Credential's
// String and GoString output when that field is set. It never varies
// with the secret's length or content: even revealing a password's
// length can help an attacker guessing at it, so the only thing this
// output ever discloses is whether the field is set at all.
const redactedMarker = "<redacted, set>"

// notSetMarker marks a secret field that carries no value at all, so a
// debug print can distinguish "an empty credential" from "a credential
// whose password happens to be redacted," without ever revealing which
// secret, if any, is actually present.
const notSetMarker = "<not set>"

// publicSetMarker replaces a set field that is deliberately not a secret,
// so the output distinguishes "withheld from you" from "simply too bulky
// to print". See publicMarker below for why the distinction earns its
// keep.
const publicSetMarker = "<set, not secret>"

// String implements fmt.Stringer. Go's fmt package calls this
// automatically for the %v, %s, and %q verbs, including when a
// Credential is embedded as a field inside a larger struct formatted
// with %v or %+v. That automatic dispatch is what makes it impossible for
// an accidental fmt.Sprintf or log.Printf call anywhere else in the
// codebase to leak a real secret through this type via those verbs: the
// redaction happens here, once, rather than depending on every call site
// remembering to redact it by hand. String alone does NOT cover
// encoding/json or log/slog, which both reflect over a value's exported
// fields directly rather than consulting fmt.Stringer; MarshalJSON and
// LogValue below close those two paths the same way.
func (c Credential) String() string {
	return fmt.Sprintf(
		"credential.Credential{Username:%q, Password:%s, PrivateKeyPEM:%s, Passphrase:%s, "+
			"CertificatePEM:%s, PFXBase64:%s}",
		c.Username,
		setMarker(c.Password != ""),
		setMarker(len(c.PrivateKeyPEM) != 0),
		setMarker(c.Passphrase != ""),
		publicMarker(len(c.CertificatePEM) != 0),
		setMarker(c.PFXBase64 != ""),
	)
}

// GoString implements fmt.GoStringer. Go's fmt package calls this for
// the %#v verb instead of dumping the struct's real field values, so the
// same redaction guarantee String documents above holds for %#v too.
func (c Credential) GoString() string {
	return c.String()
}

// MarshalJSON implements json.Marshaler. Without this method,
// encoding/json.Marshal reflects directly over Credential's exported
// fields (it never consults fmt.Stringer/fmt.GoStringer), so
// json.Marshal(cred) would serialize Password and Passphrase in
// cleartext and PrivateKeyPEM as trivially reversible base64
// (encoding/json's default []byte encoding), completely bypassing the
// redaction String and GoString provide. This was a real, confirmed gap
// found during Phase W6's Schema/Injection Hardening audit
// (FAILURE_PATTERNS.md #22): the type's own doc comment claimed this was
// "structurally impossible" before this method existed, and it was not.
func (c Credential) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Username       string
		Password       string
		PrivateKeyPEM  string
		Passphrase     string
		CertificatePEM string
		PFXBase64      string
	}{
		Username:       c.Username,
		Password:       setMarker(c.Password != ""),
		PrivateKeyPEM:  setMarker(len(c.PrivateKeyPEM) != 0),
		Passphrase:     setMarker(c.Passphrase != ""),
		CertificatePEM: publicMarker(len(c.CertificatePEM) != 0),
		PFXBase64:      setMarker(c.PFXBase64 != ""),
	})
}

// LogValue implements slog.LogValuer. log/slog's JSON handler, like
// encoding/json, reflects over a value's fields directly rather than
// consulting fmt.Stringer; without this method a structured log call
// such as slog.Info("connecting", "credential", cred) would leak every
// secret field the same way an un-redacted json.Marshal would. slog
// resolves a LogValuer automatically before handing a value to any
// handler, so returning the same redacted String() here closes this path
// with the one definition every other redaction on this type already
// uses.
func (c Credential) LogValue() slog.Value {
	return slog.StringValue(c.String())
}

// setMarker returns redactedMarker if set is true, or notSetMarker
// otherwise. It centralizes the two literal marker strings String uses
// so every field's marker stays in sync with the others.
func setMarker(set bool) string {
	if set {
		return redactedMarker
	}
	return notSetMarker
}

// publicMarker is setMarker for a field that is set but is not a secret,
// which on this type is CertificatePEM alone.
//
// It exists so a debug print does not claim to be protecting something it
// is not. Marking a certificate "redacted" would be a lie in the
// direction that costs an incident responder time: they would go looking
// for a way to read a value that a TLS handshake already publishes to
// anyone who connects. The output is still a marker rather than the body,
// because a PEM certificate is several lines of noise in a log line, not
// because those lines are sensitive.
func publicMarker(set bool) string {
	if set {
		return publicSetMarker
	}
	return notSetMarker
}

// ErrNotFound is returned by Store.Lookup when no credential is stored
// for the requested device name. This covers two cases identically: the
// underlying credential file does not exist at all, or it exists but has
// no entry for that device name. Callers should compare against this
// with errors.Is rather than string matching, since adapters (file_store.go)
// wrap it with device-specific, actionable guidance text before
// returning it.
var ErrNotFound = errors.New("credential: no credential found for device")

// Store is the Crawl-tier port through which the platform resolves a
// device's SSH credential by name. internal/transport (built in a
// parallel slice of this phase) depends only on this interface, never on
// a concrete adapter, so a Postgres/Vault-backed Store built later for
// Walk tier (PLAN.md Section 17) is a new implementation of this
// interface, not a rewrite of every caller.
type Store interface {
	// Lookup resolves deviceName to its stored Credential. It returns
	// ErrNotFound (wrapped with device-specific guidance) if no
	// credential is stored for deviceName.
	Lookup(ctx context.Context, deviceName string) (Credential, error)
}
