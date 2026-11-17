// Legacy SSH algorithms against a real SSH server on loopback that offers only
// what an IOSvL2 15.2 switch offers (SHA-1 key exchange, an ssh-rsa host key):
// refused by default naming the property that allows it, not retried, never
// counted against the circuit, and reached once the record allows it, with a
// warning naming what was used.
package remoteexec

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"net"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/breaker"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// oldServer is a server offering only the algorithms of an old device.
type oldServer struct {
	target   Target
	hostKey  ssh.PublicKey
	accepted *int32
}

// startOldSSHServer starts a real SSH server on loopback whose only key
// exchanges are kex and whose only host key algorithm is ssh-rsa (SHA-1) when
// rsaSHA1Only is set, as an IOSvL2 15.2 switch offers. It accepts
// rightPassword and counts every connection.
func startOldSSHServer(t *testing.T, kex []string, rsaSHA1Only bool) oldServer {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	if rsaSHA1Only {
		if signer, err = ssh.NewSignerWithAlgorithms(signer.(ssh.AlgorithmSigner), []string{ssh.KeyAlgoRSA}); err != nil {
			t.Fatal(err)
		}
	}
	config := &ssh.ServerConfig{
		Config: ssh.Config{KeyExchanges: kex},
		PasswordCallback: func(_ ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
			if string(password) == rightPassword {
				return nil, nil
			}
			return nil, errors.New("password refused")
		},
	}
	config.AddHostKey(signer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to open a loopback listener: %v", err)
	}
	t.Cleanup(func() { listener.Close() })
	accepted := new(int32)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			atomic.AddInt32(accepted, 1)
			go serveOneFakeConnection(conn, config, func(string) (string, string, int) { return "ok\n", "", 0 })
		}
	}()
	addr := listener.Addr().(*net.TCPAddr)
	return oldServer{target: Target{Host: addr.IP.String(), Port: addr.Port}, hostKey: signer.PublicKey(), accepted: accepted}
}

// connectAs dials s with legacy allowed, verifying against a known_hosts
// file that trusts s's key.
func (s oldServer) connectAs(t *testing.T, r *Runner, legacy Legacy) (*Conn, error) {
	t.Helper()
	target := s.target
	target.Legacy = legacy
	return r.Connect(context.Background(), nil, target, PasswordAuth("u", rightPassword))
}

// knownRunner returns a Runner verifying against a file trusting s's key.
func (s oldServer) knownRunner(t *testing.T) *Runner {
	t.Helper()
	path := filepath.Join(t.TempDir(), "known_hosts")
	writeKnownHostsFor(t, path, s.target, s.hostKey)
	return New(Options{KnownHostsPath: path})
}

// TestConnect_AnOldDeviceIsRefusedNamingWhatAllowsIt proves a device offering
// only SHA-1 key exchange and an ssh-rsa host key is refused by default, once
// per call, naming each property in turn, and is reached when its record
// allows both, with warnings naming what the connection used.
func TestConnect_AnOldDeviceIsRefusedNamingWhatAllowsIt(t *testing.T) {
	s := startOldSSHServer(t, []string{ssh.InsecureKeyExchangeDH14SHA1}, true)
	r := s.knownRunner(t)

	before := atomic.LoadInt32(s.accepted)
	_, err := s.connectAs(t, r, Legacy{})
	var refused *LegacyRefusedError
	if !errors.As(err, &refused) || refused.Property != LegacyKeyExchangeProperty || !slices.Contains(refused.Offered, ssh.InsecureKeyExchangeDH14SHA1) {
		t.Fatalf("no legacy allowed: %v, want a refusal naming %s and the offer", err, LegacyKeyExchangeProperty)
	}
	if !strings.Contains(err.Error(), "set "+LegacyKeyExchangeProperty+": true") {
		t.Errorf("the refusal does not say how to allow it: %v", err)
	}
	if got := atomic.LoadInt32(s.accepted) - before; got != 1 {
		t.Errorf("a refused negotiation made %d handshakes, want 1: the server's offer is the same on every attempt", got)
	}
	var dialErr *DialError
	if !errors.As(err, &dialErr) || dialErr.Unreached() || !dialErr.Refused() {
		t.Errorf("a refused negotiation reads as unreached (%v): a wait would keep trying it", err)
	}

	// The key exchange alone is not enough: the host key is the next refusal.
	_, err = s.connectAs(t, r, Legacy{KeyExchange: true})
	if !errors.As(err, &refused) || refused.Property != LegacyHostKeysProperty || !slices.Equal(refused.Offered, []string{ssh.KeyAlgoRSA}) {
		t.Fatalf("legacy key exchange only: %v, want a refusal naming %s", err, LegacyHostKeysProperty)
	}

	conn, err := s.connectAs(t, r, Legacy{KeyExchange: true, HostKeys: true})
	if err != nil {
		t.Fatalf("both allowed: %v", err)
	}
	defer conn.Close()
	if res, err := conn.Run(context.Background(), "show version"); err != nil || res.Stdout != "ok\n" {
		t.Fatalf("a command after the legacy handshake: %+v, %v", res, err)
	}
	warnings := strings.Join(conn.LegacyWarnings("sw-l2"), "\n")
	for _, want := range []string{"used legacy SSH key exchange " + ssh.InsecureKeyExchangeDH14SHA1, "used legacy SSH host key algorithm " + ssh.KeyAlgoRSA} {
		if !strings.Contains(warnings, want) {
			t.Errorf("warnings %q do not say %q", warnings, want)
		}
	}
}

