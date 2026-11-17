// Package remoteexec: the legacy SSH algorithms a device's record may allow.
//
// golang.org/x/crypto/ssh still offers some algorithms it calls insecure when
// a client names none of its own: SHA-1 Diffie-Hellman key exchange
// (diffie-hellman-group14-sha1), SHA-1 RSA host key signatures (ssh-rsa), DSA
// host keys and the truncated hmac-sha1-96 MAC. This package never takes that
// default. Every connection names its algorithms itself, the library's secure
// set (ssh.SupportedAlgorithms) and nothing more, so a device that can do
// better is never talked down by a machine in the middle, and one that cannot
// is refused with a message naming what it offered.
//
// An old device that offers nothing better (an IOSvL2 15.2 switch offers only
// SHA-1 key exchange and an ssh-rsa host key) is reached through a per-device
// opt-in on its record, one per kind, as pkg/devicetls does for TLS and
// pkg/snmp for SNMP: ssh_allow_legacy_key_exchange, ssh_allow_legacy_host_keys
// and ssh_allow_legacy_ciphers. One weakening never brings another with it,
// each is reported as a warning on every connection that has it, and the
// names of the weak algorithms are written in this package alone, which
// internal/archtest holds.
package remoteexec

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"golang.org/x/crypto/ssh"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// The device properties that allow legacy SSH algorithms, each false unless
// set to true.
const (
	// LegacyKeyExchangeProperty allows SHA-1 Diffie-Hellman key exchange:
	// diffie-hellman-group14-sha1, diffie-hellman-group-exchange-sha1 and
	// diffie-hellman-group1-sha1 (a 1024-bit group).
	LegacyKeyExchangeProperty = "ssh_allow_legacy_key_exchange"
	// LegacyHostKeysProperty allows a host to prove itself with a SHA-1 RSA
	// signature (ssh-rsa) or a DSA key (ssh-dss), and their certificates.
	LegacyHostKeysProperty = "ssh_allow_legacy_host_keys"
	// LegacyCiphersProperty allows the CBC, 3DES and RC4 ciphers and the
	// truncated hmac-sha1-96 MAC.
	LegacyCiphersProperty = "ssh_allow_legacy_ciphers"
)

// LegacyProperties returns every property Legacy is read from, for a device
// type's dispatch allowlist.
func LegacyProperties() []string {
	return []string{LegacyKeyExchangeProperty, LegacyHostKeysProperty, LegacyCiphersProperty}
}

// Legacy is which legacy SSH algorithms one device's record allows. The zero
// Legacy allows none, and is what every connection gets unless its Target
// says otherwise. It is comparable, so a Pool can key connections by it: a
// connection that negotiated a legacy algorithm is never lent to a call for a
// device whose record does not allow it.
type Legacy struct {
	// KeyExchange allows SHA-1 Diffie-Hellman key exchange.
	KeyExchange bool
	// HostKeys allows ssh-rsa (SHA-1) and DSA host key algorithms.
	HostKeys bool
	// Ciphers allows the CBC, 3DES and RC4 ciphers and hmac-sha1-96.
	Ciphers bool
}

// ParseLegacy reads Legacy from a device's properties. Each flag must be a
// boolean when present: a weakening is asked for with true, and "yes" or
// "1" is refused rather than read either way.
func ParseLegacy(props inventory.Properties) (Legacy, error) {
	raw := props.Raw()
	var l Legacy
	for _, flag := range []struct {
		key string
		to  *bool
	}{
		{LegacyKeyExchangeProperty, &l.KeyExchange},
		{LegacyHostKeysProperty, &l.HostKeys},
		{LegacyCiphersProperty, &l.Ciphers},
	} {
		v, present := raw[flag.key]
		if !present {
			continue
		}
		b, ok := v.(bool)
		if !ok {
			return Legacy{}, fmt.Errorf("property %s must be true or false", flag.key)
		}
		*flag.to = b
	}
	return l, nil
}

// LegacyFor returns what device's record allows, and the zero Legacy for a
// nil device or a record whose flags do not parse. The inventory refuses such
// a record when it is loaded (internal/inventory's ItemFactory), so the
// second case is a device built some other way; allowing nothing is the safe
// reading of it, and its connection is then refused naming the property.
func LegacyFor(device inventory.InventoryItem) Legacy {
	if device == nil {
		return Legacy{}
	}
	l, err := ParseLegacy(device.Properties())
	if err != nil {
		return Legacy{}
	}
	return l
}

// IsZero reports whether l allows no legacy algorithm.
func (l Legacy) IsZero() bool { return l == Legacy{} }

// legacyKeyExchanges are the SHA-1 key exchanges, strongest group first.
func legacyKeyExchanges() []string {
	return []string{ssh.InsecureKeyExchangeDH14SHA1, ssh.InsecureKeyExchangeDHGEXSHA1, ssh.InsecureKeyExchangeDH1SHA1}
}

