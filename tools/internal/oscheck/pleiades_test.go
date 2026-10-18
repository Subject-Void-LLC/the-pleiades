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
