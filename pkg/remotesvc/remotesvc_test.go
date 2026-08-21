package remotesvc

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
)

// The command-sending tests below run against a real in-process SSH
// server executing a real /bin/sh, with a real systemctl on PATH. That
// systemctl is a shell script rather than systemd, and the distinction
// worth being precise about is which half is stubbed: the SSH transport,
// the shell, the quoting, the argument vector and the exit status are all
// genuine, and only the daemon at the far end is not.
//
// That is the seam this package can honestly test on a Linux CI box with
// no systemd, and it catches the failures that actually happen here: a
// unit name that loses its quoting, a verb spelled wrong, a non-zero exit
// swallowed. Proving this against real systemd is a container Release
// Gate's job, and this file does not pretend to be one.

// fakeSystemctl puts a systemctl on PATH that records every invocation
// and answers from environment variables the test sets. It returns the
// path of the file the recording lands in.
func fakeSystemctl(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	record := filepath.Join(dir, "invocations")

	// "$@" is written one argument per line so a test can assert on the
	// argument VECTOR rather than on a re-joined string, which is the
	// only way to prove quoting survived.
	//
	// The record path arrives through the environment rather than being
	// interpolated into this script, and that is not fussiness. t.TempDir
	// derives its path from the TEST NAME, and these tests are named after
	// the hostile unit names they check, so the subtest for "unit$(whoami)"
	// gets a directory path containing "$(whoami)". Interpolated inside
	// double quotes, the shell expanded it and the recording went
	// somewhere else, which showed up as "systemctl was called 0 times"
	// against code that was behaving correctly. An environment value is
	// not re-expanded.
	script := `#!/bin/sh
for arg in "$@"; do printf '%s\n' "$arg" >> "$FAKE_RECORD"; done
printf -- '---\n' >> "$FAKE_RECORD"
if [ "$1" = "show" ]; then
  printf 'LoadState=%s\n' "${FAKE_LOAD_STATE:-loaded}"
  printf 'ActiveState=%s\n' "${FAKE_ACTIVE_STATE:-active}"
  printf 'UnitFileState=%s\n' "${FAKE_UNIT_FILE_STATE:-enabled}"
  exit "${FAKE_SHOW_EXIT:-0}"
fi
if [ -n "$FAKE_ERROR" ]; then printf '%s\n' "$FAKE_ERROR" >&2; fi
exit "${FAKE_EXIT:-0}"
`
	path := filepath.Join(dir, "systemctl")
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil { // #nosec G306 -- test fixture that must be executable
		t.Fatalf("writing the fake systemctl: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_RECORD", record)
	return record
}

// invocations returns each recorded systemctl call as its argument vector.
func invocations(t *testing.T, record string) [][]string {
	t.Helper()
	data, err := os.ReadFile(record) // #nosec G304 -- path built by this test
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("reading recorded invocations: %v", err)
	}
	var calls [][]string
	var current []string
	for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		if line == "---" {
			calls = append(calls, current)
			current = nil
			continue
		}
		current = append(current, line)
	}
	return calls
}

