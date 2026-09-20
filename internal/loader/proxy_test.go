//go:build unix

// Package loader: tests of the proxy that runs a program for each call.
package loader

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// These call the proxy Load registered, exactly as the engine does, with a
// real shell script as the program on the other side of a real process
// boundary: real stdin, a real pipe as file descriptor 3, real exit codes.

// captureRequest is an invoke body that saves the request it was sent,
// its environment and its arguments in record, then answers. A confined
// program cannot write its own directory, so record is a directory the
// test lets it write (recordingOptions).
func captureRequest(record string) string {
	return fmt.Sprintf(`cat > '%[1]s/request.json'
env > '%[1]s/env.txt'
printf '%%s\n' "$@" > '%[1]s/args.txt'
printf '{"changed":true,"facts":{"answer":"forty-two"}}\n' >&3`, record)
}

// recordingOptions are testOptions with record writable by the program,
// for a fixture built from captureRequest.
func recordingOptions(record string) Options {
	o := testOptions()
	o.testWritable = []string{record}
	return o
}

// TestProxy_DeliversTheRequestAndReturnsTheResponse is the happy path:
// the request arrives whole on stdin, the response's changed flag and
// facts come back, and the device's identity and address cross.
func TestProxy_DeliversTheRequestAndReturnsTheResponse(t *testing.T) {
	dir := programDir(t)
	record := t.TempDir()
	oneMethodProgram(t, dir, "loadertest.proxy.run", true, captureRequest(record))
	d := loadOne(t, dir, "loadertest.proxy.run", recordingOptions(record))

	rc := newRecordingContext()
	result, err := d.Invoke(t.Context(), rc, newSSHDevice(), map[string]any{"message": "hello"})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if !result.Changed {
		t.Error("the program answered changed:true and the proxy reported no change")
	}
	if rc.stats["answer"] != "forty-two" {
		t.Errorf("stats = %v, want the program's own fact", rc.stats)
	}

	var req wire.ChildRequest
	if err := json.Unmarshal([]byte(readFile(t, filepath.Join(record, "request.json"))), &req); err != nil {
		t.Fatalf("the request the program read is not JSON: %v", err)
	}
	if req.FQCN != "loadertest.proxy.run" || req.Mode != string(collection.ModeExecute) {
		t.Errorf("request named %q in mode %q, want the method in execute mode", req.FQCN, req.Mode)
	}
	if req.Params["message"] != "hello" {
		t.Errorf("request params = %v, want the task's own", req.Params)
	}
	if req.DeviceName != "web1" || req.DeviceHost != "192.0.2.10" || req.SSHPort != 2222 || req.DeviceID != "dev-1" {
		t.Errorf("request device = %q %q:%d (%q), want web1 192.0.2.10:2222 (dev-1)", req.DeviceName, req.DeviceHost, req.SSHPort, req.DeviceID)
	}
	if req.Secrets["password"] != testPassword {
		t.Error("the credential did not arrive on stdin, which is the only way it may travel")
	}
}

// TestProxy_CheckModeCrosses proves a check reaches the program as a
// check, which is the whole difference between reading a device and
// changing it.
func TestProxy_CheckModeCrosses(t *testing.T) {
	dir := programDir(t)
	record := t.TempDir()
	oneMethodProgram(t, dir, "loadertest.proxy.check", true, captureRequest(record))
	d := loadOne(t, dir, "loadertest.proxy.check", recordingOptions(record))

	if _, err := d.Check(t.Context(), newRecordingContext(), newSSHDevice(), nil); err != nil {
		t.Fatalf("Check: %v", err)
	}
	var req wire.ChildRequest
	if err := json.Unmarshal([]byte(readFile(t, filepath.Join(record, "request.json"))), &req); err != nil {
		t.Fatal(err)
	}
	if req.Mode != string(collection.ModeCheck) {
		t.Fatalf("a check reached the program in mode %q", req.Mode)
	}
}

// TestProxy_TheCredentialTravelsOnStdinOnly proves the credential is in
// neither the program's arguments nor its environment, and that nothing
// else from this process's environment crosses either.
func TestProxy_TheCredentialTravelsOnStdinOnly(t *testing.T) {
	t.Setenv("PLEIADES_LOADER_TEST_SENTINEL", "must-not-cross")
	t.Setenv("SSH_AUTH_SOCK", "/tmp/agent.sock")
	dir := programDir(t)
	record := t.TempDir()
	oneMethodProgram(t, dir, "loadertest.proxy.env", false, captureRequest(record))
	d := loadOne(t, dir, "loadertest.proxy.env", recordingOptions(record))

	if _, err := d.Invoke(t.Context(), newRecordingContext(), newSSHDevice(), nil); err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	env := readFile(t, filepath.Join(record, "env.txt"))
	args := readFile(t, filepath.Join(record, "args.txt"))

	if strings.Contains(env, testPassword) || strings.Contains(args, testPassword) {
		t.Fatal("the credential reached the program's environment or arguments")
	}
	for _, leaked := range []string{"PLEIADES_LOADER_TEST_SENTINEL", "SSH_AUTH_SOCK"} {
		if strings.Contains(env, leaked) {
			t.Errorf("%s crossed into the program's environment", leaked)
		}
	}
	// The control: the allowlist really is applied, not an empty env.
	if os.Getenv("PATH") != "" && !strings.Contains(env, "PATH=") {
		t.Error("PATH did not cross, so the allowlist is not what reached the program")
	}
	if strings.TrimSpace(args) != "invoke" {
		t.Errorf("the program's arguments were %q, want the single word invoke", args)
	}
}

