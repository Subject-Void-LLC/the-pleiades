package remoteexec_test

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
)

// liveNetconfProfile names one real device this diagnostic can be run
// against.
//
// It is a table rather than a copied test function per device, and that
// matters beyond tidiness: the whole point of a second vendor is to run
// the IDENTICAL probe against both and compare, and two hand-maintained
// copies drift into asking slightly different questions, which is
// exactly when a real difference between the two devices stops being
// visible.
type liveNetconfProfile struct {
	// name identifies the profile in subtest output.
	name string

	// gateEnv must be set for this profile to run at all.
	gateEnv string

	// hostEnv, userEnv and passEnv name the environment variables
	// carrying this device's address and credentials. There is no
	// credential default of any kind: both DevNet sandboxes issue a
	// fresh password per reservation, so a literal in this file would be
	// both a leaked live credential and a test that silently rots.
	hostEnv, userEnv, passEnv string

	// defaultHost is a published, non-secret sandbox hostname, safe to
	// carry here.
	defaultHost string

	// ports are tried in order until one serves a NETCONF hello.
	ports []int
}

var liveNetconfProfiles = []liveNetconfProfile{
	{
		name:        "ios-xe",
		gateEnv:     "PLEIADES_E2E_IOS",
		hostEnv:     "PLEIADES_E2E_IOS_HOST",
		userEnv:     "PLEIADES_E2E_IOS_USER",
		passEnv:     "PLEIADES_E2E_IOS_PASS",
		defaultHost: "devnetsandboxiosxec8k.cisco.com",
		ports:       []int{830, 22},
	},
	{
		name:        "ios-xr",
		gateEnv:     "PLEIADES_E2E_XR",
		hostEnv:     "PLEIADES_E2E_XR_HOST",
		userEnv:     "PLEIADES_E2E_XR_USER",
		passEnv:     "PLEIADES_E2E_XR_PASS",
		defaultHost: "sandbox-iosxr-1.cisco.com",
		ports:       []int{830, 22},
	},
}

// resolve returns this profile's connection details, or skips.
func (p liveNetconfProfile) resolve(t *testing.T) (host, user, pass string) {
	t.Helper()
	if os.Getenv(p.gateEnv) == "" {
		t.Skipf("set %s=1 to run this live diagnostic against a real %s device", p.gateEnv, p.name)
	}
	user = os.Getenv(p.userEnv)
	pass = os.Getenv(p.passEnv)
	if user == "" || pass == "" {
		t.Skipf("%s and %s must both be set (no default credential: this sandbox issues a unique password per reservation)", p.userEnv, p.passEnv)
	}
	host = os.Getenv(p.hostEnv)
	if host == "" {
		host = p.defaultHost
	}
	return host, user, pass
}

