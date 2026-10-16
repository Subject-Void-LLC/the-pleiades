// Tests for pgclient that need no server: what Open refuses before it
// dials anything, and what its startup packet asks the server for.
package pgclient_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"testing"

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
