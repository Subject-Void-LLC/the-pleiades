// Tests for maccheck's flow, through a stand-in for the Mac, and for the
// pieces of its Mac end that run without one.
package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeRemote stands in for the Mac, recording which steps ran.
type fakeRemote struct {
	reachable, ready, ship error
	passed                 bool
	testErr                error
	steps                  []string
	script                 string
}

func (f *fakeRemote) Reachable(string) error {
	f.steps = append(f.steps, "reachable")
	return f.reachable
}
func (f *fakeRemote) Ready(string) error { f.steps = append(f.steps, "ready"); return f.ready }
func (f *fakeRemote) Ship(string, string) error {
	f.steps = append(f.steps, "ship")
	return f.ship
}
func (f *fakeRemote) Test(_, _, script string, _, _ io.Writer) (bool, error) {
	f.steps = append(f.steps, "test")
	f.script = script
	return f.passed, f.testErr
}

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

// TestRun_TheFlow drives every outcome: nothing configured (a skip with
// -optional, an error without), each step failing with what to do, a test
// failure and a pass.
func TestRun_TheFlow(t *testing.T) {
	dir := inRepo(t)
	t.Setenv(envKey, "")
	pkgs := []string{"github.com/x/a", "github.com/x/b"}
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
		{"unreachable", "me@mac", pkgs, &fakeRemote{reachable: errors.New("me@mac (10.0.0.9:22) did not answer")}, 2, "reachable", []string{"did not answer", "set PLEIADES_MAC in tests/mac/.env", "delete tests/mac/.env"}},
		{"not ready", "me@mac", pkgs, &fakeRemote{ready: errors.New("me@mac is not a Mac")}, 2, "reachable ready", []string{"is not a Mac", "delete tests/mac/.env"}},
		{"tests fail", "me@mac", pkgs, &fakeRemote{}, 1, "reachable ready ship test", []string{"the tests failed on me@mac"}},
		{"tests pass", "me@mac", pkgs, &fakeRemote{passed: true}, 0, "reachable ready ship test", []string{"all tests passed on me@mac"}},
		{"-mac wins over the setting", "me@mac", append([]string{"-mac", "other@mac2"}, pkgs...), &fakeRemote{passed: true}, 0, "reachable ready ship test", []string{"on other@mac2"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.configure != "" {
				writeEnv(t, dir, "# a comment\n\n"+envKey+"="+tc.configure+"\n")
			}
			var stdout, stderr bytes.Buffer
			code := run(tc.args, &stdout, &stderr, tc.remote)
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

// TestTestScript: the packages run in the commit's tree under caffeinate,
// both passes, and the script always ends by printing the exit status.
func TestTestScript(t *testing.T) {
	s := testScript("abc123", 2, []string{"github.com/x/a", "github.com/x/b"})
	for _, w := range []string{
		`cd "$HOME/.pleiades-maccheck/abc123"`,
		"caffeinate -dims /bin/sh -c 'go run ./tools/testgate -full -p 2 github.com/x/a github.com/x/b && go run ./tools/testgate -repeat 3 -strict -p 2 github.com/x/a github.com/x/b'",
		`echo "maccheck-exit=$?"`,
		"exec 2>&1",
	} {
		if !strings.Contains(s, w) {
			t.Errorf("script does not hold %q:\n%s", w, s)
		}
	}
}

// TestProbe_NamesWhatToDo: an address that never answers is refused
// within the probe's bound, saying what to check.
func TestProbe_NamesWhatToDo(t *testing.T) {
	was := probeTimeout
	probeTimeout = 300 * time.Millisecond
	t.Cleanup(func() { probeTimeout = was })
	// TEST-NET-1 (RFC 5737) is never routed, so the dial waits out the bound.
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

// TestReadyAnswer accepts only a Mac with go and caffeinate.
func TestReadyAnswer(t *testing.T) {
	for out, want := range map[string]string{
		"Darwin\ngo\ncaffeinate\n":  "",
		"Linux\ngo\ncaffeinate\n":   "is not a Mac",
		"Darwin\nno-go\ncaffeinate": "has no go",
		"Darwin\ngo\nno-caffeinate": "has no caffeinate",
		"":                          "answered the readiness check",
	} {
		err := readyAnswer("me@mac", out)
		if (want == "") != (err == nil) || err != nil && !strings.Contains(err.Error(), want) {
			t.Errorf("readyAnswer(%q) = %v, want %q", out, err, want)
		}
	}
}

// TestSplitMarker reads the exit status from the end of the run's output.
func TestSplitMarker(t *testing.T) {
	for out, want := range map[string]struct {
		output string
		code   int
		ok     bool
	}{
		"ok pkg\nmaccheck-exit=0":       {"ok pkg", 0, true},
		"FAIL pkg\nmaccheck-exit=1\n":   {"FAIL pkg", 1, true},
		"cut off before the end":        {"cut off before the end", 0, false},
		"maccheck-exit=x":               {"maccheck-exit=x", 0, false},
		"a maccheck-exit=9 in the text": {"a maccheck-exit=9 in the text", 0, false},
	} {
		output, code, ok := splitMarker(out)
		if output != want.output || code != want.code || ok != want.ok {
			t.Errorf("splitMarker(%q) = %q, %d, %v", out, output, code, ok)
		}
	}
}

// TestArgvYAML quotes each element so the script reaches the Mac whole.
func TestArgvYAML(t *testing.T) {
	if got := argvYAML("/bin/sh", "-c", "echo \"a\"\nexit 0"); got != `["/bin/sh", "-c", "echo \"a\"\nexit 0"]` {
		t.Errorf("argvYAML = %s", got)
	}
}

// TestConfigured: the environment names the Mac over the file, the file
// over nothing, and a file setting anything else is refused.
func TestConfigured(t *testing.T) {
	dir := inRepo(t)
	t.Setenv(envKey, "")
	if got, err := configured(); got != "" || err != nil {
		t.Errorf("with neither: %q, %v", got, err)
	}
	writeEnv(t, dir, envKey+"='me@mac'\n")
	if got, err := configured(); got != "me@mac" || err != nil {
		t.Errorf("from the file: %q, %v", got, err)
	}
	t.Setenv(envKey, "other@mac2")
	if got, err := configured(); got != "other@mac2" || err != nil {
		t.Errorf("the environment over the file: %q, %v", got, err)
	}
	t.Setenv(envKey, "")
	for content, want := range map[string]string{
		"PLEIADES_MAK=me@mac\n": "the only setting is PLEIADES_MAC",
		"just words\n":          "is not KEY=VALUE",
	} {
		writeEnv(t, dir, content)
		if _, err := configured(); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: %v, want it refused saying %q", content, err, want)
		}
	}
}
