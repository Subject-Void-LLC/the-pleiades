// Package externalscaffold_test: the release gate: a generated program built
// from its README and run through the real process boundary.
package externalscaffold_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/externalscaffold"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/external"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// repoRoot locates the module root from this test's own package
// directory (internal/forge/externalscaffold), so a generated program can
// be built against the pkg/ code in this same tree.
func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	return filepath.Clean(filepath.Join(wd, "..", "..", ".."))
}

// TestGenerate_ReleaseGate is the release gate for `forge new-external`:
// a generated program builds, its own generated test passes, and the
// built binary answers both commands of the external Collection contract
// through the real process boundary Pleiades uses.
//
// Every step runs for real. The program is written, with its own
// generated go.mod, into a fresh directory outside this module, and built
// by the README's own offline commands (buildOutOfTree): pointed at this
// checkout, tidied, tested and built as real go subprocesses with the
// network off. The binary is then started
// the way Pleiades starts it: one argument, the request on stdin, the
// response on a real pipe passed as file descriptor 3, and an environment
// cut down to the one variable the method needs. For the method's own
// invocations the target is a real SSH server (remoteexectest, a genuine
// key exchange and a real /bin/sh), verified against a real known_hosts
// entry, so the recorded stat is what `uname -a` really printed. Nothing
// here is a buffer standing in for a stream. That is what RULE 0 asks of
// a gate: Main's own stream wiring is exercised, not just Serve's.
func TestGenerate_ReleaseGate(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping release gate test in -short mode")
	}
	if runtime.GOOS == "windows" {
		t.Skip("the gate's SSH server runs each command through /bin/sh")
	}

	name := fmt.Sprintf("test.relgate%d.probe", os.Getpid())
	bin := buildOutOfTree(t, name)

	t.Run("describe", func(t *testing.T) {
		assertDescribe(t, bin, name)
	})

	srv, err := remoteexectest.Start(remoteexectest.Options{})
	if err != nil {
		t.Fatalf("starting the SSH server: %v", err)
	}
	t.Cleanup(srv.Close)

	knownHosts := filepath.Join(t.TempDir(), "known_hosts")
	if err := os.WriteFile(knownHosts, []byte(knownhosts.Line([]string{srv.Addr()}, srv.HostKey)+"\n"), 0o600); err != nil {
		t.Fatalf("writing known_hosts: %v", err)
	}
	strangers := filepath.Join(t.TempDir(), "known_hosts_empty")
	if err := os.WriteFile(strangers, []byte("\n"), 0o600); err != nil {
		t.Fatalf("writing an empty known_hosts: %v", err)
	}

	wantUname := localUname(t)

	// target is the request for the generated method against the real
	// server, with mode filled in per case.
	target := func(mode string) wire.ChildRequest {
		return wire.ChildRequest{
			FQCN:         name,
			Mode:         mode,
			JobID:        "relgate-job",
			DeviceID:     "relgate-device",
			DeviceName:   "relgate-device",
			DeviceHost:   srv.Host,
			SSHPort:      srv.Port,
			Capabilities: []capability.Name{capability.NameSSHTransport},
			Secrets:      srv.Secrets(),
		}
	}

	tests := []struct {
		name       string
		request    wire.ChildRequest
		knownHosts string
		wantError  string
		wantUname  bool
	}{
		{
			name:       "an unknown method is answered with an error, not run",
			request:    wire.ChildRequest{FQCN: "no.such.method", Mode: string(collection.ModeExecute)},
			knownHosts: knownHosts,
			wantError:  `collection method "no.such.method" is not registered`,
		},
		{
			name:       "execute runs the method and records what the device printed",
			request:    target(string(collection.ModeExecute)),
			knownHosts: knownHosts,
			wantUname:  true,
		},
		{
			name:       "an empty mode is execute",
			request:    target(""),
			knownHosts: knownHosts,
			wantUname:  true,
		},
		{
			name:       "check reaches Check, which reads and reports no change",
			request:    target(string(collection.ModeCheck)),
			knownHosts: knownHosts,
			wantUname:  true,
		},
		{
			name:       "an unknown mode is refused before anything runs",
			request:    target("chekc"),
			knownHosts: knownHosts,
			wantError:  "unknown mode",
		},
		{
			name:       "an unknown host key is refused, never trusted",
			request:    target(string(collection.ModeCheck)),
			knownHosts: strangers,
			wantError:  "knownhosts",
		},
	}

	for _, tt := range tests {
		t.Run("invoke/"+tt.name, func(t *testing.T) {
			resp := invokeOverFD3(t, bin, tt.request, []string{remoteexec.KnownHostsEnv + "=" + tt.knownHosts})

			if tt.wantError != "" {
				if !strings.Contains(resp.Error, tt.wantError) {
					t.Fatalf("Error = %q, want it to contain %q", resp.Error, tt.wantError)
				}
				return
			}
			if resp.Error != "" {
				t.Fatalf("Error = %q, want none", resp.Error)
			}
			if resp.Changed {
				t.Error("Changed = true, want false: the generated method only reads")
			}
			if tt.wantUname {
				got, _ := resp.Facts["uname"].(string)
				if got != wantUname {
					t.Errorf(`Facts["uname"] = %q, want what uname -a printed here, %q`, got, wantUname)
				}
			}
		})
	}
}

