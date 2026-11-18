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
// The algorithms of those keys come first, then every other algorithm legacy
// allows. The order is a preference, not a restriction, on purpose: a server
// whose key really changed still presents another type, and the callback
// refuses it as a changed key (knownhosts.KeyError), which hostKeyRefused
// counts as an answer rather than a network failure.
//
// Only what legacy allows is ever offered (legacy.go): an RSA key is asked
// for with SHA-2 signatures, and with ssh-rsa (SHA-1) only on a device whose
// record allows legacy host keys. The list is never nil, since the library's
// own default would offer ssh-rsa and DSA. The allowed list comes back in the
// library's order when verification is skipped (cb accepts any key), when the
// host has no entry (the handshake refuses it as unknown), and when an entry
// for the host is an @cert-authority line, since such a host presents a
// certificate and that order already puts certificate algorithms first.
func hostKeyAlgorithms(cb ssh.HostKeyCallback, addr string, legacy Legacy) []string {
	allowed := legacy.hostKeys()
	if cb == nil {
		return allowed
	}
	// The remote address is read only when addr is empty, which it never is
	// here, so any well-formed address will do.
	err := cb(addr, &net.TCPAddr{IP: net.IPv4zero}, hostKeyProbe())
	var keyErr *knownhosts.KeyError
	if !errors.As(err, &keyErr) || len(keyErr.Want) == 0 {
		return allowed
	}
	if anyCertAuthority(keyErr.Want) {
		return allowed
	}

	var preferred []string
	for _, known := range keyErr.Want {
		for _, algo := range algorithmsForKeyType(known.Key.Type(), allowed) {
			if !slices.Contains(preferred, algo) {
				preferred = append(preferred, algo)
			}
		}
	}
	// A key type no allowed algorithm proves leaves the order alone, and the
	// handshake reports the host's key as before.
	return appendNew(preferred, allowed...)
}

// algorithmsForKeyType returns the host key algorithms among allowed that
// prove possession of a plain key of keyType, in the library's order of
// preference. An RSA key signs with SHA-2 first and SHA-1 last.
func algorithmsForKeyType(keyType string, allowed []string) []string {
	var candidates []string
	switch keyType {
	case ssh.KeyAlgoRSA:
		candidates = []string{ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256, ssh.KeyAlgoRSA}
	default:
		// Any other type (an ECDSA, ed25519 or DSA key, a security key type)
		// is proved by the algorithm of its own name.
		candidates = []string{keyType}
	}
	var out []string
	for _, algo := range candidates {
		if slices.Contains(allowed, algo) {
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
