// The RULE 0 test of Main's real wiring: a real external Collection
// program, built from source with `go build` and run as a genuine child
// process, the way The Pleiades runs one.
//
// Everything in serve_test.go runs Serve over buffers, which proves the
// logic and nothing about the wiring around it. This file proves the
// wiring: that os.Args reaches the command, that the request arrives on
// the child's real stdin, that the response leaves on the real file
// descriptor 3 a parent hands over through exec.Cmd.ExtraFiles, that
// nothing the protocol writes lands on stdout, and that Serve's code
// becomes the process's real exit status. The program is
// testdata/echoprog, which is nothing but a call to external.Main.
//
// The parent side here mirrors internal/adapters/native's own spawn in
// ipc_parent.go: the request marshaled onto stdin, the write end of an
// os.Pipe as ExtraFiles[0], the response decoded concurrently with the
// run, and the parent's copy of the write end closed once the child
// exits. The one deliberate difference is the environment, which is
// emptied entirely: stricter than the loader's allowlist, and proof that
// the program needs nothing from its parent but stdin and fd 3.
package external_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/external"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// echoProgMethod is the one method testdata/echoprog provides. It must
// match the program's own methodName.
const echoProgMethod = "externaltest.echo.run"

// echoProgManifest is the manifest testdata/echoprog declares, kept
// identical to the program's own literal so describe's output can be
// compared against it whole.
var echoProgManifest = collection.Manifest{
	SupportedTransports:  []string{"ssh"},
	RequiredCapabilities: []capability.Name{capability.NameSSHTransport},
	Status:               collection.StatusImplemented,
	Reversibility:        collection.Reversibility{Notes: "a test fixture that changes nothing on any device"},
	SupportsCheck:        true,
	Doc:                  collection.Doc{Summary: "Echo what the method was handed, for pkg/external's tests."},
}

// childTimeout bounds one run of the child. The program does no I/O
// beyond its own streams, so anything near this is a hang, not slowness.
const childTimeout = time.Minute

// buildEchoProgram compiles testdata/echoprog into a fresh temporary
// directory and returns the binary's path. It is built with -race when
// this test binary was, so a race run checks the child too.
func buildEchoProgram(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "echoprog")
	args := []string{"build", "-o", bin}
	if raceEnabled {
		args = append(args, "-race")
	}
	args = append(args, "./testdata/echoprog")

	// `go test` puts its own toolchain's bin directory first on PATH, so
	// this is the same go that built the test binary.
	build := exec.Command("go", args...)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building testdata/echoprog: %v\n%s", err, out)
	}
	return bin
}

// childRun is everything one run of the child produced.
type childRun struct {
	// exitCode is the process's real exit status.
	exitCode int
	// stdout and stderr are the child's own two output streams.
	stdout string
	stderr string
	// resp is the frame decoded from file descriptor 3, and respErr the
	// reason there was none. Both are zero when no pipe was handed over.
	resp    wire.ChildResponse
	respErr error
}