// buildOutOfTree generates the program for name, writes it to a fresh
// directory outside this module, and builds it by running the README's own
// offline commands, read out of the generated README rather than retyped
// here, with the local checkout path replaced by this one and the network
// switched off. It returns the built binary. That is the path an author
// with no network takes, and it proves the generated go.mod, the README's
// steps and the program agree: a README step that stopped working, or a
// go.mod that needed more than a module and a go line, fails here.
func buildOutOfTree(t *testing.T, name string) string {
	t.Helper()
	files, err := externalscaffold.Generate(externalscaffold.Config{Name: name})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	dir := t.TempDir()
	var readme string
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(dir, f.Path), f.Content, 0o600); err != nil {
			t.Fatalf("writing %s: %v", f.Path, err)
		}
		if f.Path == "README.md" {
			readme = string(f.Content)
		}
	}

	commands := offlineBuildCommands(t, readme)
	env := append(os.Environ(), "GOPROXY=off", "GOTOOLCHAIN=local", "GOFLAGS=-mod=mod")
	for _, line := range commands {
		line = strings.ReplaceAll(line, "/path/to/the-pleiades", repoRoot(t))
		cmd := exec.Command("sh", "-c", line) // #nosec G204 -- the generated README's own command, the thing under test
		cmd.Dir = dir
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("the README's step %q failed offline: %v\n%s", line, err, out)
		}
	}
	runGo(t, dir, "vet", "./...")

	bin := filepath.Join(dir, externalscaffold.Config{Name: name}.DefaultDir())
	if _, err := os.Stat(bin); err != nil {
		t.Fatalf("the README's build step left no %s: %v", filepath.Base(bin), err)
	}
	return bin
}

// offlineBuildCommands returns the commands of the README's offline build
// block: the fenced sh block that starts by pointing the module at a local
// checkout. It fails the test if there is no such block.
func offlineBuildCommands(t *testing.T, readme string) []string {
	t.Helper()
	for _, block := range strings.Split(readme, "~~~sh\n")[1:] {
		body, _, _ := strings.Cut(block, "~~~")
		lines := strings.Split(strings.TrimSpace(body), "\n")
		if strings.HasPrefix(lines[0], "go mod edit -replace github.com/Subject-Void-LLC/the-pleiades=") {
			return lines
		}
	}
	t.Fatalf("the README has no offline build block:\n%s", readme)
	return nil
}

