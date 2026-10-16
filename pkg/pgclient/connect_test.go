// Tests for pgclient that need no server: what Open refuses before it
// dials anything, and what its startup packet asks the server for.
package pgclient_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/pgclient"
)

// readStartup reads a protocol 3 startup packet from conn, as a server
// does, and returns its parameters.
func readStartup(conn io.Reader) (map[string]string, error) {
	var head [8]byte
	if _, err := io.ReadFull(conn, head[:]); err != nil {
		return nil, err
	}
	size := binary.BigEndian.Uint32(head[0:4])
	if size < 9 || size > 1<<16 {
		return nil, fmt.Errorf("a startup packet of %d bytes", size)
	}
	if proto := binary.BigEndian.Uint32(head[4:8]); proto>>16 != 3 {
		return nil, fmt.Errorf("protocol %d.%d, not 3", proto>>16, proto&0xffff)
	}
	body := make([]byte, size-8)
	if _, err := io.ReadFull(conn, body); err != nil {
		return nil, err
	}
	// The body is key, NUL, value, NUL, ..., ending with one more NUL.
	fields := bytes.Split(bytes.TrimSuffix(body, []byte{0, 0}), []byte{0})
	if len(fields)%2 != 0 {
		return nil, fmt.Errorf("startup parameters %q do not pair up", body)
	}
	params := map[string]string{}
	for i := 0; i < len(fields); i += 2 {
		params[string(fields[i])] = string(fields[i+1])
	}
	return params, nil
}

// TestOpen_PinsSessionSettingsAtStartup: the startup packet itself asks
// for the client encoding and date style the driver decodes with, and for
// standard-conforming strings, which pkg/sqllint lexes by. Sent there,
// they win over a database's or a role's own settings, so a LATIN1
// database or a role set to escape backslashes cannot change what text
// and times read as, or where a string ends.
func TestOpen_PinsSessionSettingsAtStartup(t *testing.T) {
	client, server := net.Pipe()
	type startup struct {
		params map[string]string
		err    error
	}
	got := make(chan startup, 1)
	go func() {
		// The server reads the packet and hangs up: Open then fails, and
		// the packet is all this test is after.
		defer func() { _ = server.Close() }()
		params, err := readStartup(server)
		got <- startup{params, err}
	}()
	tg := pgclient.Target{
		Host: "db", Port: 5432, User: "app", Password: "pw-for-tests", Database: "app",
		Dial: func(context.Context) (net.Conn, error) { return client, nil },
	}
	if s, err := pgclient.Open(context.Background(), tg); err == nil {
		_ = s.Close()
		t.Fatal("Open succeeded against a server that hung up")
	}
	sent := <-got
	if sent.err != nil {
		t.Fatalf("reading the startup packet: %v", sent.err)
	}
	for name, want := range map[string]string{
		"client_encoding":             "UTF8",
		"datestyle":                   "ISO, MDY",
		"standard_conforming_strings": "on",
		"user":                        "app",
		"database":                    "app",
	} {
		if sent.params[name] != want {
			t.Errorf("startup %s = %q, want %q (all: %v)", name, sent.params[name], want, sent.params)
		}
	}
}

// unreachable is a target whose dialer records that it was called and
// fails, for a test that must see Open refuse before dialing.
func unreachable(dialed *bool) pgclient.Target {
	return pgclient.Target{
		Host: "db", Port: 5432, User: "app", Password: "pw-for-tests", Database: "app",
		Dial: func(context.Context) (net.Conn, error) {
			*dialed = true
			return nil, errors.New("dialed")
		},
	}
}

// TestOpen_RefusesWhilePQGODebugIsSet: with PQGO_DEBUG set the driver
// writes each connection's password, and every query and row, to
// standard error, so Open refuses before it dials, for any value.
func TestOpen_RefusesWhilePQGODebugIsSet(t *testing.T) {
	for _, value := range []string{"1", "yes"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("PQGO_DEBUG", value)
			dialed := false
			_, err := pgclient.Open(context.Background(), unreachable(&dialed))
			if err == nil || !strings.Contains(err.Error(), "PQGO_DEBUG") {
				t.Fatalf("Open = %v, want a refusal naming PQGO_DEBUG", err)
			}
			if dialed {
				t.Error("Open dialed with PQGO_DEBUG set")
			}
		})
	}
}

// pqDebugChild marks the re-executed test binary as the child of
// TestOpen_RefusesPQGODebugSetAtStart.
const pqDebugChild = "PGCLIENT_TEST_PQGO_DEBUG_CHILD"

