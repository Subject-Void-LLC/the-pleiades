// Package sshfacts holds the ssh.host_key.* fact family: the fingerprint of
// each SSH host key a device proved itself with on a session whose known_hosts
// check accepted that key. facts.gather records it, and a network facts method
// reaching a device over SSH can record it the same way, from the connections
// it already made (remoteexec.Conn.VerifiedHostKeys), never from a connection
// of this package's own. It is in pkg/ because a Collection imports only pkg/.
//
// The rules, each enforced here or in pkg/remoteexec:
//
//   - a key is recorded only when the known_hosts check accepted it on the
//     target's own leg (never a bastion's);
//   - a session that skipped host key verification records none and says so;
//   - there is one stable fact per key type, valued as ssh-keygen -lf prints the
//     key's fingerprint (SHA256:<base64>);
//   - a key of a type no fact names records nothing and warns;
//   - a read that met more than one server records nothing at all, and warns:
//     a retried dial whose earlier attempt accepted another key, two
//     connections that proved themselves with different keys, or two keys of
//     one type.
//
// No code reads these facts as trust: known_hosts decides what a session
// trusts, and a fact only says what a past session's check accepted.
package sshfacts

import (
	"fmt"

	"golang.org/x/crypto/ssh"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// Family is the prefix every fact of this package shares. It is reserved
// (collection.ReservedFactFamilies), so no external Collection can declare a
// fact inside it.
const Family = "ssh."

// The ssh.host_key.* facts, one per key type.
const (
	FactED25519   = "ssh.host_key.ed25519"
	FactECDSAP256 = "ssh.host_key.ecdsa_p256"
	FactECDSAP384 = "ssh.host_key.ecdsa_p384"
	FactECDSAP521 = "ssh.host_key.ecdsa_p521"
	FactRSA       = "ssh.host_key.rsa"
	FactDSA       = "ssh.host_key.dsa"
)

// FactKeys are the ssh.host_key.* facts in the order a read records them.
var FactKeys = []string{FactED25519, FactECDSAP256, FactECDSAP384, FactECDSAP521, FactRSA, FactDSA}

// Returned is when the ssh.host_key.* facts are recorded, for their
// documentation.
const Returned = "when the device was reached over SSH and its known_hosts check accepted a key of this type, as a fact"

// Returns documents the ssh.host_key.* facts, for a method's manifest and its
// catalog entry alike.
func Returns() []collection.ReturnField {
	describe := func(kind string) string {
		return "The SHA-256 fingerprint of the " + kind + " host key the device proved itself with, on a session whose known_hosts check accepted that key, " +
			"as ssh-keygen -lf prints it (SHA256:<base64>); for a host certificate, its key's. None is recorded from a session that skipped host key " +
			"verification, nor from a read that met more than one server at the address (a retried login that reached another server, or two keys of one type). A change means the device proved itself with another key known_hosts accepts, such as one a person added when the old one was replaced. " +
			"It is never read as trust: known_hosts alone decides what a session trusts."
	}
	return []collection.ReturnField{
		{Name: FactED25519, Type: "string", Returned: Returned, Description: describe("ed25519")},
		{Name: FactECDSAP256, Type: "string", Returned: Returned, Description: describe("ECDSA P-256")},
		{Name: FactECDSAP384, Type: "string", Returned: Returned, Description: describe("ECDSA P-384")},
		{Name: FactECDSAP521, Type: "string", Returned: Returned, Description: describe("ECDSA P-521")},
		{Name: FactRSA, Type: "string", Returned: Returned, Description: describe("RSA")},
		{Name: FactDSA, Type: "string", Returned: Returned, Description: describe("DSA")},
	}
}

// Fact is one ssh.host_key.* fact a read can record.
type Fact struct {
	// Key is the fact's name, one of FactKeys.
	Key string
	// Value is the key's fingerprint, SHA256:<base64>.
	Value string
}

// Read returns the facts the host keys seen on a read's connections (each
// connection's remoteexec.Conn.VerifiedHostKeys) call for, in FactKeys order,
// and the warnings to carry for device: that a connection skipped
// verification, that a key's type has no fact, or that more than one server
// answered, in which case no fact is returned at all. More than one server
// answered when a connection's dial retried and an earlier attempt accepted a
// key the session did not use, when two verified connections proved
// themselves with different keys, or when one key type answered with two
// keys. A server's own keys do not change between connections made the same
// way, so a difference of any type is another server, not another key.
func Read(device string, seen ...remoteexec.HostKeys) ([]Fact, []string) {
	var warnings []string
	var keys []ssh.PublicKey
	var first []ssh.PublicKey
	more, verified, other := false, false, false
	for _, hk := range seen {
		if !hk.Verified() {
			warnings = append(warnings, fmt.Sprintf("device %q: host key not recorded from a session that did not verify it (%s)", device, sdk.ParamInsecureSkipHostKeyVerify))
			continue
		}
		k, m := hk.Keys()
		if verified && !sameKeys(first, k) {
			other = true
		}
		if !verified {
			first, verified = k, true
		}
		other = other || hk.OtherAttempt()
		keys, more = append(keys, k...), more || m
	}
	if other {
		return nil, append(warnings, moreThanOneServer(device))
	}
	facts, w := factsOf(device, keys, more)
	return facts, append(warnings, w...)
}

// moreThanOneServer is the warning a read that met more than one server for
// device carries.
func moreThanOneServer(device string) string {
	return fmt.Sprintf("device %q proved itself with more than one host key during this read, so no %shost_key fact is recorded: more than one server answers at its address", device, Family)
}

// sameKeys reports whether a and b hold the same keys, in any order.
func sameKeys(a, b []ssh.PublicKey) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]bool{}
	for _, k := range a {
		seen[string(k.Marshal())] = true
	}
	for _, k := range b {
		if !seen[string(k.Marshal())] {
			return false
		}
	}
	return true
}