// TestProxy_RefusesAProgramChangedAfterLoad proves the digest pinned at
// load is checked again at the point of execution, and names both.
func TestProxy_RefusesAProgramChangedAfterLoad(t *testing.T) {
	dir := programDir(t)
	path := oneMethodProgram(t, dir, "loadertest.proxy.tamper", false, `printf '{"changed":true}\n' >&3`)
	d := loadOne(t, dir, "loadertest.proxy.tamper", testOptions())

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0) // #nosec G304 -- this test's own fixture
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("# tampered\n"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	_, err = d.Invoke(t.Context(), newRecordingContext(), newSSHDevice(), nil)
	if err == nil {
		t.Fatal("a program changed after load was run")
	}
	if !strings.Contains(err.Error(), "changed after it was loaded") || strings.Count(err.Error(), "sha256:") != 2 {
		t.Errorf("refusal %q must say what happened and name both digests", err)
	}
}

// TestProxy_RefusesAProgramMadeWritableAfterLoad proves the permission
// checks are re-applied before every run, not only the digest.
func TestProxy_RefusesAProgramMadeWritableAfterLoad(t *testing.T) {
	dir := programDir(t)
	path := oneMethodProgram(t, dir, "loadertest.proxy.perm", false, `printf '{"changed":true}\n' >&3`)
	d := loadOne(t, dir, "loadertest.proxy.perm", testOptions())
	if err := os.Chmod(path, 0o777); err != nil { // #nosec G302 -- the fixture under test
		t.Fatal(err)
	}
	if _, err := d.Invoke(t.Context(), newRecordingContext(), newSSHDevice(), nil); err == nil || !strings.Contains(err.Error(), "world-writable") {
		t.Fatalf("Invoke = %v, want a refusal of the now-writable program", err)
	}
}

// TestProxy_EveryWayAProgramCanMisbehave is Phase 45's boundary list, one
// case per failure, each with its own message, each returning promptly.
func TestProxy_EveryWayAProgramCanMisbehave(t *testing.T) {
	cases := []struct {
		name string
		body string
		opts func(*Options)
		want string
	}{
		{name: "exits without answering", body: "exit 0", want: "exited without writing a response"},
		{name: "exits with an error", body: "echo 'it broke' >&2\nexit 3", want: "exited with an error"},
		{name: "never exits", body: "exec sleep 30", opts: func(o *Options) { o.InvokeTimeout = 300 * time.Millisecond }, want: "did not finish within"},
		{name: "writes a malformed response", body: `printf 'not json\n' >&3`, want: "malformed response"},
		{name: "stops partway through its response", body: `printf '{"changed":tr' >&3`, want: "partway through"},
		{name: "writes data after its response", body: `printf '{"changed":true}\n{}\n' >&3`, want: "data after its response"},
		// The cap covers the describe document too, so it sits above
		// what this program describes and below what it answers.
		{name: "writes a response over the cap", body: `head -c 5000 /dev/zero | tr '\0' ' ' >&3; printf '{}' >&3`, opts: func(o *Options) { o.MaxResponse = 1024 }, want: "over the 1024-byte limit"},
		{name: "reports its own failure", body: `printf '{"error":"the device said no"}\n' >&3`, want: "the device said no"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := programDir(t)
			fqcn := "loadertest.misbehave.run"
			oneMethodProgram(t, dir, fqcn, false, tc.body)
			opts := testOptions()
			if tc.opts != nil {
				tc.opts(&opts)
			}
			d := loadOne(t, dir, fqcn, opts)

			started := time.Now()
			rc := newRecordingContext()
			_, err := d.Invoke(t.Context(), rc, newSSHDevice(), nil)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Invoke = %v, want an error saying %q", err, tc.want)
			}
			if time.Since(started) > 4*time.Second {
				t.Errorf("the call took %s to fail", time.Since(started))
			}
			if len(rc.stats) != 0 {
				t.Errorf("a failed call recorded stats %v", rc.stats)
			}
		})
	}
}

