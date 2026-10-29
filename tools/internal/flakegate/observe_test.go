// Tests for an observed run, against the real toolchain: the observer sees
// the command before and after it starts and every event in order, and a
// signal to the run's group ends what the run started.
package flakegate

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// recorder is an Observer that keeps what it saw.
type recorder struct {
	prepared, started, ended *exec.Cmd
	events                   []Event
}

func (r *recorder) Prepare(cmd *exec.Cmd) {
	r.prepared = cmd
	cmd.Env = append(os.Environ(), "GATEPROOF_SEEN=yes")
}
func (r *recorder) Started(cmd *exec.Cmd) { r.started = cmd }
func (r *recorder) Event(e Event)         { r.events = append(r.events, e) }
func (r *recorder) Ended(cmd *exec.Cmd)   { r.ended = cmd }

// TestRunGoTestJSONObserved: the observer's Prepare changes the environment
// the test sees, Started gets the running command, and Event gets each
// package's start and end in order, the same events the run returns.
func TestRunGoTestJSONObserved(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs a throwaway module")
	}
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}
	write("go.mod", "module gateproof\n\ngo 1.22\n")
	write("proof_test.go", `package gateproof

import (
	"os"
	"testing"
)

func TestSeesTheEnvironment(t *testing.T) {
	if os.Getenv("GATEPROOF_SEEN") != "yes" {
		t.Fatal("the observer's environment did not reach the test")
	}
}
`)
	restore, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(restore) })

	obs := &recorder{}
	events, err := RunGoTestJSONObserved([]string{"-count=1"}, []string{"gateproof"}, nil, obs)
	if err != nil {
		t.Fatalf("the observed run failed: %v", err)
	}
	if obs.prepared == nil || obs.started != obs.prepared || obs.started.Process == nil {
		t.Errorf("Prepare and Started did not see the one command, before and after its start")
	}
	if obs.ended != obs.prepared || obs.ended.ProcessState == nil {
		t.Errorf("Ended did not see the command once it was waited for")
	}
	if len(obs.events) != len(events) {
		t.Errorf("the observer saw %d event(s), the run returned %d", len(obs.events), len(events))
	}
	var order []string
	for _, e := range obs.events {
		if e.Test == "" && (e.Action == "start" || e.Action == "pass" || e.Action == "fail") {
			order = append(order, e.Action)
		}
	}
	if got := strings.Join(order, " "); got != "start pass" {
		t.Errorf("the package's events were %q, want its start and then its pass", got)
	}
}
