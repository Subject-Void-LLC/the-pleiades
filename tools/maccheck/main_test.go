// Tests for maccheck's flow, through a stand-in for the Mac, and for the
// pieces of its Mac end that run without one.
package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/tools/internal/oscheck"
)

// fakeRemote stands in for the Mac, recording which steps ran.
type fakeRemote struct {
	reachable, ready, ship error
	passed                 bool
	testErr                error
	steps                  []string
}

func (f *fakeRemote) Reachable() error {
	f.steps = append(f.steps, "reachable")
	return f.reachable
}
func (f *fakeRemote) Ready() error { f.steps = append(f.steps, "ready"); return f.ready }
func (f *fakeRemote) Ship(string) error {
	f.steps = append(f.steps, "ship")
	return f.ship
}
func (f *fakeRemote) Test(context.Context, string, int, []string, io.Writer) (bool, error) {
	f.steps = append(f.steps, "test")
	return f.passed, f.testErr
}

// the is the stand-in, whatever Mac run asks for.
func (f *fakeRemote) the(string) oscheck.Leg { return f }

// inRepo runs t from a fresh git repository with one commit, so the
// flow's git calls (config, rev-parse, status) read a real one.
func inRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"-c", "user.name=m", "-c", "user.email=m@example.com", "commit", "-q", "--allow-empty", "-m", "base"},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

// writeEnv writes content to dir's tests/mac/.env, removed when t ends.
func writeEnv(t *testing.T, dir, content string) {
	t.Helper()
	p := filepath.Join(dir, envFile)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(p) })
}

// worktree adds a linked worktree of the repository t runs in, and
// returns its directory.
func worktree(t *testing.T) string {
	t.Helper()
	wt := filepath.Join(t.TempDir(), "wt")
	if out, err := exec.Command("git", "worktree", "add", "-q", wt).CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v\n%s", err, out)
	}
	return wt
}

// realPath is p with symbolic links resolved, as git reports it (a
// temporary directory on macOS is under a link).
func realPath(t *testing.T, p string) string {
	t.Helper()
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return real
}

// TestRun_TheFlow drives every outcome: nothing configured (a skip with
// -optional, an error without), a target or commit ssh or git would read
// as an option, each step failing with what to do, a test failure and a
// pass.
func TestRun_TheFlow(t *testing.T) {
	dir := inRepo(t)
	t.Setenv(envKey, "")
	pkgs := []string{"github.com/x/a", "github.com/x/b"}
	file := filepath.Join(realPath(t, dir), envFile)
	for _, tc := range []struct {
		name       string
		configure  string
		args       []string
		remote     *fakeRemote
		wantCode   int
		wantSteps  string
		wantOutput []string
	}{
		{"nothing configured, optional", "", append([]string{"-optional"}, pkgs...), &fakeRemote{}, 0, "", []string{"no Mac configured", "macOS was NOT checked"}},
		{"nothing configured", "", pkgs, &fakeRemote{}, 2, "", []string{"no Mac is configured", "cp tests/mac/.env.example tests/mac/.env"}},
		{"no packages", "me@mac", nil, &fakeRemote{}, 0, "", []string{"changes no container-free package"}},
		{"not an import path", "me@mac", []string{"a b; rm -rf /"}, &fakeRemote{}, 2, "", []string{"is not an import path"}},
		{"a target ssh would read as an option", "", append([]string{"-mac", "-oProxyCommand=touch x"}, pkgs...), &fakeRemote{}, 2, "", []string{`begins with "-"`, "name it with -mac or PLEIADES_MAC", "unset PLEIADES_MAC"}},
		{"a target with a comment after it", "me@mac # mine", pkgs, &fakeRemote{}, 2, "", []string{"holds a space", "set PLEIADES_MAC in " + file, "delete " + file}},
		{"a commit git would read as an option", "me@mac", append([]string{"-commit", "--output=x"}, pkgs...), &fakeRemote{}, 2, "", []string{"which git reads as an option"}},
		{"not a commit", "me@mac", append([]string{"-commit", "nosuchref"}, pkgs...), &fakeRemote{}, 2, "", []string{`"nosuchref" is not a commit`}},
		{"unreachable", "me@mac", pkgs, &fakeRemote{reachable: errors.New("me@mac (10.0.0.9:22) did not answer")}, 2, "reachable", []string{"did not answer", "set PLEIADES_MAC in " + file, "delete " + file}},
		{"not ready", "me@mac", pkgs, &fakeRemote{ready: errors.New("me@mac is not a Mac")}, 2, "reachable ready", []string{"is not a Mac", "delete " + file}},
		{"tests fail", "me@mac", pkgs, &fakeRemote{}, 1, "reachable ready ship test", []string{"the tests failed on me@mac"}},
		{"tests pass", "me@mac", pkgs, &fakeRemote{passed: true}, 0, "reachable ready ship test", []string{"all tests passed on me@mac"}},
		{"-mac wins over the setting", "me@mac", append([]string{"-mac", "other@mac2"}, pkgs...), &fakeRemote{passed: true}, 0, "reachable ready ship test", []string{"on other@mac2"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.configure != "" {
				writeEnv(t, dir, "# a comment\n\n"+envKey+"="+tc.configure+"\n")
			}
			var stdout, stderr bytes.Buffer
			code := run(context.Background(), tc.args, &stdout, &stderr, tc.remote.the)
			out := stdout.String() + stderr.String()
			if code != tc.wantCode || strings.Join(tc.remote.steps, " ") != tc.wantSteps {
				t.Fatalf("exit %d after steps %v, want %d after %q\n%s", code, tc.remote.steps, tc.wantCode, tc.wantSteps, out)
			}
			for _, w := range tc.wantOutput {
				if !strings.Contains(out, w) {
					t.Errorf("output does not say %q:\n%s", w, out)
				}
			}
		})
	}
}