// connect starts a real SSH server and returns a live connection to it.
func connect(t *testing.T) *remoteexec.Conn {
	t.Helper()

	srv, err := remoteexectest.Start(remoteexectest.Options{})
	if err != nil {
		t.Fatalf("starting the SSH harness: %v", err)
	}
	t.Cleanup(srv.Close)

	runner := remoteexec.New(remoteexec.Options{InsecureSkipHostKeyVerify: true})
	auth, err := remoteexec.AuthFrom(srv.Username, srv.Password, nil, "")
	if err != nil {
		t.Fatalf("building auth: %v", err)
	}
	conn, err := runner.Connect(context.Background(), nil, remoteexec.Target{Host: srv.Host, Port: srv.Port}, auth)
	if err != nil {
		t.Fatalf("connecting: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// TestParseShow covers the parsing that everything else rests on.
//
// Reading by key rather than by position is the property under test. A
// systemd version printing these three properties in a different order
// would silently swap ActiveState and UnitFileState in a positional
// reader, turning "running but not enabled" into "enabled but not
// running" with no error anywhere.
func TestParseShow(t *testing.T) {
	tests := []struct {
		name   string
		stdout string
		want   State
	}{
		{
			name:   "the ordinary answer",
			stdout: "LoadState=loaded\nActiveState=active\nUnitFileState=enabled\n",
			want:   State{Unit: "nginx", LoadState: "loaded", ActiveState: "active", UnitFileState: "enabled"},
		},
		{
			name:   "properties in a different order",
			stdout: "UnitFileState=disabled\nLoadState=loaded\nActiveState=inactive\n",
			want:   State{Unit: "nginx", LoadState: "loaded", ActiveState: "inactive", UnitFileState: "disabled"},
		},
		{
			name:   "a unit that does not exist",
			stdout: "LoadState=not-found\nActiveState=inactive\nUnitFileState=\n",
			want:   State{Unit: "nginx", LoadState: "not-found", ActiveState: "inactive", UnitFileState: ""},
		},
		{
			name:   "extra properties are ignored rather than refused",
			stdout: "LoadState=loaded\nDescription=A web server\nActiveState=active\nUnitFileState=static\n",
			want:   State{Unit: "nginx", LoadState: "loaded", ActiveState: "active", UnitFileState: "static"},
		},
		{
			name:   "a value containing an equals sign survives",
			stdout: "LoadState=loaded\nActiveState=active\nUnitFileState=enabled\nExecStart=/bin/sh -c a=b\n",
			want:   State{Unit: "nginx", LoadState: "loaded", ActiveState: "active", UnitFileState: "enabled"},
		},
		{
			name:   "blank output leaves everything empty",
			stdout: "",
			want:   State{Unit: "nginx"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseShow("nginx", tt.stdout)
			if got != tt.want {
				t.Errorf("parseShow() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// TestState_Predicates pins the judgment calls, which are the part of
// this type most likely to be changed by someone who has not thought
// about the case that motivated them.
func TestState_Predicates(t *testing.T) {
	tests := []struct {
		name                                            string
		state                                           State
		exists, active, enabled, masked, static, failed bool
	}{
		{
			name:   "running and enabled",
			state:  State{LoadState: "loaded", ActiveState: "active", UnitFileState: "enabled"},
			exists: true, active: true, enabled: true,
		},
		{
			// The case a naive is-active check gets wrong: a unit part-way
			// through starting has not started, and calling it active would
			// let a method report "nothing to do" about a unit that may
			// still fail to come up.
			name:   "activating is not active",
			state:  State{LoadState: "loaded", ActiveState: "activating", UnitFileState: "enabled"},
			exists: true, enabled: true,
		},
		{
			name:   "enabled-runtime counts as enabled",
			state:  State{LoadState: "loaded", ActiveState: "active", UnitFileState: "enabled-runtime"},
			exists: true, active: true, enabled: true,
		},
		{
			// The distinction that keeps a typo from reporting success.
			name:  "a unit that does not exist",
			state: State{LoadState: "not-found", ActiveState: "inactive"},
		},
		{
			name:   "masked at the load level",
			state:  State{LoadState: "masked", ActiveState: "inactive", UnitFileState: "masked"},
			exists: true, masked: true,
		},
		{
			name:   "static cannot be enabled",
			state:  State{LoadState: "loaded", ActiveState: "active", UnitFileState: "static"},
			exists: true, active: true, static: true,
		},
		{
			name:   "failed is not active",
			state:  State{LoadState: "loaded", ActiveState: "failed", UnitFileState: "enabled"},
			exists: true, enabled: true, failed: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.state.Exists(); got != tt.exists {
				t.Errorf("Exists() = %v, want %v", got, tt.exists)
			}
			if got := tt.state.Active(); got != tt.active {
				t.Errorf("Active() = %v, want %v", got, tt.active)
			}
			if got := tt.state.Enabled(); got != tt.enabled {
				t.Errorf("Enabled() = %v, want %v", got, tt.enabled)
			}
			if got := tt.state.Masked(); got != tt.masked {
				t.Errorf("Masked() = %v, want %v", got, tt.masked)
			}
			if got := tt.state.Static(); got != tt.static {
				t.Errorf("Static() = %v, want %v", got, tt.static)
			}
			if got := tt.state.Failed(); got != tt.failed {
				t.Errorf("Failed() = %v, want %v", got, tt.failed)
			}
		})
	}
}

// TestState_MapKeys pins the diff keys, which are a contract rather than
// a detail: a method records them under diff.before and diff.after, and
// renaming one silently changes what a later reader sees.
func TestState_MapKeys(t *testing.T) {
	m := State{Unit: "nginx", LoadState: "loaded", ActiveState: "active", UnitFileState: "enabled"}.Map()
	for _, key := range []string{"unit", "exists", "active", "enabled", "load_state", "active_state", "unit_file_state"} {
		if _, ok := m[key]; !ok {
			t.Errorf("Map() is missing key %q", key)
		}
	}
	if m["active"] != true || m["enabled"] != true || m["exists"] != true {
		t.Errorf("Map() = %v, want active, enabled and exists all true", m)
	}
}

// TestStatus_ReadsThroughARealShell proves the whole read path: the
// command reaches a real shell over real SSH, systemctl's output is
// parsed, and the properties asked for are the three this package needs.
func TestStatus_ReadsThroughARealShell(t *testing.T) {
	record := fakeSystemctl(t)
	conn := connect(t)

	state, err := Status(context.Background(), conn, "nginx")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !state.Active() || !state.Enabled() || !state.Exists() {
		t.Errorf("Status() = %+v, want an existing, active, enabled unit", state)
	}

	calls := invocations(t, record)
	if len(calls) != 1 {
		t.Fatalf("systemctl was called %d times, want 1: reading state must be one round trip", len(calls))
	}
	got := calls[0]
	want := []string{"show", "nginx", "--property=LoadState", "--property=ActiveState", "--property=UnitFileState"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("systemctl argv = %v, want %v", got, want)
	}
}

// TestStatus_AbsentUnitIsAnAnswerNotAnError is the distinction that stops
// a mistyped unit name from going green.
func TestStatus_AbsentUnitIsAnAnswerNotAnError(t *testing.T) {
	fakeSystemctl(t)
	t.Setenv("FAKE_LOAD_STATE", "not-found")
	t.Setenv("FAKE_ACTIVE_STATE", "inactive")
	t.Setenv("FAKE_UNIT_FILE_STATE", "")
	conn := connect(t)

	state, err := Status(context.Background(), conn, "nginx")
	if err != nil {
		t.Fatalf("a unit that does not exist must not be an error: %v", err)
	}
	if state.Exists() {
		t.Error("Exists() = true for a not-found unit")
	}
	if state.Active() {
		t.Error("Active() = true for a not-found unit")
	}
}

// TestStatus_UnreachableSystemdIsAnError covers the other side. systemctl
// answers 0 even for a unit it has never heard of, so a non-zero status
// means systemd itself could not be reached, and reporting that as
// "the unit is absent" would be a lie an operator acts on.
func TestStatus_UnreachableSystemdIsAnError(t *testing.T) {
	fakeSystemctl(t)
	t.Setenv("FAKE_SHOW_EXIT", "1")
	conn := connect(t)

	if _, err := Status(context.Background(), conn, "nginx"); err == nil {
		t.Fatal("expected an error when systemctl itself fails")
	}
}

// TestOperations_SendTheRightVerb walks every verb this package can send.
// A verb spelled wrong is the defect this catches, and it is invisible in
// a unit test that stubs the connection.
func TestOperations_SendTheRightVerb(t *testing.T) {
	tests := []struct {
		name string
		call func(context.Context, *remoteexec.Conn) error
		want []string
	}{
		{name: "start", want: []string{"start", "nginx"},
			call: func(ctx context.Context, c *remoteexec.Conn) error { return Start(ctx, c, "nginx") }},
		{name: "stop", want: []string{"stop", "nginx"},
			call: func(ctx context.Context, c *remoteexec.Conn) error { return Stop(ctx, c, "nginx") }},
		{name: "restart", want: []string{"restart", "nginx"},
			call: func(ctx context.Context, c *remoteexec.Conn) error { return Restart(ctx, c, "nginx") }},
		{name: "enable", want: []string{"enable", "nginx"},
			call: func(ctx context.Context, c *remoteexec.Conn) error { return Enable(ctx, c, "nginx") }},
		{name: "disable", want: []string{"disable", "nginx"},
			call: func(ctx context.Context, c *remoteexec.Conn) error { return Disable(ctx, c, "nginx") }},
		{name: "daemon-reload", want: []string{"daemon-reload"},
			call: func(ctx context.Context, c *remoteexec.Conn) error { return DaemonReload(ctx, c) }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			record := fakeSystemctl(t)
			conn := connect(t)

			if err := tt.call(context.Background(), conn); err != nil {
				t.Fatalf("%s: %v", tt.name, err)
			}
			calls := invocations(t, record)
			if len(calls) != 1 {
				t.Fatalf("systemctl was called %d times, want 1", len(calls))
			}
			if strings.Join(calls[0], " ") != strings.Join(tt.want, " ") {
				t.Errorf("systemctl argv = %v, want %v", calls[0], tt.want)
			}
		})
	}
}

// TestOperations_QuoteTheUnitName is the injection guard, and it asserts
// on the argument VECTOR rather than on the joined command line, because
// a vector is the only place the difference is visible.
//
// A unit name reaching this from a runbook variable is the most likely
// hostile value in the whole package. Without quoting, a name containing
// a semicolon would end the systemctl command and start a second one that
// the remote shell would run just as happily.
func TestOperations_QuoteTheUnitName(t *testing.T) {
	hostile := []string{
		"evil; touch /tmp/pwned",
		"unit with spaces",
		"unit$(whoami)",
		"unit`id`",
		"unit'quote",
	}

	for _, unit := range hostile {
		t.Run(unit, func(t *testing.T) {
			record := fakeSystemctl(t)
			conn := connect(t)

			if err := Start(context.Background(), conn, unit); err != nil {
				t.Fatalf("Start: %v", err)
			}
			calls := invocations(t, record)
			if len(calls) != 1 {
				t.Fatalf("systemctl was called %d times, want exactly 1: a second call means the name broke out", len(calls))
			}
			if len(calls[0]) != 2 {
				t.Fatalf("systemctl argv = %v, want exactly 2 arguments: the name did not survive as one argument", calls[0])
			}
			if calls[0][1] != unit {
				t.Errorf("systemctl received unit %q, want %q verbatim", calls[0][1], unit)
			}
		})
	}
}

// TestOperations_NonZeroExitIsAnError proves a failed systemctl is
// reported rather than swallowed, and that the message carries
// systemctl's own explanation.
//
// This is the opposite of transport.Transport's rule, and deliberately
// so: there, a non-zero exit is the remote command's own answer and not a
// Go error. Here the caller asked for a unit to be started, so systemctl
// refusing IS the failure of this function's purpose.
func TestOperations_NonZeroExitIsAnError(t *testing.T) {
	fakeSystemctl(t)
	t.Setenv("FAKE_EXIT", "5")
	t.Setenv("FAKE_ERROR", "Failed to start nginx.service: Unit nginx.service is masked.")
	conn := connect(t)

	err := Start(context.Background(), conn, "nginx")
	if err == nil {
		t.Fatal("expected an error when systemctl exits non-zero")
	}
	for _, want := range []string{"start", "nginx", "5", "masked"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to mention %q", err, want)
		}
	}
}

// TestDaemonReload_TakesNoUnit pins the one operation that is not about a
// unit, which a refactor unifying these verbs would be tempted to give
// one.
func TestDaemonReload_NonZeroExitIsAnError(t *testing.T) {
	fakeSystemctl(t)
	t.Setenv("FAKE_EXIT", "1")
	t.Setenv("FAKE_ERROR", "Failed to reload daemon: Access denied")
	conn := connect(t)

	err := DaemonReload(context.Background(), conn)
	if err == nil {
		t.Fatal("expected an error when daemon-reload fails")
	}
	if !strings.Contains(err.Error(), "Access denied") {
		t.Errorf("error = %v, want it to carry systemctl's own explanation", err)
	}
}