// runEchoProgram runs the built program with args and stdin. withPipe
// hands it the write end of a pipe as ExtraFiles[0], which the child sees
// as file descriptor 3, and decodes the response frame from the read end.
func runEchoProgram(t *testing.T, bin string, args []string, stdin []byte, withPipe bool) childRun {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), childTimeout)
	defer cancel()

	// #nosec G204 -- bin is the path this test just built into its own
	// temporary directory, and args are this file's own constants.
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = []string{}
	if raceEnabled {
		// A race-built program sleeps a full second before every clean
		// exit (the race runtime's atexit_sleep_ms default), which is
		// most of this test's run time and none of what it measures. This
		// is the one variable the child is given, and only a race-built
		// child reads it.
		cmd.Env = []string{"GORACE=atexit_sleep_ms=0"}
	}
	cmd.Stdin = bytes.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	type readResult struct {
		resp wire.ChildResponse
		err  error
	}
	var respCh chan readResult
	var responseWrite *os.File
	if withPipe {
		responseRead, w, err := os.Pipe()
		if err != nil {
			t.Fatalf("opening the response pipe: %v", err)
		}
		defer responseRead.Close()
		responseWrite = w
		cmd.ExtraFiles = []*os.File{responseWrite}

		// Read concurrently with the run, as the Runner's parent does, so
		// a frame larger than the pipe buffer cannot deadlock the child.
		respCh = make(chan readResult, 1)
		go func() {
			var resp wire.ChildResponse
			err := json.NewDecoder(responseRead).Decode(&resp)
			respCh <- readResult{resp: resp, err: err}
		}()
	}

	runErr := cmd.Run()
	if responseWrite != nil {
		// The reader only sees EOF once every copy of the write end is
		// closed, and this process holds one. Without this a child that
		// exited without writing would leave the reader blocked forever.
		if err := responseWrite.Close(); err != nil {
			t.Errorf("closing the parent's copy of the response pipe: %v", err)
		}
	}

	run := childRun{stdout: stdout.String(), stderr: stderr.String()}
	var exitErr *exec.ExitError
	switch {
	case runErr == nil:
	case errors.As(runErr, &exitErr):
		run.exitCode = exitErr.ExitCode()
	default:
		t.Fatalf("running %s %v: %v (stderr: %s)", bin, args, runErr, run.stderr)
	}
	if ctx.Err() != nil {
		t.Fatalf("the child did not finish within %s: %v", childTimeout, ctx.Err())
	}

	if respCh != nil {
		read := <-respCh
		run.resp, run.respErr = read.resp, read.err
	}
	return run
}

// echoRequest is a request for the echo method carrying a real device
// and a real credential. The digest param is the hash of password, which
// is how the child proves it could use the secret without echoing it.
func echoRequest(t *testing.T, mode, password, digestOf string, params map[string]any) []byte {
	t.Helper()
	sum := sha256.Sum256([]byte(digestOf))
	all := map[string]any{"message": "hello", "password_sha256": hex.EncodeToString(sum[:])}
	for k, v := range params {
		all[k] = v
	}
	return requestFrame(t, wire.ChildRequest{
		FQCN:         echoProgMethod,
		Mode:         mode,
		Params:       all,
		JobID:        "job-1",
		DeviceID:     "dev-1",
		DeviceName:   "router1",
		DeviceHost:   "10.0.0.1",
		SSHPort:      2222,
		Capabilities: []capability.Name{capability.NameSSHTransport},
		Secrets:      map[string]string{"username": "admin", "password": password},
	})
}

