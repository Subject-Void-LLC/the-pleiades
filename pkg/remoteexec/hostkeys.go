// Package remoteexec: the host keys a connection's known_hosts check
// accepted, kept so a facts gatherer can record what the session verified
// (pkg/sshfacts) without dialing anything of its own.
//
// Only the target's own leg is kept, never a bastion's, and only the keys the
// known_hosts callback returned nil for. A connection that skipped host key
// verification keeps none and says it skipped (HostKeys.Verified), so a key no
// check accepted is never presented as the device's.
//
// The key is taken inside the host key callback, which golang.org/x/crypto/ssh
// calls only after the server has signed the key exchange with that key
// (handshake.go, verifyHostKeySignature before hostKeyCallback). So a key kept
// here was both trusted by known_hosts and proved by the server, unlike a TLS
// certificate seen in VerifyConnection (pkg/devicetls's presented.go). The
// library calls the callback again on every later key exchange of the same
// connection, and each key it accepts there is kept too.
//
// The keys travel on the Conn rather than on a context, because a connection a
// Pool lends again performs no handshake: a holder on the later task's context
// would see nothing, while the Conn still knows what its own handshake
// verified.
package remoteexec

import (
	"bytes"
	"context"
	"net"
	"sync"

	"golang.org/x/crypto/ssh"
)

// maxHostKeys bounds how many distinct accepted keys one connection keeps,
// so a server presenting a new key on every key exchange cannot grow it.
const maxHostKeys = 8

// hostKeyLog is what one connection's known_hosts check accepted on its
// target's leg: the keys of the dial attempt whose handshake became the
// session, with any later key exchange of that session, kept apart from the
// keys earlier attempts accepted before they failed. It is shared by every
// view of the connection (Raw, WithBecome) and by every Conn a Pool lends for
// it. It is safe for concurrent use: a key exchange later in the connection's
// life runs on the library's own goroutine.
type hostKeyLog struct {
	// verified is whether the leg's host key was checked at all; it is set
	// once, when the log is made, and never changes.
	verified bool

	mu sync.Mutex
	// keys are the distinct keys the current attempt accepted, in the order
	// first accepted: once the dial succeeds, the session's.
	keys []ssh.PublicKey
	// more is set once the current attempt accepted a key past maxHostKeys.
	more bool
	// earlier are the distinct keys attempts before the current one accepted,
	// at most maxHostKeys; earlierMore is set past that.
	earlier     []ssh.PublicKey
	earlierMore bool
}

// newHostKeyLog returns an empty log for a leg whose host key is checked
// (verified) or skipped.
func newHostKeyLog(verified bool) *hostKeyLog {
	return &hostKeyLog{verified: verified}
}

// beginAttempt starts a new dial attempt: what the attempt before it
// accepted becomes earlier, since that attempt's handshake did not become
// the session.
func (l *hostKeyLog) beginAttempt() {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, k := range l.keys {
		l.earlier, l.earlierMore = addDistinct(l.earlier, l.earlierMore, k)
	}
	l.earlierMore = l.earlierMore || l.more
	l.keys, l.more = nil, false
}

// add keeps key for the current attempt unless it is kept already.
func (l *hostKeyLog) add(key ssh.PublicKey) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.keys, l.more = addDistinct(l.keys, l.more, key)
}

// addDistinct appends key to keys unless an equal key is there, setting more
// instead once keys holds maxHostKeys.
func addDistinct(keys []ssh.PublicKey, more bool, key ssh.PublicKey) ([]ssh.PublicKey, bool) {
	if containsKey(keys, key) {
		return keys, more
	}
	if len(keys) == maxHostKeys {
		return keys, true
	}
	return append(keys, key), more
}

// containsKey reports whether keys holds a key equal to key.
func containsKey(keys []ssh.PublicKey, key ssh.PublicKey) bool {
	blob := key.Marshal()
	for _, k := range keys {
		if bytes.Equal(k.Marshal(), blob) {
			return true
		}
	}
	return false
}