// TestRun_AWorktreeReadsTheMainWorktreesSettings: a linked worktree with
// no tests/mac/.env of its own uses the main worktree's, and names that
// file in what to do; one of its own wins.
func TestRun_AWorktreeReadsTheMainWorktreesSettings(t *testing.T) {
	dir := inRepo(t)
	t.Setenv(envKey, "")
	writeEnv(t, dir, envKey+"=me@mac\n")
	wt := worktree(t)
	t.Chdir(wt)
	pkgs := []string{"github.com/x/a"}

	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), append([]string{"-optional"}, pkgs...), &stdout, &stderr, (&fakeRemote{passed: true}).the); code != 0 || !strings.Contains(stdout.String(), "all tests passed on me@mac") {
		t.Fatalf("exit %d:\n%s%s", code, &stdout, &stderr)
	}
	stdout.Reset()
	stderr.Reset()
	if code := run(context.Background(), pkgs, &stdout, &stderr, (&fakeRemote{reachable: errors.New("did not answer")}).the); code != 2 || !strings.Contains(stderr.String(), "delete "+filepath.Join(realPath(t, dir), envFile)) {
		t.Fatalf("exit %d, not naming the main worktree's file:\n%s%s", code, &stdout, &stderr)
	}

	writeEnv(t, wt, envKey+"=other@mac2\n")
	stdout.Reset()
	stderr.Reset()
	if code := run(context.Background(), pkgs, &stdout, &stderr, (&fakeRemote{passed: true}).the); code != 0 || !strings.Contains(stdout.String(), "all tests passed on other@mac2") {
		t.Fatalf("the worktree's own file did not win: exit %d:\n%s%s", code, &stdout, &stderr)
	}
}

// TestPrivateProject_IsTheWorktreesOwn: each worktree drives the Mac from
// a project and binary of its own, so two running at once never build
// over each other's.
func TestPrivateProject_IsTheWorktreesOwn(t *testing.T) {
	dir := realPath(t, inRepo(t))
	main, err := privateProject()
	if err != nil || main != filepath.Join(dir, ".git", "pleiades-maccheck") {
		t.Fatalf("main worktree: %q, %v", main, err)
	}
	t.Chdir(worktree(t))
	linked, err := privateProject()
	if err != nil || linked != filepath.Join(dir, ".git", "worktrees", "wt", "pleiades-maccheck") {
		t.Fatalf("linked worktree: %q, %v", linked, err)
	}
}