// TestProxy_SurvivesAFloodOfOutput proves a program writing far more than
// the cap to stdout and stderr still completes, with its output bounded,
// rather than stalling on a full pipe.
func TestProxy_SurvivesAFloodOfOutput(t *testing.T) {
	dir := programDir(t)
	fqcn := "loadertest.flood.run"
	oneMethodProgram(t, dir, fqcn, false, "head -c 20000000 /dev/zero\nhead -c 20000000 /dev/zero >&2\nprintf '{\"changed\":false}\\n' >&3")
	opts := testOptions()
	opts.MaxOutput = 1024
	d := loadOne(t, dir, fqcn, opts)

	started := time.Now()
	result, err := d.Invoke(t.Context(), newRecordingContext(), newSSHDevice(), nil)
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if result.Changed {
		t.Error("the program answered changed:false")
	}
	if time.Since(started) > 4*time.Second {
		t.Errorf("a 40 MB flood took %s to drain", time.Since(started))
	}
}

// TestProxy_MasksTheCredentialInEverythingItQuotes proves a program that
// echoes the credential on stderr, or in its own error, never gets it
// into the error the engine sees.
func TestProxy_MasksTheCredentialInEverythingItQuotes(t *testing.T) {
	extract := `req=$(cat)
pw=$(printf '%s' "$req" | sed -n 's/.*"password":"\([^"]*\)".*/\1/p')
`
	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "on stderr", body: extract + "echo \"login failed for $pw\" >&2\nexit 3"},
		{name: "in its reported error", body: extract + "printf '{\"error\":\"login failed for %s\"}\\n' \"$pw\" >&3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := programDir(t)
			fqcn := "loadertest.mask.run"
			oneMethodProgram(t, dir, fqcn, false, tc.body)
			d := loadOne(t, dir, fqcn, testOptions())

			_, err := d.Invoke(t.Context(), newRecordingContext(), newSSHDevice(), nil)
			if err == nil {
				t.Fatal("expected the call to fail")
			}
			if strings.Contains(err.Error(), testPassword) {
				t.Fatalf("the credential reached the error: %v", err)
			}
			// The control: the program really did print something there.
			if !strings.Contains(err.Error(), "login failed for") {
				t.Errorf("error %q does not carry what the program said, so the mask assertion proves nothing", err)
			}
		})
	}
}

// TestProxy_ToleratesAProcessHoldingTheChannelOpen proves a program that
// answers and leaves a background process holding file descriptor 3 does
// not hang the call: the complete answer is used once the grace ends.
func TestProxy_ToleratesAProcessHoldingTheChannelOpen(t *testing.T) {
	dir := programDir(t)
	fqcn := "loadertest.linger.run"
	oneMethodProgram(t, dir, fqcn, false, "printf '{\"changed\":true}\\n' >&3\nsleep 2 >/dev/null 2>&1 &")
	d := loadOne(t, dir, fqcn, testOptions())

	started := time.Now()
	result, err := d.Invoke(t.Context(), newRecordingContext(), newSSHDevice(), nil)
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if !result.Changed {
		t.Error("the complete answer was not used")
	}
	if elapsed := time.Since(started); elapsed > 1500*time.Millisecond {
		t.Errorf("the call waited %s on a process holding the channel, longer than its grace", elapsed)
	}
}

// TestProxy_NoDeviceAndNoContext covers a task with no target, which sends
// no device, and a call with no context, which is refused before anything
// runs.
func TestProxy_NoDeviceAndNoContext(t *testing.T) {
	dir := programDir(t)
	record := t.TempDir()
	oneMethodProgram(t, dir, "loadertest.bare.run", false, captureRequest(record))
	d := loadOne(t, dir, "loadertest.bare.run", recordingOptions(record))

	if _, err := d.Invoke(t.Context(), newRecordingContext(), nil, nil); err != nil {
		t.Fatalf("Invoke with no device: %v", err)
	}
	var req wire.ChildRequest
	if err := json.Unmarshal([]byte(readFile(t, filepath.Join(record, "request.json"))), &req); err != nil {
		t.Fatal(err)
	}
	if req.DeviceName != "" || req.DeviceHost != "" || len(req.Capabilities) != 0 {
		t.Errorf("a call with no device sent device fields: %+v", req)
	}

	if _, err := d.Invoke(t.Context(), nil, nil, nil); err == nil {
		t.Error("a call with no runbook context was run")
	}
}

// TestProxy_AStatTheRunRefusesIsReported covers the program answering and
// the run refusing to record what it said.
func TestProxy_AStatTheRunRefusesIsReported(t *testing.T) {
	dir := programDir(t)
	record := t.TempDir()
	oneMethodProgram(t, dir, "loadertest.refuse.run", false, captureRequest(record))
	d := loadOne(t, dir, "loadertest.refuse.run", recordingOptions(record))

	rc := newRecordingContext()
	rc.refuse = errors.New("no room")
	if _, err := d.Invoke(t.Context(), rc, newSSHDevice(), nil); err == nil || !strings.Contains(err.Error(), "no room") {
		t.Fatalf("Invoke = %v, want the refusal reported", err)
	}
}
