package remoteexec_test

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
)

// TestLiveNetconfSubsystemShapes is a diagnostic, not a Release Gate. It
// serves two purposes at once, which is why it lives here rather than in
// pkg/netconf: it is the first proof that Conn.Subsystem reaches a real
// device at all, and it captures the raw bytes a real Cisco IOS XE
// NETCONF server sends before any client parses them, so RFC 6242
// framing selection and RFC 6241 capability negotiation are written
// against a device's own answer instead of against a recollection of
// what a hello is supposed to look like.
//
// That sequencing is the whole point. pkg/netcli's own live probe exists
// because Phase 86.5's predecessor spec fabricated IOS prompt shapes it
// had never observed (FAILURE_PATTERNS.md #202), and a NETCONF hello has
// more guessable-looking detail in it than a CLI prompt does: which
// base capabilities are advertised, whether :candidate is offered,
// whether the server sends its hello before or after the client's.
// Every one of those is a fact about a device.
//
// It is READ-ONLY and applies no configuration. It sends exactly one
// client hello and then closes the session; it never sends an <rpc>, so
// there is nothing for a device reload to undo. That matters because the
// DevNet sandbox is shared with every other user of it.
//
// Gated on PLEIADES_E2E_IOS, skipping rather than failing when unset, and
// taking credentials from the environment only. The sandbox issues a
// fresh password per reservation, so a literal in this file would be
// both a leaked live credential and a test that silently rots.
func TestLiveNetconfSubsystemShapes(t *testing.T) {
	if os.Getenv("PLEIADES_E2E_IOS") == "" {
		t.Skip("set PLEIADES_E2E_IOS=1 to run this live diagnostic against a real Cisco IOS XE device")
	}
	user := os.Getenv("PLEIADES_E2E_IOS_USER")
	pass := os.Getenv("PLEIADES_E2E_IOS_PASS")
	if user == "" || pass == "" {
		t.Skip("PLEIADES_E2E_IOS_USER and PLEIADES_E2E_IOS_PASS must both be set (no default credential: the sandbox issues a unique password per reservation)")
	}
	host := os.Getenv("PLEIADES_E2E_IOS_HOST")
	if host == "" {
		host = "devnetsandboxiosxec8k.cisco.com"
	}

	runner := remoteexec.New(remoteexec.Options{InsecureSkipHostKeyVerify: true})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Both ports are probed, and which of them actually serves the
	// subsystem is one of the facts this diagnostic exists to establish
	// rather than assume. NETCONF over SSH is a SUBSYSTEM on an ordinary
	// SSH connection (RFC 6242 section 2), so port 22 SHOULD work and
	// the dedicated 830 listener SHOULD be a convention; whether a given
	// IOS XE build agrees is a question about the device, and
	// NetconfCapable's own NetconfPort accessor exists precisely because
	// the answer is per-device configuration.
	var (
		sub      *remoteexec.Subsystem
		hello    string
		livePort int
	)
	for _, port := range []int{830, 22} {
		conn, err := runner.Connect(ctx, nil, remoteexec.Target{Host: host, Port: port}, remoteexec.PasswordAuth(user, pass))
		if err != nil {
			t.Logf("port %d: Connect: %v", port, err)
			continue
		}
		defer conn.Close()

		candidate, err := conn.Subsystem(ctx, "netconf")
		if err != nil {
			t.Logf("port %d: Subsystem(netconf) refused: %v", port, err)
			continue
		}

		// A subsystem request the server ACCEPTS and then immediately
		// ends is a real and distinct outcome from one it refuses, and
		// telling them apart is the reason the read happens here rather
		// than after the loop: a client that only checked the request
		// reply would report this device as working.
		body, err := readUntilDelimiter(candidate, "]]>]]>", 1<<20)
		if err != nil {
			t.Logf("port %d: subsystem accepted, but reading the server hello failed: %v (read %d bytes, stderr: %q)",
				port, err, len(body), candidate.Stderr())
			candidate.Close()
			continue
		}
		sub, hello, livePort = candidate, body, port
		break
	}
	if sub == nil {
		t.Fatal("no NETCONF hello could be read on either port 830 or port 22")
	}
	defer sub.Close()
	t.Logf("NETCONF answered on port %d", livePort)

	// The hello above was read with the base:1.0 end-of-message
	// delimiter. A server MUST frame its hello that way regardless of
	// which base version it goes on to negotiate (RFC 6242 section 4.1:
	// chunked framing starts only AFTER both hellos), so that is safe to
	// assume for the hello and for nothing after it.
	t.Logf("=== SERVER HELLO (%d bytes) ===\n%s", len(hello), hello)

	// The three facts the client's negotiation is about to be written
	// against, called out individually so they appear in the log even
	// when the hello itself is long enough to skim past.
	for _, probe := range []struct{ label, needle string }{
		{"base:1.0 advertised", "urn:ietf:params:netconf:base:1.0"},
		{"base:1.1 advertised", "urn:ietf:params:netconf:base:1.1"},
		{":candidate advertised", "urn:ietf:params:netconf:capability:candidate:1.0"},
		{":writable-running advertised", "urn:ietf:params:netconf:capability:writable-running:1.0"},
		{":validate advertised", "urn:ietf:params:netconf:capability:validate"},
		{":rollback-on-error advertised", "urn:ietf:params:netconf:capability:rollback-on-error:1.0"},
	} {
		t.Logf("%-32s %v", probe.label+":", strings.Contains(hello, probe.needle))
	}

	if sessionID := between(hello, "<session-id>", "</session-id>"); sessionID != "" {
		t.Logf("%-32s %s", "session-id:", sessionID)
	}

	// Send a client hello so the session ends cleanly from the server's
	// point of view rather than as an abandoned channel. Deliberately
	// base:1.0 only: this probe reads nothing after the hello, so
	// negotiating chunked framing here would prove nothing and would
	// leave the log harder to read.
	clientHello := `<?xml version="1.0" encoding="UTF-8"?>` +
		`<hello xmlns="urn:ietf:params:xml:ns:netconf:base:1.0">` +
		`<capabilities><capability>urn:ietf:params:netconf:base:1.0</capability></capabilities>` +
		`</hello>]]>]]>`
	if _, err := io.WriteString(sub, clientHello); err != nil {
		t.Fatalf("writing the client hello: %v", err)
	}
	t.Log("client hello sent; session closing without an <rpc>, so nothing was changed on the device")
}

