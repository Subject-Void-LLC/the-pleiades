// Package externalscaffold_test: a script as an external Collection (forge
// new-external --script): what Generate accepts, and a built program running
// a real script through the real process boundary.
package externalscaffold_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/externalscaffold"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport/need"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/external"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// gateScript is the script the release gate's program wraps. Each case is
// chosen by the "case" param, matched in the params JSON on stdin, so one
// build answers every case. It runs under /bin/sh, the one interpreter every
// POSIX machine has.
const gateScript = `input=$(cat)
case "$input" in
*'"case":"ok"'*)
  printf '{"changed": true, "stats": {"got": %s, "where": "%s"}}\n' "$input" "$PWD" ;;
*'"case":"unchanged"'*)
  printf '{"changed": false, "facts": {"n": 3, "f": 1.5}}' ;;
*'"case":"dump"'*)
  to=$(printf '%s' "$input" | sed -n 's/.*"dump_to":"\([^"]*\)".*/\1/p')
  printf '%s\n' "$0" "$@" > "$to/argv"
  env > "$to/env"
  printf '%s' "$input" > "$to/stdin"
  printf '{"changed": false}' ;;
*'"case":"garbage"'*) echo hello ;;
*'"case":"two"'*) printf '{"changed": false}\n{"changed": true}\n' ;;
*'"case":"silent"'*) : ;;
*'"case":"no-changed"'*) printf '{"stats": {}}' ;;
*'"case":"null-changed"'*) printf '{"changed": null}' ;;
*'"case":"null"'*) printf 'null' ;;
*'"case":"extra"'*) printf '{"changed": true, "extra": 1}' ;;
*'"case":"reserved"'*) printf '{"changed": true, "stats": {"inverse": {}}}' ;;
*'"case":"twice"'*) printf '{"changed": true, "stats": {"k": 1}, "facts": {"k": 2}}' ;;
*'"case":"fail"'*)
  say=$(printf '%s' "$input" | sed -n 's/.*"say":"\([^"]*\)".*/\1/p')
  printf 'it went wrong: %s\n' "$say" >&2
  exit 3 ;;
*'"case":"flood"'*) head -c 2000000 /dev/zero | tr '\0' 'x'; printf '{"changed": false}' ;;
*'"case":"noisy"'*) head -c 100000 /dev/zero | tr '\0' 'e' >&2; exit 1 ;;
*) echo "no case in $input" >&2; exit 9 ;;
esac
`

// scriptConfig is the release gate's program: gateScript, run by /bin/sh.
func scriptConfig(name string) externalscaffold.Config {
	return externalscaffold.Config{Name: name, Script: []byte(gateScript), ScriptName: "gate.sh", Interpreter: need.ShellPath}
}