// TestCheckTarget refuses what ssh would read as an option, and what
// cannot name one host.
func TestCheckTarget(t *testing.T) {
	for target, want := range map[string]string{
		"me@mac":               "",
		"mac-alias":            "",
		"me@10.0.0.9":          "",
		"-oProxyCommand=sh":    `begins with "-"`,
		"me@mac # mine":        "holds a space",
		"me@mac\n-oPermit=yes": "holds a space",
		"me@\x00mac":           "control character",
	} {
		err := checkTarget(target)
		if (want == "") != (err == nil) || err != nil && !strings.Contains(err.Error(), want) {
			t.Errorf("checkTarget(%q) = %v, want %q", target, err, want)
		}
	}
}

// TestProbe_NamesWhatToDo: an address that never answers is refused
// within the probe's bound, saying what to check.
func TestProbe_NamesWhatToDo(t *testing.T) {
	was := probeTimeout
	probeTimeout = 300 * time.Millisecond
	t.Cleanup(func() { probeTimeout = was })
	// TEST-NET-1 (RFC 5737) is never routed, so the dial fails, at once or
	// when the bound runs out.
	err := probe("me@mac", "192.0.2.1", "22")
	if err == nil || !strings.Contains(err.Error(), "did not answer on its SSH port") || !strings.Contains(err.Error(), "Remote Login") {
		t.Fatalf("probe = %v", err)
	}
	if err := probe("me@mac", "", ""); err == nil {
		t.Error("an empty endpoint was probed")
	}
}

// TestParseSSHG reads what ssh -G prints for an alias.
func TestParseSSHG(t *testing.T) {
	got := parseSSHG([]byte("user me\nhostname 10.0.0.9\nport 2222\nidentityfile ~/.ssh/id_ed25519\nidentityfile ~/.ssh/id_rsa\nbatchmode no\n"))
	if got.host != "10.0.0.9" || got.port != "2222" || got.user != "me" || strings.Join(got.keys, ",") != "~/.ssh/id_ed25519,~/.ssh/id_rsa" {
		t.Errorf("parseSSHG = %+v", got)
	}
}

// TestConfigured: the environment names the Mac over the file, the file
// over nothing, and a file setting anything else is refused, naming it.
func TestConfigured(t *testing.T) {
	dir := inRepo(t)
	file := filepath.Join(realPath(t, dir), envFile)
	t.Setenv(envKey, "")
	if got, from, err := configured(); got != "" || from != "" || err != nil {
		t.Errorf("with neither: %q from %q, %v", got, from, err)
	}
	writeEnv(t, dir, envKey+"='me@mac'\n")
	if got, from, err := configured(); got != "me@mac" || from != file || err != nil {
		t.Errorf("from the file: %q from %q, %v", got, from, err)
	}
	t.Setenv(envKey, "other@mac2")
	if got, from, err := configured(); got != "other@mac2" || from != "" || err != nil {
		t.Errorf("the environment over the file: %q from %q, %v", got, from, err)
	}
	t.Setenv(envKey, "")
	for content, want := range map[string]string{
		"PLEIADES_MAK=me@mac\n": "the only setting is PLEIADES_MAC",
		"just words\n":          "is not KEY=VALUE",
	} {
		writeEnv(t, dir, content)
		if _, _, err := configured(); err == nil || !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), file) {
			t.Errorf("%q: %v, want it refused saying %q and naming %s", content, err, want, file)
		}
	}
}

// TestLoginFailure names the fix for each way ssh refuses.
func TestLoginFailure(t *testing.T) {
	for said, want := range map[string]string{
		"me@mac: Permission denied (publickey,password).":      "ssh-copy-id -i /home/me/.ssh/id_ed25519.pub me@mac",
		"Host key verification failed.":                        "accept its key once: ssh me@mac true",
		"ssh: connect to host mac port 22: Connection refused": "ssh could not log in to me@mac",
	} {
		err := loginFailure("me@mac", "/home/me/.ssh/id_ed25519", said, errors.New("exit status 255"))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("loginFailure(%q) = %v, want it to say %q", said, err, want)
		}
	}
}