// TestConnect_ARefusedNegotiationDoesNotOpenTheCircuit proves refusals of an
// old device's offer never cost another caller its turn: after more of them
// than the breaker's threshold, the same Runner connects at once when the
// record allows the device's algorithms.
func TestConnect_ARefusedNegotiationDoesNotOpenTheCircuit(t *testing.T) {
	s := startOldSSHServer(t, []string{ssh.InsecureKeyExchangeDH14SHA1}, false)
	r := s.knownRunner(t)
	for i := 0; i < 2*breaker.DefaultThreshold; i++ {
		if _, err := s.connectAs(t, r, Legacy{}); errors.Is(err, breaker.ErrOpen) {
			t.Fatalf("refusal %d: the circuit opened: %v", i, err)
		}
	}
	conn, err := s.connectAs(t, r, Legacy{KeyExchange: true})
	if err != nil {
		t.Fatalf("connecting once the record allows the key exchange: %v", err)
	}
	_ = conn.Close()
}

// TestConnect_AFlagAModernDeviceDoesNotNeedIsWarned proves a legacy flag on
// a device that negotiates secure algorithms is used for nothing: the
// connection agrees to the secure ones, and the warning says the flag was not
// needed rather than that it was used.
func TestConnect_AFlagAModernDeviceDoesNotNeedIsWarned(t *testing.T) {
	s := startOldSSHServer(t, nil, false)
	r := s.knownRunner(t)
	conn, err := s.connectAs(t, r, Legacy{KeyExchange: true, HostKeys: true, Ciphers: true})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	warnings := conn.LegacyWarnings("modern")
	if len(warnings) != 3 {
		t.Fatalf("warnings %q, want one for each flag", warnings)
	}
	for _, w := range warnings {
		if !strings.Contains(w, "did not need it") {
			t.Errorf("%q says a legacy algorithm was used on a modern server", w)
		}
	}
	// No flag, no warning: the default says nothing.
	plain, err := s.connectAs(t, r, Legacy{})
	if err != nil {
		t.Fatal(err)
	}
	defer plain.Close()
	if w := plain.LegacyWarnings("modern"); w != nil {
		t.Errorf("a connection whose record allows nothing warned %q", w)
	}
}

// TestLegacy_TheDefaultOffersNoWeakAlgorithm proves the zero Legacy names
// none of the algorithms the library calls insecure, though the library's
// own default would offer SHA-1 key exchange, ssh-rsa and hmac-sha1-96, and
// that each flag adds its own kind and no other.
func TestLegacy_TheDefaultOffersNoWeakAlgorithm(t *testing.T) {
	insecure := ssh.InsecureAlgorithms()
	weak := slices.Concat(insecure.KeyExchanges, insecure.Ciphers, insecure.MACs, insecure.HostKeys)
	def := Legacy{}.algorithms()
	offered := slices.Concat(def.KeyExchanges, def.Ciphers, def.MACs, Legacy{}.hostKeys())
	for _, w := range weak {
		if slices.Contains(offered, w) {
			t.Errorf("the default offers %s", w)
		}
	}
	kex := Legacy{KeyExchange: true}.algorithms()
	if !slices.Contains(kex.KeyExchanges, ssh.InsecureKeyExchangeDH14SHA1) || slices.Contains(Legacy{KeyExchange: true}.hostKeys(), ssh.KeyAlgoRSA) || slices.Contains(kex.Ciphers, ssh.InsecureCipherAES128CBC) {
		t.Errorf("the key exchange flag offers %v and host keys %v", kex, Legacy{KeyExchange: true}.hostKeys())
	}
	ciphers := Legacy{Ciphers: true}.algorithms()
	if !slices.Contains(ciphers.Ciphers, ssh.InsecureCipherAES128CBC) || !slices.Contains(ciphers.MACs, ssh.InsecureHMACSHA196) || slices.Contains(ciphers.KeyExchanges, ssh.InsecureKeyExchangeDH14SHA1) {
		t.Errorf("the cipher flag offers %v", ciphers)
	}
	// Secure algorithms always come first.
	if kex.KeyExchanges[0] != def.KeyExchanges[0] {
		t.Errorf("the key exchange flag reordered the secure ones: %v", kex.KeyExchanges)
	}
}