// TestGenerate_ScriptConfigIsChecked covers what Generate refuses of a
// script program, and the file it embeds the script as.
func TestGenerate_ScriptConfigIsChecked(t *testing.T) {
	script := []byte("echo '{\"changed\": false}'\n")
	for _, tc := range []struct {
		name    string
		cfg     externalscaffold.Config
		wantErr string
	}{
		{"an interpreter with no script", externalscaffold.Config{Name: "acme.run", Interpreter: "/bin/sh"}, "no script"},
		{"a script with no interpreter", externalscaffold.Config{Name: "acme.run", Script: script}, "--interpreter"},
		{"a relative interpreter", externalscaffold.Config{Name: "acme.run", Script: script, Interpreter: "python3"}, "not an absolute"},
		{"a dot-relative interpreter", externalscaffold.Config{Name: "acme.run", Script: script, Interpreter: "./python3"}, "not an absolute"},
		{"an unclean interpreter", externalscaffold.Config{Name: "acme.run", Script: script, Interpreter: "/usr/bin/../bin/sh"}, "not an absolute, clean"},
		{"a control character", externalscaffold.Config{Name: "acme.run", Script: script, Interpreter: "/bin/s\nh"}, "control character"},
		{"a backtick", externalscaffold.Config{Name: "acme.run", Script: script, Interpreter: "/bin/`sh`"}, "backtick"},
		{"an empty script", externalscaffold.Config{Name: "acme.run", Script: []byte{}, Interpreter: "/bin/sh"}, "empty"},
		{"a script over the size", externalscaffold.Config{Name: "acme.run", Script: bytes.Repeat([]byte("#"), externalscaffold.MaxScriptSize+1), Interpreter: "/bin/sh"}, "more than"},
		{"a script and a capability", externalscaffold.Config{Name: "acme.run", Script: script, Interpreter: "/bin/sh", Capability: "acme.thing"}, "defines no capability"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := externalscaffold.Generate(tc.cfg)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Generate = %v, want an error containing %q", err, tc.wantErr)
			}
		})
	}

	for scriptName, want := range map[string]string{
		"build.py":    "run-script.py",
		"pack.tar.gz": "run-script.gz",
		"noext":       "run-script",
		"odd.p-y":     "run-script",
		"":            "run-script",
	} {
		cfg := externalscaffold.Config{Name: "acme.run", Script: script, ScriptName: scriptName, Interpreter: "/bin/sh"}
		if got := cfg.ScriptFile(); got != want {
			t.Errorf("ScriptFile for %q = %q, want %q", scriptName, got, want)
		}
		files, err := externalscaffold.Generate(cfg)
		if err != nil {
			t.Fatal(err)
		}
		var embedded bool
		for _, f := range files {
			if f.Path == want {
				embedded = bytes.Equal(f.Content, script)
			}
			if f.Path == "run.go" && !strings.Contains(string(f.Content), "//go:embed "+want+"\n") {
				t.Errorf("run.go does not embed %s:\n%s", want, f.Content)
			}
		}
		if !embedded {
			t.Errorf("Generate wrote no %s holding the script's exact bytes", want)
		}
	}
}