// TestRun_Phases: prepare checks the Mac and sends the tree and runs
// nothing; test runs the tests alone, after a liveness probe, and says how
// long they took; any other phase is refused before anything runs.
func TestRun_Phases(t *testing.T) {
	dir := inRepo(t)
	t.Setenv(envKey, "")
	writeEnv(t, dir, envKey+"=me@mac\n")
	pkgs := []string{"github.com/x/a"}
	for _, tc := range []struct {
		phase     string
		wantCode  int
		wantSteps string
		want      string
	}{
		{"prepare", 0, "reachable ready ship", "-phase test runs its tests"},
		{"test", 0, "reachable test", "all tests passed on me@mac (macOS) in "},
		{"all", 0, "reachable ready ship test", "all tests passed on me@mac (macOS) in "},
		{"later", 2, "", `not "later"`},
	} {
		t.Run(tc.phase, func(t *testing.T) {
			remote := &fakeRemote{passed: true}
			var stdout, stderr bytes.Buffer
			code := run(context.Background(), append([]string{"-phase", tc.phase}, pkgs...), &stdout, &stderr, remote.the)
			out := stdout.String() + stderr.String()
			if code != tc.wantCode || strings.Join(remote.steps, " ") != tc.wantSteps || !strings.Contains(out, tc.want) {
				t.Errorf("exit %d after %v, want %d after %q saying %q:\n%s", code, remote.steps, tc.wantCode, tc.wantSteps, tc.want, out)
			}
		})
	}
}

// TestRun_AnInterruptIsNotTheMacsFault: tests stopped from this side end
// with the remote's word for it and no advice to change or remove the Mac.
func TestRun_AnInterruptIsNotTheMacsFault(t *testing.T) {
	dir := inRepo(t)
	t.Setenv(envKey, "")
	writeEnv(t, dir, envKey+"=me@mac\n")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	remote := &fakeRemote{testErr: errors.New("interrupted; the tests on me@mac were stopped")}
	var stdout, stderr bytes.Buffer
	code := run(ctx, []string{"-phase", "test", "github.com/x/a"}, &stdout, &stderr, remote.the)
	if code != 2 || !strings.Contains(stderr.String(), "were stopped") || strings.Contains(stderr.String(), "To test on another Mac") {
		t.Errorf("exit %d:\n%s", code, &stderr)
	}
}

// TestRun_ReadyFileOnlyWhenTheTreeWasSent: prepare writes the ready file
// once the tree is on the Mac, and not when nothing is to run or the Mac
// is not configured, so a caller starts -phase test only when it has a
// tree to test.
func TestRun_ReadyFileOnlyWhenTheTreeWasSent(t *testing.T) {
	dir := inRepo(t)
	t.Setenv(envKey, "")
	ready := filepath.Join(t.TempDir(), "ready")
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"-optional", "-phase", "prepare", "-ready-file", ready, "github.com/x/a"}, &stdout, &stderr, (&fakeRemote{}).the); code != 0 || oscheck.FileExists(ready) {
		t.Fatalf("no Mac configured: exit %d, ready file written %v", code, oscheck.FileExists(ready))
	}
	writeEnv(t, dir, envKey+"=me@mac\n")
	if code := run(context.Background(), []string{"-phase", "prepare", "-ready-file", ready}, &stdout, &stderr, (&fakeRemote{}).the); code != 0 || oscheck.FileExists(ready) {
		t.Fatalf("no packages: exit %d, ready file written %v", code, oscheck.FileExists(ready))
	}
	if code := run(context.Background(), []string{"-phase", "prepare", "-ready-file", ready, "github.com/x/a"}, &stdout, &stderr, (&fakeRemote{}).the); code != 0 || !oscheck.FileExists(ready) {
		t.Fatalf("a tree sent: exit %d, ready file written %v\n%s%s", code, oscheck.FileExists(ready), &stdout, &stderr)
	}
}