// TestOpen_RefusesPQGODebugSetAtStart: the driver reads PQGO_DEBUG once,
// as the program starts, so unsetting it later leaves its output on. The
// test binary is run again with it set from the start; that child unsets
// it and must still be refused.
func TestOpen_RefusesPQGODebugSetAtStart(t *testing.T) {
	if os.Getenv(pqDebugChild) == "1" {
		if err := os.Unsetenv("PQGO_DEBUG"); err != nil {
			t.Fatal(err)
		}
		dialed := false
		if _, err := pgclient.Open(context.Background(), unreachable(&dialed)); err == nil || !strings.Contains(err.Error(), "PQGO_DEBUG") || dialed {
			t.Fatalf("with PQGO_DEBUG set at start and unset since: Open = %v, dialed %v", err, dialed)
		}
		return
	}
	// #nosec G204 -- the test binary itself, rerunning this one test.
	cmd := exec.Command(os.Args[0], "-test.run=^TestOpen_RefusesPQGODebugSetAtStart$", "-test.count=1")
	cmd.Env = append(os.Environ(), "PQGO_DEBUG=1", pqDebugChild+"=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the child: %v\n%s", err, out)
	}
}

// deadlineless is one end of a pipe that refuses every deadline, as an
// SSH channel does (pkg/remoteexec's DialTCP and DialUnix return one).
type deadlineless struct{ net.Conn }

// errNoDeadline is what x/crypto/ssh answers a deadline with.
var errNoDeadline = errors.New("ssh: tcpChan: deadline not supported")

// SetDeadline refuses, as an SSH channel's does.
func (deadlineless) SetDeadline(time.Time) error { return errNoDeadline }

// SetReadDeadline refuses, as an SSH channel's does.
func (deadlineless) SetReadDeadline(time.Time) error { return errNoDeadline }

// SetWriteDeadline refuses, as an SSH channel's does.
func (deadlineless) SetWriteDeadline(time.Time) error { return errNoDeadline }

// silentServer is a dialer for a server that accepts, reads all it is
// sent and never answers, over a stream that takes no deadline.
func silentServer(t *testing.T) func(context.Context) (net.Conn, error) {
	return func(context.Context) (net.Conn, error) {
		client, server := net.Pipe()
		go func() { _, _ = io.Copy(io.Discard, server) }()
		t.Cleanup(func() { _ = server.Close() })
		return deadlineless{client}, nil
	}
}

// TestOpen_IsBoundedOverAStreamWithoutDeadlines: against a server that
// never answers, over a stream that refuses deadlines, Open gives up once
// its context ends or its connect timeout passes, whichever is first,
// whether it is waiting on the answer to its TLS request or on the
// startup. A deadline on the stream cannot do that.
func TestOpen_IsBoundedOverAStreamWithoutDeadlines(t *testing.T) {
	for name, cfg := range map[string]*tls.Config{
		"waiting on the startup":     nil,
		"waiting on the TLS request": {MinVersion: tls.VersionTLS12, ServerName: "db"},
	} {
		t.Run(name, func(t *testing.T) {
			tg := pgclient.Target{Host: "db", Port: 5432, User: "app", Password: "pw-for-tests", Database: "app", TLS: cfg, Dial: silentServer(t)}
			t.Run("by the context", func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
				defer cancel()
				if err := openWithin(ctx, t, tg, 10*time.Second); !errors.Is(err, context.DeadlineExceeded) {
					t.Errorf("Open = %v, want the context's deadline", err)
				}
			})
			t.Run("by the connect timeout", func(t *testing.T) {
				bounded := tg
				bounded.ConnectTimeout = 300 * time.Millisecond
				if err := openWithin(context.Background(), t, bounded, 10*time.Second); err == nil || !strings.Contains(err.Error(), "did not open within 300ms") {
					t.Errorf("Open = %v, want the connect timeout named", err)
				}
			})
		})
	}
}

// openWithin opens tg against a server that never answers and returns
// Open's error, failing the test unless Open gives up within limit.
func openWithin(ctx context.Context, t *testing.T, tg pgclient.Target, limit time.Duration) error {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		s, err := pgclient.Open(ctx, tg)
		if err == nil {
			_ = s.Close()
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Open connected to a server that never answered")
		}
		return err
	case <-time.After(limit):
		t.Fatalf("Open was still waiting after %s, long past its bound", limit)
		return nil
	}
}