// TestGenerate_ScriptReleaseGate builds a script program by its README's own
// lines and runs it over the real process boundary, as the loader does: the
// request on stdin, the response on file descriptor 3, and an environment
// The Pleiades chose. Its describe declares a controller-side method acting on
// no device, with no check and no undo, each with its reason. Every case of
// the answer contract is a real script printing it.
//
// The request carries a credential the program holds, as one does whenever
// the template binds one, and the program's own environment carries a value
// of its own. Neither may reach the script: the dump case records the
// script's whole argv, environment and stdin, and none of them holds either
// value.
func TestGenerate_ScriptReleaseGate(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping release gate test in -short mode")
	}
	need.POSIXShell(t)

	name := fmt.Sprintf("test.script%d.run", os.Getpid())
	bin := buildIn(t, scriptConfig(name), t.TempDir(), nil)

	t.Run("describe", func(t *testing.T) {
		out, err := runDescribe(bin)
		if err != nil {
			t.Fatalf("describe: %v\n%s", err, out)
		}
		var desc external.Description
		if err := json.Unmarshal(out, &desc); err != nil {
			t.Fatalf("describe printed %q: %v", out, err)
		}
		if len(desc.Methods) != 1 || desc.Methods[0].Name != name {
			t.Fatalf("Methods = %+v, want exactly %q", desc.Methods, name)
		}
		m := desc.Methods[0].Manifest
		if m.ExecutionContext.Site != collection.SiteController || m.ExecutionContext.Device != collection.DeviceNone {
			t.Errorf("ExecutionContext = %+v, want a controller-side method acting on no device", m.ExecutionContext)
		}
		if m.SupportsCheck || m.NoCheckReason == "" {
			t.Errorf("SupportsCheck = %v, NoCheckReason = %q; want no check, with a reason", m.SupportsCheck, m.NoCheckReason)
		}
		if m.Reversibility.Reversible || m.Reversibility.Notes == "" {
			t.Errorf("Reversibility = %+v, want not reversible, with a reason", m.Reversibility)
		}
		if len(m.SupportedTransports) != 0 || len(m.RequiredCapabilities) != 0 {
			t.Errorf("the method needs %v over %v; a script reaches no device", m.RequiredCapabilities, m.SupportedTransports)
		}
	})

	const (
		heldSecret = "canary-password-the-script-must-never-see"
		heldKey    = "canary-private-key-the-script-must-never-see"
		ownEnv     = "canary-environment-value-the-script-must-never-see"
	)
	tmp := t.TempDir()
	env := []string{"PATH=" + os.Getenv("PATH"), "TMPDIR=" + tmp, "HOME=" + t.TempDir(), "LEAK_PROBE=" + ownEnv}
	invoke := func(t *testing.T, mode string, params map[string]any) wire.ChildResponse {
		t.Helper()
		return invokeOverFD3(t, bin, wire.ChildRequest{
			FQCN: name, Mode: mode, JobID: "script-job", Params: params,
			Secrets: map[string]string{"password": heldSecret, "private_key": heldKey},
		}, env)
	}

	t.Run("the script gets the params and runs in the run's temporary directory", func(t *testing.T) {
		resp := invoke(t, string(collection.ModeExecute), map[string]any{"case": "ok", "note": "hello"})
		if resp.Error != "" || !resp.Changed {
			t.Fatalf("response = %+v, want changed and no error", resp)
		}
		got, _ := resp.Facts["got"].(map[string]any)
		if got["case"] != "ok" || got["note"] != "hello" {
			t.Errorf(`Facts["got"] = %v, want the params the task passed`, resp.Facts["got"])
		}
		if where, _ := resp.Facts["where"].(string); !strings.HasPrefix(where, tmp+string(filepath.Separator)) {
			t.Errorf("the script ran in %q, want a directory inside the program's TMPDIR %s", where, tmp)
		}
		if left, _ := os.ReadDir(tmp); len(left) != 0 {
			t.Errorf("the run left %d entries in TMPDIR, want its directory removed", len(left))
		}
	})

	t.Run("facts and stats are values the register reads", func(t *testing.T) {
		resp := invoke(t, string(collection.ModeExecute), map[string]any{"case": "unchanged"})
		if resp.Error != "" || resp.Changed {
			t.Fatalf("response = %+v, want unchanged and no error", resp)
		}
		if fmt.Sprint(resp.Facts["n"]) != "3" || fmt.Sprint(resp.Facts["f"]) != "1.5" {
			t.Errorf("Facts = %v, want n 3 and f 1.5", resp.Facts)
		}
	})

	t.Run("no credential reaches the script", func(t *testing.T) {
		dump := t.TempDir()
		params := map[string]any{"case": "dump", "dump_to": dump}
		resp := invoke(t, string(collection.ModeExecute), params)
		if resp.Error != "" {
			t.Fatalf("Error = %q", resp.Error)
		}
		read := func(file string) string {
			b, err := os.ReadFile(filepath.Join(dump, file))
			if err != nil {
				t.Fatalf("the script did not record its %s: %v", file, err)
			}
			return string(b)
		}
		argv, environ, stdin := read("argv"), read("env"), read("stdin")
		for what, body := range map[string]string{"argv": argv, "environment": environ, "stdin": stdin} {
			for _, canary := range []string{heldSecret, heldKey, ownEnv} {
				if strings.Contains(body, canary) {
					t.Errorf("the script's %s holds %q", what, canary)
				}
			}
		}
		if want, _ := json.Marshal(params); stdin != string(want) {
			t.Errorf("stdin = %q, want exactly the params %q", stdin, want)
		}
		if lines := strings.Split(strings.TrimSpace(argv), "\n"); len(lines) != 1 || filepath.Base(lines[0]) != scriptConfig(name).ScriptFile() {
			t.Errorf("argv = %q, want the script's path and nothing else", argv)
		}
		allowed := map[string]bool{"HOME": true, "TMPDIR": true, "PATH": true, "LANG": true, "LC_ALL": true, "TZ": true,
			// What the shell itself sets in its own environment.
			"PWD": true, "OLDPWD": true, "SHLVL": true, "_": true}
		for _, line := range strings.Split(strings.TrimSpace(environ), "\n") {
			key, _, _ := strings.Cut(line, "=")
			if !allowed[key] {
				t.Errorf("the script's environment holds %s, which the wrapper does not hand on", key)
			}
		}
	})

	for _, tc := range []struct {
		name      string
		params    map[string]any
		wantError []string
	}{
		{"a line that is not JSON fails", map[string]any{"case": "garbage"}, []string{"must be one JSON object"}},
		{"two objects fail", map[string]any{"case": "two"}, []string{"more than one JSON object"}},
		{"printing nothing fails", map[string]any{"case": "silent"}, []string{"printed nothing"}},
		{"an answer without changed fails", map[string]any{"case": "no-changed"}, []string{"holds no changed"}},
		{"a null changed fails", map[string]any{"case": "null-changed"}, []string{"changed must be true or false"}},
		{"null fails", map[string]any{"case": "null"}, []string{"must be one JSON object"}},
		{"an unknown field fails", map[string]any{"case": "extra"}, []string{`"extra" is not one of changed, stats or facts`}},
		{"a reserved key fails", map[string]any{"case": "reserved"}, []string{`"inverse" is a key The Pleiades reserves`}},
		{"a key in both fails", map[string]any{"case": "twice"}, []string{`"k" is set twice`}},
		{"a non-zero exit fails, with stderr masked", map[string]any{"case": "fail", "say": heldSecret}, []string{"exited 3", "it went wrong: ********"}},
		{"too much stdout fails", map[string]any{"case": "flood"}, []string{"more than 1048576 bytes on stdout"}},
		{"stderr is cut short", map[string]any{"case": "noisy"}, []string{"exited 1", "[stderr cut short]"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := invoke(t, string(collection.ModeExecute), tc.params)
			if resp.Changed {
				t.Error("Changed = true on a failed answer")
			}
			for _, want := range tc.wantError {
				if !strings.Contains(resp.Error, want) {
					t.Errorf("Error = %q, want it to contain %q", resp.Error, want)
				}
			}
			if strings.Contains(resp.Error, heldSecret) {
				t.Errorf("Error holds the credential: %q", resp.Error)
			}
			if len(resp.Error) > 8<<10 {
				t.Errorf("Error is %d bytes; a failure's stderr is bounded", len(resp.Error))
			}
		})
	}

	t.Run("a check is refused, not run", func(t *testing.T) {
		dump := t.TempDir()
		resp := invoke(t, string(collection.ModeCheck), map[string]any{"case": "dump", "dump_to": dump})
		if resp.Error == "" {
			t.Fatal("a check of a method without check support answered without an error")
		}
		if _, err := os.Stat(filepath.Join(dump, "stdin")); err == nil {
			t.Error("the script ran in check mode")
		}
	})
}

