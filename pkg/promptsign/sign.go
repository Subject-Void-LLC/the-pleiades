// Package promptsign: the signed document, the keys that may sign one,
// and what a use's policy makes of a signature.
package promptsign

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// The kinds of document this signs.
const (
	KindPrompt     = "prompt"
	KindParameters = "parameters"
	KindRoutes     = "routes"
)

// Document is a signed document's envelope: what it is, which version of
// which name, the use it is for, when it stops being trusted, and its
// body. A prompt's body holds text and per-model variants; a parameter
// set's and a routing table's hold their own fields. Every field is signed.
type Document struct {
	Kind       string          `json:"kind"`
	Name       string          `json:"name"`
	Version    int64           `json:"version"`
	Use        string          `json:"use"`
	Expires    string          `json:"expires,omitempty"`
	Text       string          `json:"text,omitempty"`
	Variants   map[string]any  `json:"variants,omitempty"`
	Parameters map[string]any  `json:"parameters,omitempty"`
	Body       json.RawMessage `json:"body,omitempty"`
}

// Parsed is a document read from its canonical bytes.
type Parsed struct {
	Document
	// Canonical is the document's canonical form, which is what is signed.
	Canonical []byte
	// Hash is the SHA-256 of Canonical in hex.
	Hash string
}

// Parse reads a document: canonicalized first (refusing what two readers
// could read differently), then decoded strictly, then checked.
func Parse(raw []byte) (*Parsed, error) {
	canon, err := Canonical(raw)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(canon))
	dec.DisallowUnknownFields()
	var d Document
	if err := dec.Decode(&d); err != nil {
		return nil, fmt.Errorf("not a signable document: %w", err)
	}
	switch {
	case d.Kind != KindPrompt && d.Kind != KindParameters && d.Kind != KindRoutes:
		return nil, fmt.Errorf("kind %q is not prompt, parameters or routes", d.Kind)
	case d.Name == "" || d.Use == "":
		return nil, errors.New("a document needs a name and a use")
	case d.Version < 1:
		return nil, errors.New("a document's version is a whole number from 1")
	case d.Kind == KindPrompt && d.Text == "":
		return nil, errors.New("a prompt needs text")
	}
	if d.Expires != "" {
		if _, err := time.Parse(time.RFC3339, d.Expires); err != nil {
			return nil, fmt.Errorf("expires %q is not an RFC 3339 time", d.Expires)
		}
	}
	sum := sha256.Sum256(canon)
	return &Parsed{Document: d, Canonical: canon, Hash: hex.EncodeToString(sum[:])}, nil
}

// Signature is a detached signature: the signing key's id and the
// Ed25519 signature over the document's canonical form.
type Signature struct {
	Key   string `json:"key"`
	Value string `json:"sig"`
}

// KeyID is a public key's id: the first 16 hex digits of its SHA-256.
func KeyID(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:8])
}

// signDomain is put before the canonical form in what is signed, so a
// signature a key made over some other JSON for another purpose is never a
// valid signature of a prompt document.
const signDomain = "pleiades-prompt-document-v1\n"

// signedBytes are the bytes a signature covers.
func signedBytes(doc *Parsed) []byte {
	return append([]byte(signDomain), doc.Canonical...)
}

// Sign signs a document's canonical form, under the domain prefix.
func Sign(doc *Parsed, key ed25519.PrivateKey) Signature {
	return Signature{
		Key:   KeyID(key.Public().(ed25519.PublicKey)),
		Value: base64.StdEncoding.EncodeToString(ed25519.Sign(key, signedBytes(doc))),
	}
}

// TrustedKey is a public key and the uses it may sign for.
type TrustedKey struct {
	Public ed25519.PublicKey
	// Uses is the key's role: the uses whose documents it may sign.
	Uses []string
	// Expires is when the key stops being trusted; zero for never.
	Expires time.Time
}

// ID is the key's id.
func (k TrustedKey) ID() string { return KeyID(k.Public) }

// mayFor reports whether the key's role covers use.
func (k TrustedKey) mayFor(use string) bool {
	for _, u := range k.Uses {
		if u == use {
			return true
		}
	}
	return false
}

// Policy is what a use requires of its documents' signatures.
type Policy string

// The policies.
const (
	Off      Policy = "off"
	Optional Policy = "optional"
	Required Policy = "required"
)

