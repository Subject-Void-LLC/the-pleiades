// Package credential defines the Walk-tier CredentialStore port: the
// minimal value type and interface the platform uses to resolve a
// device's SSH username, password, or private key by device name, plus
// the redaction (this file) and masking (mask.go) helpers that keep
// those secrets out of logs and debug output.
//
// This is deliberately smaller than PLAN.md Section 17's full
// Postgres/Vault-backed CredentialStore (Crawl/Run tier, behind unbuilt
// Phase 22). It follows the same "adapter behind a port, Walk gets a
// file-backed one, Crawl gets a database-backed one later" split
// internal/inventory already uses for its Repository interface
// (internal/inventory/file_repository.go is the Walk-tier adapter,
// internal/inventory/ent_repository.go the Crawl-tier one), applied here
// to credentials instead of inventory state. file_store.go is this
// package's Walk-tier adapter.
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
	// passphrase-protected. It is meaningless if PrivateKeyPEM is empty.
	Passphrase string
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
		"credential.Credential{Username:%q, Password:%s, PrivateKeyPEM:%s, Passphrase:%s}",
		c.Username,
		setMarker(c.Password != ""),
		setMarker(len(c.PrivateKeyPEM) != 0),
		setMarker(c.Passphrase != ""),
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
		Username      string
		Password      string
		PrivateKeyPEM string
		Passphrase    string
	}{
		Username:      c.Username,
		Password:      setMarker(c.Password != ""),
		PrivateKeyPEM: setMarker(len(c.PrivateKeyPEM) != 0),
		Passphrase:    setMarker(c.Passphrase != ""),
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

// ErrNotFound is returned by Store.Lookup when no credential is stored
// for the requested device name. This covers two cases identically: the
// underlying credential file does not exist at all, or it exists but has
// no entry for that device name. Callers should compare against this
// with errors.Is rather than string matching, since adapters (file_store.go)
// wrap it with device-specific, actionable guidance text before
// returning it.
var ErrNotFound = errors.New("credential: no credential found for device")

// Store is the Walk-tier port through which the platform resolves a
// device's SSH credential by name. internal/transport (built in a
// parallel slice of this phase) depends only on this interface, never on
// a concrete adapter, so a Postgres/Vault-backed Store built later for
// Crawl tier (PLAN.md Section 17) is a new implementation of this
// interface, not a rewrite of every caller.
type Store interface {
	// Lookup resolves deviceName to its stored Credential. It returns
	// ErrNotFound (wrapped with device-specific guidance) if no
	// credential is stored for deviceName.
	Lookup(ctx context.Context, deviceName string) (Credential, error)
}