// TestLiveNetconfSubsystemShapes is a diagnostic, not a Release Gate. It
// serves two purposes at once, which is why it lives here rather than in
// pkg/netconf: it is the proof that Conn.Subsystem reaches a real device
// at all, and it captures the raw bytes a real NETCONF server sends
// before any client parses them, so RFC 6242 framing selection and RFC
// 6241 capability negotiation are written against a device's own answer
// instead of against a recollection of what a hello is supposed to look
// like.
//
// That sequencing is the whole point. pkg/netcli's own live probe exists
// because Phase 86.5's predecessor spec fabricated IOS prompt shapes it
// had never observed (FAILURE_PATTERNS.md #202), and a NETCONF hello has
// more guessable-looking detail in it than a CLI prompt does: which base
// capabilities are advertised, whether :candidate is offered, whether
// the server answers on the SSH port as well as 830. Every one of those
// is a fact about a device, and the IOS XE run already disproved two
// reasonable-sounding guesses.
//
// It is READ-ONLY and applies no configuration. It sends exactly one
// client hello and then closes the session; it never sends an <rpc>, so
// there is nothing for a device reload to undo. That matters because
// both sandboxes are shared with every other visitor at the same time.
func TestLiveNetconfSubsystemShapes(t *testing.T) {
	for _, profile := range liveNetconfProfiles {
		t.Run(profile.name, func(t *testing.T) {
			host, user, pass := profile.resolve(t)

			runner := remoteexec.New(remoteexec.Options{InsecureSkipHostKeyVerify: true})
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()

			// Every port is probed, and which of them actually serves the
			// subsystem is one of the facts this diagnostic exists to
			// establish rather than assume. NETCONF over SSH is a
			// SUBSYSTEM on an ordinary SSH connection (RFC 6242 section
			// 2), so port 22 SHOULD work and the dedicated 830 listener
			// SHOULD be a convention; whether a given build agrees is a
			// question about the device, and NetconfCapable's own
			// NetconfPort accessor exists precisely because the answer is
			// per-device configuration. IOS XE 17.12 accepts the
			// subsystem request on 22 and then immediately ends the
			// channel, which is why this loop reads a hello before
			// declaring a port working.
			var (
				sub      *remoteexec.Subsystem
				hello    string
				livePort int
			)
			for _, port := range profile.ports {
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

				// A subsystem request the server ACCEPTS and then
				// immediately ends is a real and distinct outcome from one
				// it refuses, and telling them apart is the reason the read
				// happens here rather than after the loop: a client that
				// only checked the request reply would report such a device
				// as working.
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
				t.Fatalf("no NETCONF hello could be read on any of ports %v", profile.ports)
			}
			defer sub.Close()

			t.Logf("NETCONF answered on port %d", livePort)
			t.Logf("=== SERVER HELLO (%d bytes) ===\n%s", len(hello), truncateForLog(hello, 4000))

			// The facts the client's negotiation rests on, called out
			// individually so they appear in the log even when the hello
			// itself is long enough to skim past. :candidate is the one
			// that most distinguishes one vendor from another, and it is
			// the reason a second device is worth the trouble: IOS XE
			// offers writable-running and no candidate at all, so every
			// datastore-family operation this client implements is
			// currently proven only against a container.
			for _, probe := range []struct{ label, needle string }{
				{"base:1.0", "urn:ietf:params:netconf:base:1.0"},
				{"base:1.1", "urn:ietf:params:netconf:base:1.1"},
				{":candidate", "urn:ietf:params:netconf:capability:candidate:1.0"},
				{":confirmed-commit", "urn:ietf:params:netconf:capability:confirmed-commit"},
				{":writable-running", "urn:ietf:params:netconf:capability:writable-running:1.0"},
				{":validate", "urn:ietf:params:netconf:capability:validate"},
				{":rollback-on-error", "urn:ietf:params:netconf:capability:rollback-on-error:1.0"},
				{":startup", "urn:ietf:params:netconf:capability:startup:1.0"},
				{":xpath", "urn:ietf:params:netconf:capability:xpath:1.0"},
			} {
				t.Logf("%-24s %v", probe.label+":", strings.Contains(hello, probe.needle))
			}
			if sessionID := between(hello, "<session-id>", "</session-id>"); sessionID != "" {
				t.Logf("%-24s %s", "session-id:", sessionID)
			}
			// The YANG namespace a configuration document has to be
			// written in is per-platform, and the Release Gate needs the
			// real one rather than an assumed one.
			for _, ns := range []string{
				"http://cisco.com/ns/yang/Cisco-IOS-XE-native",
				"http://cisco.com/ns/yang/Cisco-IOS-XR-ifmgr-cfg",
				"urn:ietf:params:xml:ns:yang:ietf-interfaces",
			} {
				if strings.Contains(hello, ns) {
					t.Logf("%-24s %s", "namespace present:", ns)
				}
			}

			// Send a client hello so the session ends cleanly from the
			// server's point of view rather than as an abandoned channel.
			// Deliberately base:1.0 only: this probe reads nothing after
			// the hello, so negotiating chunked framing here would prove
			// nothing.
			clientHello := `<?xml version="1.0" encoding="UTF-8"?>` +
				`<hello xmlns="urn:ietf:params:xml:ns:netconf:base:1.0">` +
				`<capabilities><capability>urn:ietf:params:netconf:base:1.0</capability></capabilities>` +
				`</hello>]]>]]>`
			if _, err := io.WriteString(sub, clientHello); err != nil {
				t.Fatalf("writing the client hello: %v", err)
			}
			t.Log("client hello sent; session closing without an <rpc>, so nothing was changed on the device")
		})
	}
}