// legacyHostKeys are the SHA-1 RSA and DSA host key algorithms.
func legacyHostKeys() []string { return ssh.InsecureAlgorithms().HostKeys }

// legacyCiphers are the CBC, 3DES and RC4 ciphers.
func legacyCiphers() []string { return ssh.InsecureAlgorithms().Ciphers }

// legacyMACs is the truncated SHA-1 MAC.
func legacyMACs() []string { return ssh.InsecureAlgorithms().MACs }

// keyExchanges returns the key exchanges a connection offers, secure ones
// first.
func (l Legacy) keyExchanges() []string {
	out := ssh.SupportedAlgorithms().KeyExchanges
	if l.KeyExchange {
		out = appendNew(out, legacyKeyExchanges()...)
	}
	return out
}

// ciphers returns the ciphers a connection offers, secure ones first.
func (l Legacy) ciphers() []string {
	out := ssh.SupportedAlgorithms().Ciphers
	if l.Ciphers {
		out = appendNew(out, legacyCiphers()...)
	}
	return out
}

// macs returns the MACs a connection offers, secure ones first.
func (l Legacy) macs() []string {
	out := ssh.SupportedAlgorithms().MACs
	if l.Ciphers {
		out = appendNew(out, legacyMACs()...)
	}
	return out
}

// hostKeys returns every host key algorithm a connection may accept, in the
// library's order of preference, secure ones first.
func (l Legacy) hostKeys() []string {
	out := ssh.SupportedAlgorithms().HostKeys
	if l.HostKeys {
		out = appendNew(out, legacyHostKeys()...)
	}
	return out
}

// algorithms returns the key exchanges, ciphers and MACs a connection offers,
// for an ssh.ClientConfig's embedded Config.
func (l Legacy) algorithms() ssh.Config {
	return ssh.Config{KeyExchanges: l.keyExchanges(), Ciphers: l.ciphers(), MACs: l.macs()}
}

// ScanConfig returns the client configuration a host key scan (trust-host
// --first-connect) negotiates with, under l: the key exchanges, ciphers and
// MACs a connection offers, and one group of host key algorithms per kind of
// key, so each handshake shows one key and the host's keys are seen one kind
// at a time, as ssh-keyscan does. An RSA key is asked for with SHA-2
// signatures first and ssh-rsa last, so a host that can do better is never
// asked for SHA-1.
func (l Legacy) ScanConfig() (ssh.Config, [][]string) {
	rsa := []string{ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256}
	groups := [][]string{{ssh.KeyAlgoED25519}, {ssh.KeyAlgoECDSA256}, {ssh.KeyAlgoECDSA384}, {ssh.KeyAlgoECDSA521}}
	if l.HostKeys {
		rsa = append(rsa, ssh.KeyAlgoRSA)
		groups = append(groups, rsa, []string{ssh.InsecureKeyAlgoDSA})
	} else {
		groups = append(groups, rsa)
	}
	return l.algorithms(), groups
}

// appendNew appends each of extra to list that list does not already hold.
func appendNew(list []string, extra ...string) []string {
	for _, e := range extra {
		if !slices.Contains(list, e) {
			list = append(list, e)
		}
	}
	return list
}

// Warnings says, for the named device, what each weakening l allows means.
// It is empty for the zero Legacy. A connection's own warnings
// (Conn.LegacyWarnings) also say whether it used them.
func (l Legacy) Warnings(device string) []string {
	var out []string
	if l.KeyExchange {
		out = append(out, fmt.Sprintf("device %q allows legacy SSH key exchange (%s): SHA-1 Diffie-Hellman is open to known attacks; keep it only while the device cannot be upgraded", device, LegacyKeyExchangeProperty))
	}
	if l.HostKeys {
		out = append(out, fmt.Sprintf("device %q allows legacy SSH host keys (%s): an ssh-rsa (SHA-1) or DSA signature is a weaker proof of the device's identity; keep it only while the device cannot be upgraded", device, LegacyHostKeysProperty))
	}
	if l.Ciphers {
		out = append(out, fmt.Sprintf("device %q allows legacy SSH ciphers (%s): CBC, 3DES and RC4 ciphers and hmac-sha1-96 are broken or weak; keep it only while the device cannot be upgraded", device, LegacyCiphersProperty))
	}
	return out
}

// legacyUse is what one connection negotiated that only l allowed, by kind.
type legacyUse struct {
	keyExchange, hostKey string
	ciphers              []string
}

// usedLegacy returns which of a connection's negotiated algorithms are legacy
// ones.
func usedLegacy(algs ssh.NegotiatedAlgorithms) legacyUse {
	var use legacyUse
	if slices.Contains(legacyKeyExchanges(), algs.KeyExchange) {
		use.keyExchange = algs.KeyExchange
	}
	if slices.Contains(legacyHostKeys(), algs.HostKey) {
		use.hostKey = algs.HostKey
	}
	for _, name := range []string{algs.Read.Cipher, algs.Write.Cipher, algs.Read.MAC, algs.Write.MAC} {
		if (slices.Contains(legacyCiphers(), name) || slices.Contains(legacyMACs(), name)) && !slices.Contains(use.ciphers, name) {
			use.ciphers = append(use.ciphers, name)
		}
	}
	return use
}