// assertDescribe runs the built program with "describe" and checks the
// document Pleiades would load from it: the protocol version and exactly
// one method, with the contract the generator promises.
func assertDescribe(t *testing.T, bin, name string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, bin, external.CommandDescribe) // #nosec G204 -- a binary this test just built
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	cmd.Env = []string{}
	if err := cmd.Run(); err != nil {
		t.Fatalf("%s describe: %v\nstderr:\n%s", bin, err, stderr.String())
	}

	var desc external.Description
	if err := json.Unmarshal(stdout.Bytes(), &desc); err != nil {
		t.Fatalf("describe output is not a Description: %v\n%s", err, stdout.String())
	}
	if desc.Protocol != external.ProtocolVersion {
		t.Errorf("Protocol = %d, want %d", desc.Protocol, external.ProtocolVersion)
	}
	if len(desc.Methods) != 1 || desc.Methods[0].Name != name {
		t.Fatalf("Methods = %+v, want exactly one named %q", desc.Methods, name)
	}

	m := desc.Methods[0].Manifest
	if m.Status != collection.StatusImplemented {
		t.Errorf("Status = %q, want %q", m.Status, collection.StatusImplemented)
	}
	if !m.SupportsCheck {
		t.Error("SupportsCheck = false, want true")
	}
	if len(m.RequiredCapabilities) != 1 || m.RequiredCapabilities[0] != capability.NameSSHTransport {
		t.Errorf("RequiredCapabilities = %v, want [%s]", m.RequiredCapabilities, capability.NameSSHTransport)
	}
	if m.Reversibility.Reversible || m.Reversibility.Notes == "" {
		t.Errorf("Reversibility = %+v, want not reversible, with a reason", m.Reversibility)
	}
	if m.Doc.Summary == "" || len(m.Doc.Examples) == 0 {
		t.Errorf("Doc = %+v, want a summary and at least one example", m.Doc)
	}
}

// invokeOverFD3 runs the built program with "invoke" exactly as Pleiades
// does: the request as JSON on stdin, and the write end of a real pipe as
// the child's file descriptor 3, which is where external.Main sends the
// one response. env replaces the whole environment, the way the loader's
// allowlist does.
func invokeOverFD3(t *testing.T, bin string, req wire.ChildRequest, env []string) wire.ChildResponse {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("encoding the request: %v", err)
	}

	respR, respW, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer func() { _ = respR.Close() }()

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, bin, external.CommandInvoke) // #nosec G204 -- a binary this test just built
	cmd.Stdin = bytes.NewReader(body)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	// ExtraFiles[0] becomes file descriptor 3 in the child.
	cmd.ExtraFiles = []*os.File{respW}
	cmd.Env = env

	if err := cmd.Start(); err != nil {
		_ = respW.Close()
		t.Fatalf("starting %s invoke: %v", bin, err)
	}
	// The child holds its own copy of the write end now. Closing the
	// parent's is what lets the read below see end of file when the
	// child exits, rather than wait forever on a writer that is this
	// very process.
	_ = respW.Close()

	raw, readErr := io.ReadAll(respR)
	waitErr := cmd.Wait()
	if readErr != nil {
		t.Fatalf("reading file descriptor 3: %v", readErr)
	}
	if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			t.Fatalf("%s invoke exited %d, want 0 (a method's failure travels in the response)\nstderr:\n%s", bin, exitErr.ExitCode(), stderr.String())
		}
		t.Fatalf("%s invoke: %v", bin, waitErr)
	}

	var resp wire.ChildResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("file descriptor 3 did not carry a ChildResponse: %v\n%q\nstderr:\n%s", err, raw, stderr.String())
	}
	return resp
}

// localUname is what `uname -a` prints on this machine. The gate's SSH
// server runs every command through this machine's own /bin/sh, so the
// generated method's stat must match it exactly.
func localUname(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("uname", "-a").Output()
	if err != nil {
		t.Fatalf("uname -a: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// runGo runs the real go command in dir and fails the test with its full
// output when it fails.
func runGo(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go %v failed: %v\n%s", args, err, out)
	}
}