// TestLiveNetconfChunkedFramingAndErrorShapes is the second half of the
// diagnostic: it negotiates base:1.1, exchanges read-only RPCs under RFC
// 6242 chunked framing, and deliberately provokes an <rpc-error>, so
// pkg/netconf's chunk codec and its error type are both written against
// a real device's bytes rather than against the RFC's prose alone.
//
// Running it against two vendors is what turns "this error type decodes
// this device" into "this error type decodes NETCONF". IOS XE sends no
// <error-message> at all for an unknown-element error, and Netopeer2
// does; a third data point is how that stops being a coincidence.
//
// The framing helpers below are TEST FIXTURES and deliberately naive:
// they buffer whole messages, which is exactly what the real codec must
// not do. Writing the encoder twice, once here against the RFC and once
// for real in pkg/netconf, is a deliberate cross-check: if the two
// disagree, the device is the tie-breaker.
//
// READ-ONLY. The only RPCs sent are <get-config> with a subtree filter,
// one deliberately invalid <rpc> to observe the error convention, one
// <get-config> against the candidate datastore to observe how a device
// that lacks it refuses, and <close-session>. Nothing is written.
func TestLiveNetconfChunkedFramingAndErrorShapes(t *testing.T) {
	for _, profile := range liveNetconfProfiles {
		t.Run(profile.name, func(t *testing.T) {
			host, user, pass := profile.resolve(t)

			runner := remoteexec.New(remoteexec.Options{InsecureSkipHostKeyVerify: true})
			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
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

			hello, err := readUntilDelimiter(sub, "]]>]]>", 1<<20)
			if err != nil {
				t.Fatalf("reading the server hello: %v", err)
			}
			hasCandidate := strings.Contains(hello, "urn:ietf:params:netconf:capability:candidate:1.0")

			clientHello := `<?xml version="1.0" encoding="UTF-8"?>` +
				`<hello xmlns="urn:ietf:params:xml:ns:netconf:base:1.0"><capabilities>` +
				`<capability>urn:ietf:params:netconf:base:1.0</capability>` +
				`<capability>urn:ietf:params:netconf:base:1.1</capability>` +
				`</capabilities></hello>]]>]]>`
			if _, err := io.WriteString(sub, clientHello); err != nil {
				t.Fatalf("writing the client hello: %v", err)
			}

			id := 100
			ask := func(label, body string) string {
				t.Helper()
				id++
				rpc := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?><rpc message-id="%d" xmlns="urn:ietf:params:xml:ns:netconf:base:1.0">%s</rpc>`, id, body)
				if err := writeChunked(sub, rpc); err != nil {
					t.Fatalf("%s: writing: %v", label, err)
				}
				reply, raw, err := readChunked(sub, 1<<22)
				if err != nil {
					t.Fatalf("%s: reading: %v (stderr: %q)", label, err, sub.Stderr())
				}
				if label == "get-config" {
					t.Logf("=== RAW CHUNK FRAMING ON THE WIRE (%d bytes) ===\n%s", len(raw), truncateForLog(fmt.Sprintf("%q", raw), 1200))
				}
				t.Logf("=== %s ===\n%s", strings.ToUpper(label), truncateForLog(reply, 2500))
				return reply
			}

			// A deliberately narrow read. A bare <get-config> on a real
			// router returns megabytes, which would tell us nothing extra
			// about framing and would be unkind to a shared sandbox.
			ask("get-config", `<get-config><source><running/></source><filter type="subtree">`+
				`<interfaces xmlns="urn:ietf:params:xml:ns:yang:ietf-interfaces"/>`+
				`</filter></get-config>`)

			// The error convention this vendor uses, which is what
			// pkg/netconf's typed error is modelled on. Getting it from
			// each device is the difference between modelling the RFC and
			// modelling one vendor.
			ask("rpc-error shape", `<pleiades-no-such-operation/>`)

			// How this device refuses a datastore it does not have, which
			// is the specific refusal net.netconf.config's target
			// parameter has to produce. On a device that DOES have a
			// candidate datastore this is a successful read instead, and
			// that is the answer worth having.
			label := "candidate datastore refusal"
			if hasCandidate {
				label = "candidate datastore read (this device HAS one)"
			}
			ask(label, `<get-config><source><candidate/></source><filter type="subtree">`+
				`<interfaces xmlns="urn:ietf:params:xml:ns:yang:ietf-interfaces"/>`+
				`</filter></get-config>`)

			ask("close-session", `<close-session/>`)
			t.Log("session closed cleanly; only read-only RPCs were sent")
		})
	}
}

// truncateForLog bounds what a diagnostic prints. A real device's hello
// is tens of kilobytes of YANG module URNs, and dumping all of it buries
// the handful of lines that answer the question.
func truncateForLog(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + fmt.Sprintf("\n... (%d more bytes elided)", len(s)-max)
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
func between(s, open, closing string) string {
	i := strings.Index(s, open)
	if i < 0 {
		return ""
	}
	rest := s[i+len(open):]
	j := strings.Index(rest, closing)
	if j < 0 {
		return ""
	}
	return rest[:j]
}

// writeChunked frames s per RFC 6242 section 4.2 and writes it. Test
// fixture only; see this file's second test's doc comment.
func writeChunked(w io.Writer, s string) error {
	_, err := io.WriteString(w, "\n#"+itoa(len(s))+"\n"+s+"\n##\n")
	return err
}

// readChunked reads one chunk-framed message, returning both the
// decoded payload and the raw bytes exactly as they arrived, so the wire
// shape itself can be logged rather than only its decoding. Test fixture
// only.
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