// readUntilDelimiter reads from r until delim appears, returning
// everything up to but not including it. It is a test fixture, not a
// preview of pkg/netconf's own framing reader: this one buffers the
// whole message, which is exactly what a real client must not do.
func readUntilDelimiter(r io.Reader, delim string, maxBytes int) (string, error) {
	var buf []byte
	chunk := make([]byte, 4096)
	for {
		if i := strings.Index(string(buf), delim); i >= 0 {
			return string(buf[:i]), nil
		}
		if len(buf) >= maxBytes {
			return "", io.ErrShortBuffer
		}
		n, err := r.Read(chunk)
		if n > 0 {
			buf = append(buf, chunk[:n]...)
		}
		if err != nil {
			return string(buf), err
		}
	}
}

// between returns the text between the first open and the next close,
// or the empty string when either is absent.
func between(s, open, close string) string {
	i := strings.Index(s, open)
	if i < 0 {
		return ""
	}
	rest := s[i+len(open):]
	j := strings.Index(rest, close)
	if j < 0 {
		return ""
	}
	return rest[:j]
}

// TestLiveNetconfChunkedFramingAndErrorShapes is the second half of the
// diagnostic: it negotiates base:1.1, exchanges one read-only RPC under
// RFC 6242 chunked framing, and deliberately provokes an <rpc-error>, so
// pkg/netconf's chunk codec and its error type are both written against
// a real device's bytes rather than against the RFC's prose alone.
//
// The framing helpers below are TEST FIXTURES and deliberately naive:
// they buffer whole messages, which is exactly what the real codec must
// not do. Writing the encoder twice, once here against the RFC and once
// for real in pkg/netconf, is a deliberate cross-check: if the two
// disagree, the device is the tie-breaker.
//
// READ-ONLY. The only RPCs sent are <get-config> with a subtree filter,
// one deliberately invalid <rpc> to observe the error convention, and
// <close-session>. Nothing is written to the device's configuration.
func TestLiveNetconfChunkedFramingAndErrorShapes(t *testing.T) {
	if os.Getenv("PLEIADES_E2E_IOS") == "" {
		t.Skip("set PLEIADES_E2E_IOS=1 to run this live diagnostic against a real Cisco IOS XE device")
	}
	user := os.Getenv("PLEIADES_E2E_IOS_USER")
	pass := os.Getenv("PLEIADES_E2E_IOS_PASS")
	if user == "" || pass == "" {
		t.Skip("PLEIADES_E2E_IOS_USER and PLEIADES_E2E_IOS_PASS must both be set (no default credential: the sandbox issues a unique password per reservation)")
	}
	host := os.Getenv("PLEIADES_E2E_IOS_HOST")
	if host == "" {
		host = "devnetsandboxiosxec8k.cisco.com"
	}

	runner := remoteexec.New(remoteexec.Options{InsecureSkipHostKeyVerify: true})
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	conn, err := runner.Connect(ctx, nil, remoteexec.Target{Host: host, Port: 830}, remoteexec.PasswordAuth(user, pass))
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer conn.Close()

	sub, err := conn.Subsystem(ctx, "netconf")
	if err != nil {
		t.Fatalf("Subsystem(netconf): %v", err)
	}
	defer sub.Close()

	if _, err := readUntilDelimiter(sub, "]]>]]>", 1<<20); err != nil {
		t.Fatalf("reading the server hello: %v", err)
	}

	// Client hello advertising base:1.1, which switches BOTH directions
	// to chunked framing immediately after this message. The hello
	// itself is still delimiter-framed (RFC 6242 section 4.1).
	clientHello := `<?xml version="1.0" encoding="UTF-8"?>` +
		`<hello xmlns="urn:ietf:params:xml:ns:netconf:base:1.0"><capabilities>` +
		`<capability>urn:ietf:params:netconf:base:1.0</capability>` +
		`<capability>urn:ietf:params:netconf:base:1.1</capability>` +
		`</capabilities></hello>]]>]]>`
	if _, err := io.WriteString(sub, clientHello); err != nil {
		t.Fatalf("writing the client hello: %v", err)
	}

	// A deliberately tiny subtree filter: the device's hostname. A bare
	// <get-config> on a real router returns megabytes, which would tell
	// us nothing extra about framing and would be unkind to a shared
	// sandbox.
	getConfig := `<?xml version="1.0" encoding="UTF-8"?>` +
		`<rpc message-id="101" xmlns="urn:ietf:params:xml:ns:netconf:base:1.0">` +
		`<get-config><source><running/></source><filter type="subtree">` +
		`<native xmlns="http://cisco.com/ns/yang/Cisco-IOS-XE-native"><hostname/></native>` +
		`</filter></get-config></rpc>`
	if err := writeChunked(sub, getConfig); err != nil {
		t.Fatalf("writing get-config: %v", err)
	}
	reply, raw, err := readChunked(sub, 1<<22)
	if err != nil {
		t.Fatalf("reading the get-config reply: %v (stderr: %q)", err, sub.Stderr())
	}
	t.Logf("=== RAW CHUNK FRAMING ON THE WIRE (%d bytes) ===\n%q", len(raw), raw)
	t.Logf("=== DECODED get-config REPLY ===\n%s", reply)

	// An <rpc> naming an operation the device does not implement. This
	// is the error convention pkg/netconf's typed error is modelled on,
	// and getting it from the device is the difference between modelling
	// the RFC and modelling this vendor.
	badRPC := `<?xml version="1.0" encoding="UTF-8"?>` +
		`<rpc message-id="102" xmlns="urn:ietf:params:xml:ns:netconf:base:1.0">` +
		`<pleiades-no-such-operation/></rpc>`
	if err := writeChunked(sub, badRPC); err != nil {
		t.Fatalf("writing the invalid rpc: %v", err)
	}
	errReply, _, err := readChunked(sub, 1<<22)
	if err != nil {
		t.Fatalf("reading the invalid rpc reply: %v", err)
	}
	t.Logf("=== <rpc-error> SHAPE ===\n%s", errReply)

	// Also provoke an error with a well-formed operation against a
	// datastore this device does not advertise, which is the specific
	// refusal net.netconf.config's target parameter has to produce.
	candidateRPC := `<?xml version="1.0" encoding="UTF-8"?>` +
		`<rpc message-id="103" xmlns="urn:ietf:params:xml:ns:netconf:base:1.0">` +
		`<get-config><source><candidate/></source></get-config></rpc>`
	if err := writeChunked(sub, candidateRPC); err != nil {
		t.Fatalf("writing the candidate-datastore rpc: %v", err)
	}
	candReply, _, err := readChunked(sub, 1<<22)
	if err != nil {
		t.Logf("candidate-datastore rpc: read failed: %v", err)
	} else {
		t.Logf("=== :candidate REFUSAL SHAPE ===\n%s", candReply)
	}

	closeRPC := `<rpc message-id="104" xmlns="urn:ietf:params:xml:ns:netconf:base:1.0"><close-session/></rpc>`
	if err := writeChunked(sub, closeRPC); err != nil {
		t.Logf("writing close-session: %v", err)
	}
	t.Log("session closed cleanly; only read-only RPCs were sent")
}