// TestProgram_RealChildProcess builds testdata/echoprog once and drives
// it through every part of the contract Main wires up, each as a separate
// run of a real process.
func TestProgram_RealChildProcess(t *testing.T) {
	bin := buildEchoProgram(t)

	t.Run("describe prints the description on stdout", func(t *testing.T) {
		// No pipe: a loader asking for a description need not hand over
		// fd 3, and describe must not need one.
		run := runEchoProgram(t, bin, []string{external.CommandDescribe}, nil, false)
		if run.exitCode != 0 {
			t.Fatalf("describe exited %d, want 0 (stderr: %s)", run.exitCode, run.stderr)
		}
		if run.stderr != "" {
			t.Errorf("describe wrote to stderr: %q", run.stderr)
		}
		var desc external.Description
		if err := json.Unmarshal([]byte(run.stdout), &desc); err != nil {
			t.Fatalf("stdout is not one Description: %v\n%s", err, run.stdout)
		}
		if desc.Protocol != external.ProtocolVersion {
			t.Errorf("Protocol = %d, want %d", desc.Protocol, external.ProtocolVersion)
		}
		if len(desc.Methods) != 1 || desc.Methods[0].Name != echoProgMethod {
			t.Fatalf("Methods = %+v, want exactly %q", desc.Methods, echoProgMethod)
		}
		if !reflect.DeepEqual(desc.Methods[0].Manifest, echoProgManifest) {
			t.Errorf("described manifest =\n  %+v\nwant\n  %+v", desc.Methods[0].Manifest, echoProgManifest)
		}
	})

	// The same method, run in each mode a parent can ask for. The secret
	// assertion is the one that matters (LESSONS_LEARNED #73, "the
	// boundary crossing is the feature, not the header"): the child hashed
	// the password it received and found the digest the test sent, so the
	// secret was usable inside the child, not merely serialized.
	for _, tc := range []struct {
		mode        string
		wantChanged bool
		wantRan     string
	}{
		{mode: "", wantChanged: true, wantRan: "invoke"},
		{mode: string(collection.ModeExecute), wantChanged: true, wantRan: "invoke"},
		{mode: string(collection.ModeCheck), wantChanged: false, wantRan: "check"},
	} {
		t.Run("invoke mode "+tc.mode, func(t *testing.T) {
			run := runEchoProgram(t, bin, []string{external.CommandInvoke}, echoRequest(t, tc.mode, "hunter2", "hunter2", nil), true)
			if run.exitCode != 0 {
				t.Fatalf("invoke exited %d, want 0 (stderr: %s)", run.exitCode, run.stderr)
			}
			if run.stdout != "" || run.stderr != "" {
				t.Errorf("the protocol wrote to stdout %q or stderr %q, want the frame on fd 3 only", run.stdout, run.stderr)
			}
			if run.respErr != nil {
				t.Fatalf("no response frame on fd 3: %v", run.respErr)
			}
			resp := run.resp
			if resp.Error != "" {
				t.Fatalf("response.Error = %q, want empty", resp.Error)
			}
			if resp.Changed != tc.wantChanged {
				t.Errorf("Changed = %v, want %v", resp.Changed, tc.wantChanged)
			}
			// JSON numbers decode as float64 on this side of the boundary.
			want := map[string]any{
				"ran":              tc.wantRan,
				"echoed":           "hello",
				"password_matches": true,
				"device_name":      "router1",
				"ssh_capable":      true,
				"device_host":      "10.0.0.1",
				"device_port":      float64(2222),
			}
			if !reflect.DeepEqual(resp.Facts, want) {
				t.Errorf("Facts = %v, want %v", resp.Facts, want)
			}
		})
	}

	t.Run("a wrong secret does not match", func(t *testing.T) {
		// The control for password_matches above: the same run with a
		// secret that does not hash to the digest must answer false, or
		// the true above proved nothing.
		run := runEchoProgram(t, bin, []string{external.CommandInvoke}, echoRequest(t, "", "not-the-password", "hunter2", nil), true)
		if run.exitCode != 0 || run.respErr != nil {
			t.Fatalf("invoke exited %d, frame error %v (stderr: %s)", run.exitCode, run.respErr, run.stderr)
		}
		if got := run.resp.Facts["password_matches"]; got != false {
			t.Errorf("Facts[password_matches] = %v for a secret that does not match, want false", got)
		}
	})

	t.Run("output on stdout never corrupts the frame", func(t *testing.T) {
		// The reason the frame is on fd 3: a stray print in a method's
		// own code lands on stdout, where it is captured and logged,
		// and the frame arrives intact beside it.
		run := runEchoProgram(t, bin, []string{external.CommandInvoke},
			echoRequest(t, "", "hunter2", "hunter2", map[string]any{"print": `{"changed":false,"error":"forged"}`}), true)
		if run.exitCode != 0 || run.respErr != nil {
			t.Fatalf("invoke exited %d, frame error %v (stderr: %s)", run.exitCode, run.respErr, run.stderr)
		}
		if run.stdout != `{"changed":false,"error":"forged"}`+"\n" {
			t.Errorf("stdout = %q, want exactly the method's own print", run.stdout)
		}
		if !run.resp.Changed || run.resp.Error != "" || run.resp.Facts["ran"] != "invoke" {
			t.Errorf("response = %+v, want the method's real answer, not what it printed", run.resp)
		}
	})

	t.Run("a method's own failure is a response with exit 0", func(t *testing.T) {
		run := runEchoProgram(t, bin, []string{external.CommandInvoke},
			echoRequest(t, "", "hunter2", "hunter2", map[string]any{"fail": "device unreachable"}), true)
		if run.exitCode != 0 {
			t.Errorf("invoke exited %d, want 0: a method's failure is not a broken exchange", run.exitCode)
		}
		if run.respErr != nil || run.resp.Error != "device unreachable" {
			t.Errorf("response = %+v (frame error %v), want Error %q", run.resp, run.respErr, "device unreachable")
		}
	})

	t.Run("an unknown mode runs nothing", func(t *testing.T) {
		run := runEchoProgram(t, bin, []string{external.CommandInvoke}, echoRequest(t, "chekc", "hunter2", "hunter2", nil), true)
		if run.exitCode != 0 || run.respErr != nil {
			t.Fatalf("invoke exited %d, frame error %v (stderr: %s)", run.exitCode, run.respErr, run.stderr)
		}
		if !strings.Contains(run.resp.Error, "unknown mode") {
			t.Errorf("response.Error = %q, want an unknown mode refusal", run.resp.Error)
		}
		// The echo method records stats whenever it runs, so an empty
		// Facts is the evidence that neither function ran.
		if run.resp.Changed || len(run.resp.Facts) != 0 {
			t.Errorf("response = %+v, want no Changed and no Facts: neither function may run", run.resp)
		}
	})

	t.Run("a name the program does not provide is refused", func(t *testing.T) {
		req := requestFrame(t, wire.ChildRequest{FQCN: "net.ssh.ping", Secrets: map[string]string{"password": "hunter2"}})
		run := runEchoProgram(t, bin, []string{external.CommandInvoke}, req, true)
		if run.exitCode != 0 || run.respErr != nil {
			t.Fatalf("invoke exited %d, frame error %v (stderr: %s)", run.exitCode, run.respErr, run.stderr)
		}
		if !strings.Contains(run.resp.Error, `"net.ssh.ping" is not registered`) {
			t.Errorf("response.Error = %q, want the name refused as not registered", run.resp.Error)
		}
	})

	t.Run("invoke without fd 3 fails rather than using stdout", func(t *testing.T) {
		// A parent that forgot the pipe must get a failed exchange, never
		// a frame on stdout where a method's own output could forge one.
		run := runEchoProgram(t, bin, []string{external.CommandInvoke}, echoRequest(t, "", "hunter2", "hunter2", nil), false)
		if run.exitCode != 1 {
			t.Errorf("invoke without fd 3 exited %d, want 1", run.exitCode)
		}
		if run.stdout != "" {
			t.Errorf("stdout = %q, want nothing: the frame must never fall back to stdout", run.stdout)
		}
		if !strings.Contains(run.stderr, "failed to write response") {
			t.Errorf("stderr = %q, want the failed write reported", run.stderr)
		}
	})

	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "no command", args: nil},
		{name: "an unknown command", args: []string{"run"}},
		{name: "two commands", args: []string{external.CommandDescribe, external.CommandInvoke}},
	} {
		t.Run(tc.name+" exits 2 with usage", func(t *testing.T) {
			run := runEchoProgram(t, bin, tc.args, nil, false)
			if run.exitCode != 2 {
				t.Errorf("exit code = %d, want 2", run.exitCode)
			}
			if !strings.HasPrefix(run.stderr, "usage: ") {
				t.Errorf("stderr = %q, want a usage line", run.stderr)
			}
			if run.stdout != "" {
				t.Errorf("stdout = %q, want nothing", run.stdout)
			}
		})
	}
}