// TestParseLegacy_TakesOnlyABoolean proves each flag reads true or false and
// refuses anything else, rather than reading "yes" either way.
func TestParseLegacy_TakesOnlyABoolean(t *testing.T) {
	cases := []struct {
		name  string
		props map[string]any
		want  Legacy
		bad   string
	}{
		{"none", nil, Legacy{}, ""},
		{"all true", map[string]any{LegacyKeyExchangeProperty: true, LegacyHostKeysProperty: true, LegacyCiphersProperty: true}, Legacy{true, true, true}, ""},
		{"false", map[string]any{LegacyKeyExchangeProperty: false}, Legacy{}, ""},
		{"a word", map[string]any{LegacyHostKeysProperty: "yes"}, Legacy{}, LegacyHostKeysProperty},
		{"a number", map[string]any{LegacyCiphersProperty: 1}, Legacy{}, LegacyCiphersProperty},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ParseLegacy(inventory.NewProperties(c.props))
			if c.bad != "" {
				if err == nil || !strings.Contains(err.Error(), c.bad) {
					t.Fatalf("ParseLegacy = %v, %v; want a refusal naming %s", got, err, c.bad)
				}
				return
			}
			if err != nil || got != c.want {
				t.Fatalf("ParseLegacy = %+v, %v; want %+v", got, err, c.want)
			}
		})
	}
}

// TestLegacy_WarningsNameEachFlag proves the static warnings name each flag
// a record sets, and say nothing for a record that sets none.
func TestLegacy_WarningsNameEachFlag(t *testing.T) {
	if w := (Legacy{}).Warnings("d"); w != nil {
		t.Errorf("no flag warned %q", w)
	}
	w := strings.Join(Legacy{KeyExchange: true, HostKeys: true, Ciphers: true}.Warnings("d"), "\n")
	for _, p := range LegacyProperties() {
		if !strings.Contains(w, p) {
			t.Errorf("warnings %q do not name %s", w, p)
		}
	}
}

// TestExplainNegotiation_NamesNoPropertyThatWouldNotHelp proves a server
// whose offer holds nothing this build implements is told so, rather than
// sent to set a property that would change nothing, and that a flag already
// set is not named again.
func TestExplainNegotiation_NamesNoPropertyThatWouldNotHelp(t *testing.T) {
	neg := &ssh.AlgorithmNegotiationError{What: "key exchange", RequestedAlgorithms: []string{"sntrup4591761x25519-sha512@tinyssh.org"}}
	err := ExplainNegotiation(neg, "10.0.0.1:22", Legacy{})
	var refused *LegacyRefusedError
	if !errors.As(err, &refused) || refused.Property != "" || !strings.Contains(err.Error(), "no key exchange algorithm this build implements") {
		t.Errorf("an unimplemented offer: %v", err)
	}
	cipher := &ssh.AlgorithmNegotiationError{What: "client to server cipher", RequestedAlgorithms: []string{ssh.InsecureCipherTripleDESCBC}}
	if err := ExplainNegotiation(cipher, "10.0.0.1:22", Legacy{}); !errors.As(err, &refused) || refused.Property != LegacyCiphersProperty {
		t.Errorf("a 3DES-only server: %v", err)
	}
	mac := &ssh.AlgorithmNegotiationError{What: "server to client MAC", RequestedAlgorithms: []string{ssh.InsecureHMACSHA196}}
	if err := ExplainNegotiation(mac, "10.0.0.1:22", Legacy{}); !errors.As(err, &refused) || refused.Property != LegacyCiphersProperty {
		t.Errorf("an hmac-sha1-96-only server: %v", err)
	}
	if err := ExplainNegotiation(mac, "10.0.0.1:22", Legacy{Ciphers: true}); !errors.As(err, &refused) || refused.Property != "" {
		t.Errorf("a flag already set was named again: %v", err)
	}
	other := errors.New("tcp dial: refused")
	if got := ExplainNegotiation(other, "10.0.0.1:22", Legacy{}); got != other {
		t.Errorf("another error was rewritten: %v", got)
	}
}

// propsItem is an inventory item that answers only Properties, all
// LegacyFor asks of one.
type propsItem struct {
	inventory.InventoryItem
	props map[string]any
}

// Properties returns the item's properties.
func (p propsItem) Properties() inventory.Properties { return inventory.NewProperties(p.props) }

// TestLegacyFor_AnUnreadableRecordAllowsNothing proves a device whose flags
// do not parse, and no device at all, are read as allowing nothing, while a
// readable record is read as it says.
func TestLegacyFor_AnUnreadableRecordAllowsNothing(t *testing.T) {
	if got := LegacyFor(nil); !got.IsZero() {
		t.Errorf("LegacyFor(nil) = %+v", got)
	}
	if got := LegacyFor(propsItem{props: map[string]any{LegacyKeyExchangeProperty: "yes", LegacyHostKeysProperty: true}}); !got.IsZero() {
		t.Errorf("an unreadable record = %+v, want nothing allowed", got)
	}
	if got := LegacyFor(propsItem{props: map[string]any{LegacyHostKeysProperty: true}}); got != (Legacy{HostKeys: true}) {
		t.Errorf("a readable record = %+v", got)
	}
}
