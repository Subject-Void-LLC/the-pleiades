// Package remoteexec: choosing which host key algorithms one connection
// offers its server, from the keys known_hosts already holds for that host.
//
// Without this the client offers golang.org/x/crypto/ssh's own default order,
// which puts ECDSA ahead of ed25519. A server that has both answers with its
// ECDSA key, and a known_hosts file that pins only the host's ed25519 key then
// refuses the connection as a changed key, although the key it holds is right.
// OpenSSH avoids this by putting the algorithms of the keys it already knows
// for the host first (sshconnect2.c, order_hostkeyalgs), so the server proves
// itself with a key the client can check. This file does the same.
package remoteexec

import (
	"bufio"
	"crypto/ed25519"
	"errors"
	"net"
	"os"
	"slices"
	"strings"
	"sync"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// probeKeyOnce guards probeKey, the throwaway key hostKeyAlgorithms asks the
// known_hosts callback about.
var (
	probeKeyOnce sync.Once
	probeKey     ssh.PublicKey
)

// hostKeyProbe returns a public key that no known_hosts file holds: an ed25519
// key made from a fixed all-zero seed is a valid key whose private half is
// public knowledge, so no real host uses it, and it never needs to verify
// anything. It exists only so the callback answers with the keys it does hold.
func hostKeyProbe() ssh.PublicKey {
	probeKeyOnce.Do(func() {
		priv := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
		// The conversion cannot fail for an ed25519 public key.
		probeKey, _ = ssh.NewPublicKey(priv.Public())
	})
	return probeKey
}

// hostKeyAlgorithms returns the host key algorithms the connection to addr
// should offer, in order, or nil to keep the library's default order.
//
// It asks cb, the connection's own known_hosts callback, about a key no host
// holds. The callback then reports every key it does hold for addr
// (knownhosts.KeyError.Want), matched by the library's own rules: hashed
// names, wildcards, negations and the bracketed host and port form. So this
// can never disagree with the check the handshake runs.
//
// The algorithms of those keys come first, then every other algorithm the
// library offers by default. The order is a preference, not a restriction, on
// purpose: a server whose key really changed still presents another type, and
// the callback refuses it as a changed key (knownhosts.KeyError), which
// hostKeyRefused counts as an answer rather than a network failure. A list
// cut down to the known types would instead end in "no common algorithm", a
// handshake failure that is retried and charged to the address's circuit.
//
// nil comes back, so nothing changes, when verification is skipped (cb accepts
// any key), when the host has no entry (the handshake refuses it as unknown),
// and when an entry for the host is an @cert-authority line, since such a
// host presents a certificate and the default order already puts certificate
// algorithms first.
func hostKeyAlgorithms(cb ssh.HostKeyCallback, addr string) []string {
	if cb == nil {
		return nil
	}
	// The remote address is read only when addr is empty, which it never is
	// here, so any well-formed address will do.
	err := cb(addr, &net.TCPAddr{IP: net.IPv4zero}, hostKeyProbe())
	var keyErr *knownhosts.KeyError
	if !errors.As(err, &keyErr) || len(keyErr.Want) == 0 {
		return nil
	}
	if anyCertAuthority(keyErr.Want) {
		return nil
	}

	var preferred []string
	for _, known := range keyErr.Want {
		for _, algo := range algorithmsForKeyType(known.Key.Type()) {
			if !slices.Contains(preferred, algo) {
				preferred = append(preferred, algo)
			}
		}
	}
	if len(preferred) == 0 {
		// A key type this library cannot negotiate as a host key: leave the
		// order alone, and the handshake reports the host's key as before.
		return nil
	}
	out := preferred
	for _, algo := range defaultHostKeyAlgorithms() {
		if !slices.Contains(out, algo) {
			out = append(out, algo)
		}
	}
	return out
}

// algorithmsForKeyType returns the host key algorithms that prove possession
// of a plain key of keyType, in the library's order of preference. An RSA key
// signs with SHA-2 first and SHA-1 last, as the library's own default does.
func algorithmsForKeyType(keyType string) []string {
	switch keyType {
	case ssh.KeyAlgoRSA:
		return []string{ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256, ssh.KeyAlgoRSA}
	case ssh.KeyAlgoECDSA256, ssh.KeyAlgoECDSA384, ssh.KeyAlgoECDSA521, ssh.KeyAlgoED25519:
		return []string{keyType}
	}
	// Anything else (a DSA key, a security key type) is offered only if the
	// library offers it at all; defaultHostKeyAlgorithms decides that.
	if slices.Contains(defaultHostKeyAlgorithms(), keyType) {
		return []string{keyType}
	}
	return nil
}

// defaultHostKeyAlgorithms returns every host key algorithm the library
// negotiates when a client sets none: its supported list followed by the ones
// it marks insecure but still offers by default (SHA-1 RSA, DSA), so a device
// that has only an old RSA key is reached exactly as it was before this file.
func defaultHostKeyAlgorithms() []string {
	out := ssh.SupportedAlgorithms().HostKeys
	for _, algo := range ssh.InsecureAlgorithms().HostKeys {
		if !slices.Contains(out, algo) {
			out = append(out, algo)
		}
	}
	return out
}

// anyCertAuthority reports whether any of keys came from an @cert-authority
// line. knownhosts reports such lines among a host's keys without saying which
// they are, so the line each one names is read back from its file.
func anyCertAuthority(keys []knownhosts.KnownKey) bool {
	lines := map[string]map[int]bool{}
	for _, k := range keys {
		if lines[k.Filename] == nil {
			lines[k.Filename] = certAuthorityLines(k.Filename)
		}
		if lines[k.Filename][k.Line] {
			return true
		}
	}
	return false
}

// certAuthorityLines returns the 1-based numbers of the lines in path that
// start with the @cert-authority marker, the way knownhosts numbers them. A
// file that cannot be read again returns an empty set, so the caller keeps the
// preference it computed: the same file was read a moment ago to build the
// callback, and the handshake's own check is unchanged either way.
func certAuthorityLines(path string) map[int]bool {
	out := map[int]bool{}
	f, err := os.Open(path) // #nosec G304 -- the known_hosts file the callback was just built from
	if err != nil {
		return out
	}
	defer func() { _ = f.Close() }()
	scanner := bufio.NewScanner(f)
	for n := 1; scanner.Scan(); n++ {
		if strings.HasPrefix(strings.TrimSpace(scanner.Text()), "@cert-authority") {
			out[n] = true
		}
	}
	return out
}