// ParsePolicy reads a policy, optional when empty.
func ParsePolicy(s string) (Policy, error) {
	switch Policy(s) {
	case "":
		return Optional, nil
	case Off, Optional, Required:
		return Policy(s), nil
	}
	return "", fmt.Errorf("%q is not a signing policy (off, optional, required)", s)
}

// Status is a verdict's outcome.
type Status string

// The outcomes. Verified is only ever the result of a check that passed.
const (
	Verified Status = "verified"
	Unsigned Status = "unsigned"
	Skipped  Status = "not checked"
	Refused  Status = "refused"
)

// Verdict is what verifying a document decided, as a run records it.
type Verdict struct {
	Status  Status
	Name    string
	Version int64
	Hash    string
	Key     string
	Reason  string
}

// Map is the verdict as a return value.
func (v Verdict) Map() map[string]any {
	m := map[string]any{"status": string(v.Status), "name": v.Name, "version": v.Version, "hash": v.Hash}
	if v.Key != "" {
		m["key"] = v.Key
	}
	if v.Reason != "" {
		m["reason"] = v.Reason
	}
	return m
}

// Versions is the register of the newest version of each name already
// used, which a document older than is refused (a rollback).
type Versions interface {
	Newest(kind, name string) (int64, bool, error)
}

// Verify decides on doc with sig (nil for none) under policy, at now,
// against keys and the register. With optional, a document with no
// signature is recorded unsigned and one with a signature is checked like
// required, so a bad signature is never quietly accepted; with off,
// nothing is checked and the verdict says so. A refusal's error says why.
func Verify(doc *Parsed, sig *Signature, policy Policy, keys []TrustedKey, versions Versions, now time.Time) (Verdict, error) {
	v := Verdict{Name: doc.Name, Version: doc.Version, Hash: doc.Hash}
	refuse := func(reason string) (Verdict, error) {
		v.Status, v.Reason = Refused, reason
		return v, errors.New(reason)
	}
	switch {
	case policy == Off:
		v.Status = Skipped
		return v, nil
	case sig == nil && policy == Optional:
		v.Status = Unsigned
		return v, nil
	case sig == nil:
		return refuse(fmt.Sprintf("%s %s v%d is not signed, and its use %s requires a signature", doc.Kind, doc.Name, doc.Version, doc.Use))
	}
	v.Key = sig.Key
	var key *TrustedKey
	for i := range keys {
		if keys[i].ID() == sig.Key {
			key = &keys[i]
		}
	}
	if key == nil {
		return refuse(fmt.Sprintf("%s %s v%d is signed by key %s, which is not trusted", doc.Kind, doc.Name, doc.Version, sig.Key))
	}
	raw, err := base64.StdEncoding.DecodeString(sig.Value)
	if err != nil || len(raw) != ed25519.SignatureSize || !ed25519.Verify(key.Public, signedBytes(doc), raw) {
		return refuse(fmt.Sprintf("%s %s v%d's signature does not verify against key %s: the document changed, or another key signed it", doc.Kind, doc.Name, doc.Version, sig.Key))
	}
	if !key.mayFor(doc.Use) {
		return refuse(fmt.Sprintf("key %s may sign for %s, not for use %s", sig.Key, strings.Join(key.Uses, ", "), doc.Use))
	}
	if !key.Expires.IsZero() && now.After(key.Expires) {
		return refuse(fmt.Sprintf("key %s expired %s", sig.Key, key.Expires.Format(time.RFC3339)))
	}
	if doc.Expires != "" {
		expires, _ := time.Parse(time.RFC3339, doc.Expires)
		if now.After(expires) {
			return refuse(fmt.Sprintf("%s %s v%d expired %s", doc.Kind, doc.Name, doc.Version, doc.Expires))
		}
	}
	if versions != nil {
		newest, ok, err := versions.Newest(doc.Kind, doc.Name)
		if err != nil {
			return refuse(fmt.Sprintf("the version register cannot be read: %v", err))
		}
		if ok && doc.Version < newest {
			return refuse(fmt.Sprintf("%s %s v%d is older than v%d, already used: a rollback", doc.Kind, doc.Name, doc.Version, newest))
		}
	}
	v.Status = Verified
	return v, nil
}