// runDescribe runs the built program with "describe" and an empty
// environment, as the loader's describe run does, and returns what it
// printed on stdout, or its stderr beside the error.
func runDescribe(bin string) ([]byte, error) {
	cmd := exec.Command(bin, external.CommandDescribe) // #nosec G204 -- a binary this test just built
	cmd.Env = []string{}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return stderr.Bytes(), err
	}
	return out, nil
}

// TestGenerate_AScriptIsInTheDigest: one script builds to one digest in two
// directories, by the README's build line, and a changed script to another.
// The approval list pins a build by its digest, so this is what makes an
// approval cover the script: an unchanged script keeps its approval, and an
// edited one is a new build to approve.
func TestGenerate_AScriptIsInTheDigest(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the script digest gate in -short mode")
	}
	need.POSIXShell(t)

	cfg := scriptConfig(fmt.Sprintf("test.scriptdigest%d.run", os.Getpid()))
	first := programDigest(t, buildIn(t, cfg, mkdirAll(t, filepath.Join(t.TempDir(), "a")), nil))
	second := programDigest(t, buildIn(t, cfg, mkdirAll(t, filepath.Join(t.TempDir(), "x", "y", "b")), nil))
	if first != second {
		t.Errorf("one script built to two digests, %s and %s", first, second)
	}

	cfg.Script = append([]byte("# one more line\n"), cfg.Script...)
	edited := programDigest(t, buildIn(t, cfg, mkdirAll(t, filepath.Join(t.TempDir(), "a")), nil))
	if edited == first {
		t.Errorf("an edited script built to the unedited one's digest, %s: an approval would cover a script nobody reviewed", first)
	}
}