// snapshot is the log as a HostKeys value. A nil log, a connection this
// package did not dial, is one that verified nothing.
func (l *hostKeyLog) snapshot() HostKeys {
	if l == nil {
		return HostKeys{}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	other := l.earlierMore
	for _, k := range l.earlier {
		other = other || !containsKey(l.keys, k)
	}
	return HostKeys{verified: l.verified, keys: append([]ssh.PublicKey(nil), l.keys...), more: l.more, otherAttempt: other}
}

// attemptsOf returns dial marking, on log, the start of each attempt it makes,
// so the keys an attempt that failed accepted are kept apart from the
// session's.
func attemptsOf(dial dialFunc, log *hostKeyLog) dialFunc {
	return func(ctx context.Context, addr string, config *ssh.ClientConfig) (*ssh.Client, error) {
		log.beginAttempt()
		return dial(ctx, addr, config)
	}
}

// keepAccepted returns cb recording on log each key cb accepts. It is applied
// only to a leg whose host key is checked, so a key the skip opt-in let
// through is never kept. A key cb refuses is not kept, and neither is the
// probe hostKeyAlgorithms asks cb about (hostkeyalgos.go), which no host holds
// and which known_hosts refuses anyway.
func keepAccepted(cb ssh.HostKeyCallback, log *hostKeyLog) ssh.HostKeyCallback {
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		if err := cb(hostname, remote, key); err != nil {
			return err
		}
		if !bytes.Equal(key.Marshal(), hostKeyProbe().Marshal()) {
			log.add(key)
		}
		return nil
	}
}

// HostKeys is what one connection's known_hosts check accepted for its target:
// whether the target's host key was checked at all, and the distinct keys it
// accepted. Its fields are unexported so only a dial in this package can say a
// key was verified; the zero value verified nothing.
type HostKeys struct {
	verified     bool
	keys         []ssh.PublicKey
	more         bool
	otherAttempt bool
}

// Verified reports whether the connection checked its target's host key
// against known_hosts. It is false for a connection made under the
// InsecureSkipHostKeyVerify opt-in, which accepted whatever key it was shown.
func (h HostKeys) Verified() bool { return h.verified }

// Keys returns each distinct key the check accepted on the dial attempt that
// became the session, and on its later key exchanges, in the order first
// accepted, and whether more were accepted than a connection keeps. A
// connection that skipped verification returns none.
func (h HostKeys) Keys() ([]ssh.PublicKey, bool) {
	return append([]ssh.PublicKey(nil), h.keys...), h.more
}

// OtherAttempt reports whether an earlier attempt of the same dial accepted a
// key the session did not prove itself with: the retry reached another server
// answering at the same address, which known_hosts also trusts.
func (h HostKeys) OtherAttempt() bool { return h.otherAttempt }

// hostKeyTypes names each plain host key type a fact can be recorded under.
// A key's type is not its signature algorithm: an RSA key is ssh-rsa whether
// it proves itself with SHA-1 or SHA-2, so one RSA key has one name. The table
// lives here because this is the one package allowed to name the legacy types
// (internal/archtest's TestOnlyRemoteexecWeakensSSH); naming one offers
// nothing, since what a connection offers is legacy.go's to decide.
var hostKeyTypes = map[string]string{
	ssh.KeyAlgoED25519:     "ed25519",
	ssh.KeyAlgoECDSA256:    "ecdsa_p256",
	ssh.KeyAlgoECDSA384:    "ecdsa_p384",
	ssh.KeyAlgoECDSA521:    "ecdsa_p521",
	ssh.KeyAlgoRSA:         "rsa",
	ssh.InsecureKeyAlgoDSA: "dsa",
}

// HostKeyType returns the short name of key's type (ed25519, ecdsa_p256,
// ecdsa_p384, ecdsa_p521, rsa or dsa), and false for any other type: a
// certificate (pass its Key), a security key's sk- types, or one a later
// library adds.
func HostKeyType(key ssh.PublicKey) (string, bool) {
	name, ok := hostKeyTypes[key.Type()]
	return name, ok
}

// VerifiedHostKeys returns what the known_hosts check of c's target accepted,
// on the dial that made c: never a bastion's key, and none when verification
// was skipped. A connection lent again by a Pool reports what its own
// handshake verified.
func (c *Conn) VerifiedHostKeys() HostKeys {
	if c == nil {
		return HostKeys{}
	}
	return c.hostKeys.snapshot()
}