// factsOf is Read's decision on the verified keys alone, more saying a
// connection accepted more keys than it keeps.
func factsOf(device string, keys []ssh.PublicKey, more bool) ([]Fact, []string) {
	var warnings []string
	byFact := map[string]string{}
	twice := map[string]bool{}
	for _, key := range keys {
		fact, ok := factFor(key)
		if !ok {
			warnings = append(warnings, fmt.Sprintf("device %q presented a host key of type %q, which no %shost_key fact names, so it is not recorded", device, key.Type(), Family))
			continue
		}
		value := ssh.FingerprintSHA256(plain(key))
		if old, seen := byFact[fact]; seen && old != value {
			twice[fact] = true
		}
		byFact[fact] = value
	}
	if len(twice) > 0 || more {
		return nil, append(warnings, moreThanOneServer(device))
	}
	var out []Fact
	for _, fact := range FactKeys {
		if value, ok := byFact[fact]; ok {
			out = append(out, Fact{Key: fact, Value: value})
		}
	}
	return out, warnings
}

// MaxFacts is the most facts one Record call emits: one per key type, so
// len(FactKeys). A caller counts it against
// collection.MaxFactsPerObservation before calling, since Record emits as it
// goes.
const MaxFacts = 6

// Record emits, through rc, the facts Read returns for the read's connections
// that want accepts (nil accepts every one, a task's filter is what a caller
// passes), and returns how many it emitted and Read's warnings for the caller
// to carry. When want accepts none of FactKeys it reads nothing and warns of
// nothing, so a task that filtered these facts out is not told why none was
// recorded. It emits at most MaxFacts facts, and facts only, no stat, as
// facts.gather emits everything it learns; it is the call a network facts
// method reaching a device over SSH makes with its connections' host keys.
func Record(rc sdk.RunbookContext, device string, want func(key string) bool, seen ...remoteexec.HostKeys) (int, []string, error) {
	if want != nil && !wantsAny(want) {
		return 0, nil, nil
	}
	facts, warnings := Read(device, seen...)
	emitted := 0
	for _, f := range facts {
		if want != nil && !want(f.Key) {
			continue
		}
		if err := rc.EmitFact(f.Key, f.Value); err != nil {
			return emitted, nil, fmt.Errorf("%s: %w", f.Key, err)
		}
		emitted++
	}
	return emitted, warnings, nil
}

// wantsAny reports whether want accepts any of FactKeys.
func wantsAny(want func(key string) bool) bool {
	for _, key := range FactKeys {
		if want(key) {
			return true
		}
	}
	return false
}

// factFor is the fact a key's fingerprint is recorded under: its own type's,
// or for a host certificate its key's type's, named by
// remoteexec.HostKeyType, so an RSA key is rsa whatever it signs with. It
// reports false for a type no fact names (a security key's sk- types, or one
// a later library adds).
func factFor(key ssh.PublicKey) (string, bool) {
	name, ok := remoteexec.HostKeyType(plain(key))
	if !ok {
		return "", false
	}
	return Family + "host_key." + name, true
}

// plain is the key a certificate certifies, or key itself, since ssh-keygen
// -lf prints a host certificate's fingerprint as its key's.
func plain(key ssh.PublicKey) ssh.PublicKey {
	if cert, ok := key.(*ssh.Certificate); ok && cert.Key != nil {
		return cert.Key
	}
	return key
}