// connectionWarnings says, for the named device, what one connection that
// negotiated algs under l did with each weakening l allows: that it used one,
// naming it, or that it did not need it, which the record should then drop.
// A flag a connection did not need is a warning and never a refusal: only a
// connection can tell, and one device may serve SSH on two ports with two
// different servers (a Catalyst 8000V runs Cisco's SSH on 22 and OpenSSH on
// 830), so another connection to the same device may need it.
func connectionWarnings(device string, l Legacy, algs ssh.NegotiatedAlgorithms) []string {
	use := usedLegacy(algs)
	var out []string
	add := func(allowed bool, used, property, kind, why string) {
		switch {
		case !allowed:
		case used != "":
			out = append(out, fmt.Sprintf("device %q used legacy SSH %s %s (%s): %s; keep it only while the device cannot be upgraded", device, kind, used, property, why))
		default:
			out = append(out, fmt.Sprintf("device %q allows legacy SSH %s (%s), and this connection did not need it: remove the property unless another of its SSH servers does", device, kind, property))
		}
	}
	add(l.KeyExchange, use.keyExchange, LegacyKeyExchangeProperty, "key exchange", "SHA-1 Diffie-Hellman is open to known attacks")
	add(l.HostKeys, use.hostKey, LegacyHostKeysProperty, "host key algorithm", "an ssh-rsa (SHA-1) or DSA signature is a weaker proof of the device's identity")
	add(l.Ciphers, strings.Join(use.ciphers, ", "), LegacyCiphersProperty, "cipher", "CBC, 3DES and RC4 ciphers and hmac-sha1-96 are broken or weak")
	return out
}

// LegacyRefusedError is a handshake that found no algorithm in common with
// the server, and names what the server offered and what would allow it.
type LegacyRefusedError struct {
	// Addr is the address dialed.
	Addr string
	// What is the kind of algorithm with nothing in common ("key exchange",
	// "host key", "client to server cipher" and so on), as the library names it.
	What string
	// Offered is what the server offered for it.
	Offered []string
	// Property is the device property that would allow the legacy ones among
	// Offered, empty when none of them is one this build implements.
	Property string
	err      error
}

// Error names the server's offer and the property that allows it, or says
// that nothing this build implements would.
func (e *LegacyRefusedError) Error() string {
	offered := strings.Join(e.Offered, ", ")
	if e.Property == "" {
		return fmt.Sprintf("the SSH server at %s offers no %s algorithm this build implements (it offered %s): %v", e.Addr, e.What, offered, e.err)
	}
	return fmt.Sprintf("the SSH server at %s offers only legacy %s algorithms (%s), which this device's record does not allow: set %s: true on the device to allow them, knowing they are weak, and it is warned about on every connection: %v",
		e.Addr, e.What, offered, e.Property, e.err)
}

// Unwrap exposes the library's own ssh.AlgorithmNegotiationError.
func (e *LegacyRefusedError) Unwrap() error { return e.err }

// ExplainNegotiation returns err as a LegacyRefusedError when it is a
// handshake that found no algorithm in common, and err itself otherwise.
func ExplainNegotiation(err error, addr string, l Legacy) error {
	var neg *ssh.AlgorithmNegotiationError
	if err == nil || !errors.As(err, &neg) {
		return err
	}
	var weak []string
	allowed := true
	property := ""
	switch {
	case neg.What == "key exchange":
		weak, allowed, property = legacyKeyExchanges(), l.KeyExchange, LegacyKeyExchangeProperty
	case neg.What == "host key":
		weak, allowed, property = legacyHostKeys(), l.HostKeys, LegacyHostKeysProperty
	case strings.HasSuffix(neg.What, "cipher"):
		weak, allowed, property = legacyCiphers(), l.Ciphers, LegacyCiphersProperty
	case strings.HasSuffix(neg.What, "MAC"):
		weak, allowed, property = legacyMACs(), l.Ciphers, LegacyCiphersProperty
	}
	refused := &LegacyRefusedError{Addr: addr, What: neg.What, Offered: slices.Clone(neg.RequestedAlgorithms), err: err}
	// The property is named only when it would help: the server offers one
	// of the legacy algorithms it allows, and the record does not allow them.
	if !allowed && slices.ContainsFunc(neg.RequestedAlgorithms, func(a string) bool { return slices.Contains(weak, a) }) {
		refused.Property = property
	}
	return refused
}

// negotiationRefused reports whether err is a handshake that found no
// algorithm in common: an answer, not the network failing, since the
// server's offer is the same on every attempt.
func negotiationRefused(err error) bool {
	var neg *ssh.AlgorithmNegotiationError
	return errors.As(err, &neg)
}
