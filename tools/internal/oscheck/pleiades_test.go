// Tests for reading pleiades adhoc --json and writing its key:=yaml
// parameters.
package oscheck

import (
	"os"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// dirExists reports whether p is an existing directory.
func dirExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

// TestAdhocStdout reads the one task's stdout, and says why there is none:
// no report, a task that failed (the method's own error, or the run's), or
// a run that ran no task, with what validation said.
func TestAdhocStdout(t *testing.T) {
	for _, tc := range []struct {
		name, report, want, wantErr string
	}{
		{"a task that ran", `{"tasks":[{"status":"ok","stats":{"stdout":"Darwin\ngo\n"}}],"outcome":{"exit_code":0}}`, "Darwin\ngo\n", ""},
		{"no stdout stat", `{"tasks":[{"status":"ok","stats":{}}],"outcome":{"exit_code":0}}`, "", ""},
		{"a task that failed", `{"tasks":[{"status":"failed","error":"exec.command: command exited 3: err"}],"outcome":{"exit_code":1,"message":"execution failed"}}`, "", "exec.command: exec.command: command exited 3: err"},
		{"a run that failed", `{"tasks":[{"status":"ok"}],"outcome":{"exit_code":1,"message":"execution failed"}}`, "", "execution failed"},
		{"no task ran", `{"tasks":[],"validation":[{"message":"no device win-x"}],"outcome":{"exit_code":2,"message":"validation failed"}}`, "", "ran 0 tasks: validation failed; no device win-x"},
		{"no report", "pleiades: no such project\n", "", "printed no report"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := adhocStdout("exec.command", []byte(tc.report))
			if got != tc.want || (tc.wantErr == "") != (err == nil) || err != nil && !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("adhocStdout = %q, %v", got, err)
			}
		})
	}
}

// TestAdhocOutcome: a command that ran is an answer whatever its status,
// read from the failed task's own rc (exec.command) or exit_code
// (exec.winrm.shell), the shapes `pleiades adhoc --json` printed on the
// lab's boxes; one that did not run is an error naming why.
func TestAdhocOutcome(t *testing.T) {
	for _, tc := range []struct {
		name, report string
		want         Outcome
		wantErr      string
	}{
		{"it passed", `{"tasks":[{"status":"changed","stats":{"rc":0,"stdout":"ok a"}}],"outcome":{"exit_code":0}}`, Outcome{Output: "ok a"}, ""},
		{"exec.command failed", `{"tasks":[{"status":"failed","error":"exec.command: command exited 1: FAIL","stats":{"rc":1,"stdout":"FAIL a","stderr":""}}],"outcome":{"exit_code":1,"message":"execution failed"}}`, Outcome{Output: "FAIL a", ExitCode: 1}, ""},
		{"exec.winrm.shell failed", `{"tasks":[{"status":"failed","error":"exec.winrm.shell: script exited 2","stats":{"exit_code":2,"stdout":"no tree","result_known":true}}],"outcome":{"exit_code":1}}`, Outcome{Output: "no tree", ExitCode: 2}, ""},
		{"it did not run", `{"tasks":[{"status":"failed","error":"remoteexec: dial: connection refused"}],"outcome":{"exit_code":1}}`, Outcome{}, "connection refused"},
		{"its timeout ran out", `{"tasks":[{"status":"failed","error":"exec.command: context deadline exceeded (its timeout of 4h0m0s ran out)"}],"outcome":{"exit_code":1}}`, Outcome{}, "ran out"},
		{"no report", "", Outcome{}, "printed no report"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := adhocOutcome("exec.command", []byte(tc.report))
			if got != tc.want || (tc.wantErr == "") != (err == nil) || err != nil && !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("adhocOutcome = %+v, %v", got, err)
			}
		})
	}
}

// TestYAMLValues: each value reaches the method as it was, newlines,
// quotes and YAML's own syntax included, as YAML reads it.
func TestYAMLValues(t *testing.T) {
	script := "echo \"a: b\"\nexit 0 # {not: a map}"
	var argv []string
	if err := yaml.Unmarshal([]byte(ArgvYAML("/bin/sh", "-c", script)), &argv); err != nil || len(argv) != 3 || argv[2] != script {
		t.Errorf("ArgvYAML read back as %q, %v", argv, err)
	}
	env := map[string]string{"SHA": "abc", "PACKAGES": "a/b c/d", "ODD": "'\"{}[],: #"}
	var got map[string]string
	text := MapYAML(env)
	if err := yaml.Unmarshal([]byte(text), &got); err != nil || len(got) != 3 || got["ODD"] != env["ODD"] || got["PACKAGES"] != "a/b c/d" {
		t.Errorf("MapYAML %s read back as %v, %v", text, got, err)
	}
	if !strings.HasPrefix(text, `{"ODD": `) {
		t.Errorf("MapYAML's keys are not sorted: %s", text)
	}
}