// writeChunked frames s per RFC 6242 section 4.2 and writes it. Test
// fixture only; see this file's second test's doc comment.
func writeChunked(w io.Writer, s string) error {
	_, err := io.WriteString(w, "\n#"+itoa(len(s))+"\n"+s+"\n##\n")
	return err
}

// readChunked reads one chunk-framed message, returning both the
// decoded payload and the raw bytes exactly as they arrived, so the
// wire shape itself can be logged rather than only its decoding. Test
// fixture only.
func readChunked(r io.Reader, maxBytes int) (decoded, raw string, err error) {
	var buf []byte
	chunk := make([]byte, 4096)
	for {
		if i := strings.Index(string(buf), "\n##\n"); i >= 0 {
			raw = string(buf[:i+4])
			decoded, err = decodeChunks(raw)
			return decoded, raw, err
		}
		if len(buf) >= maxBytes {
			return "", string(buf), io.ErrShortBuffer
		}
		n, readErr := r.Read(chunk)
		if n > 0 {
			buf = append(buf, chunk[:n]...)
		}
		if readErr != nil {
			return "", string(buf), readErr
		}
	}
}

func decodeChunks(raw string) (string, error) {
	var out strings.Builder
	rest := raw
	for {
		if strings.HasPrefix(rest, "\n##\n") {
			return out.String(), nil
		}
		if !strings.HasPrefix(rest, "\n#") {
			return out.String(), io.ErrUnexpectedEOF
		}
		rest = rest[2:]
		nl := strings.Index(rest, "\n")
		if nl < 0 {
			return out.String(), io.ErrUnexpectedEOF
		}
		size := 0
		for _, c := range rest[:nl] {
			if c < '0' || c > '9' {
				return out.String(), io.ErrUnexpectedEOF
			}
			size = size*10 + int(c-'0')
		}
		rest = rest[nl+1:]
		if len(rest) < size {
			return out.String(), io.ErrUnexpectedEOF
		}
		out.WriteString(rest[:size])
		rest = rest[size:]
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
