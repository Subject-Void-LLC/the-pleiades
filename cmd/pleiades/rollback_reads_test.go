// Gate for a rollback beneath later runs whose tasks failed, through the real
// binary against a scripted IOS router over real SSH: a show the router
// refused, and a show that failed at the SSH handshake and never logged in,
// changed nothing, so neither run stands in the way; a command that changes
// the device and failed may have changed part of it, so its run still does,
// until it is named with --despite-run. The CML window of 2026-11-17 needed a
// --despite-run for each failed show (FAILURE_PATTERNS 1269).
package main_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	cryptossh "golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
)

// readsRouter is a scripted IOS router that refuses "show bogus" as IOS
// refuses a line it does not know, and answers clear counters' question.
func readsRouter(t *testing.T) *remoteexectest.Server {
	t.Helper()
	srv, err := remoteexectest.Start(remoteexectest.Options{Device: &remoteexectest.Device{
		Prompt: "r1#",
		Replies: map[string]string{
			"show version": "Cisco IOS XE Software, Version 17.15.4",
			"show bogus":   "                ^\r\n% Invalid input detected at '^' marker.",
		},
		Asks: map[string][]remoteexectest.Question{
			"clear counters": {{Text: "Clear \"show interface\" counters on all interfaces [confirm]", Want: "y", Declined: "% Counters not cleared"}},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	return srv
}

// readsHome is a home whose known_hosts names srv's address with key.
func readsHome(t *testing.T, srv *remoteexectest.Server, key cryptossh.PublicKey) string {
	t.Helper()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	line := knownhosts.Line([]string{net.JoinHostPort(srv.Host, strconv.Itoa(srv.Port))}, key) + "\n"
	if err := os.WriteFile(filepath.Join(home, ".ssh", "known_hosts"), []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	return home
}

// readsRunbook is one net.ios.command task, with the header every runbook
// opens with.
func readsRunbook(t *testing.T, dir, name, task string) string {
	t.Helper()
	body := "# What this does: one exec-mode command on r1, for the rollback gate.\n# Blast radius: r1. Changes: what the command changes. Undo: as written.\nid: " + name + "\nhosts: r1\ntasks:\n" + task
	rel := filepath.Join("runbooks", name+".yaml")
	if err := os.WriteFile(filepath.Join(dir, rel), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return rel
}

// rollbackProblems decodes a rollback report's problems.
func rollbackProblems(t *testing.T, raw json.RawMessage) []struct {
	Reason string `json:"reason"`
	Accept string `json:"accept"`
} {
	t.Helper()
	var rb struct {
		Problems []struct {
			Reason string `json:"reason"`
			Accept string `json:"accept"`
		} `json:"problems"`
	}
	if err := json.Unmarshal(raw, &rb); err != nil {
		t.Fatalf("the rollback report does not decode: %v\n%s", err, raw)
	}
	return rb.Problems
}

// TestCLI_ARollbackBeneathFailedReadsNeedsNoDespiteRun is the gate.
func TestCLI_ARollbackBeneathFailedReadsNeedsNoDespiteRun(t *testing.T) {
	srv := readsRouter(t)
	dir := t.TempDir()
	home := readsHome(t, srv, srv.HostKey)
	for _, args := range [][]string{
		{"init"},
		{"add-host", "r1", "--type", "cisco_router", "--set", "host=" + srv.Host, "--set", "port=" + strconv.Itoa(srv.Port)},
	} {
		if out, err := runPleiades(t, dir, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	if out, err := runPleiadesWithStdin(t, dir, srv.Password+"\n", "add-credential", "r1", "--username", srv.Username, "--password-stdin"); err != nil {
		t.Fatalf("add-credential: %v\n%s", err, out)
	}
	// A home whose known_hosts holds another key for the router: its login
	// is refused at the SSH handshake, before any command is sent.
	_, other, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := cryptossh.NewSignerFromKey(other)
	if err != nil {
		t.Fatal(err)
	}
	wrongKey := readsHome(t, srv, signer.PublicKey())

	run := func(home, rb string, wantCode int) string {
		t.Helper()
		rep, raw, code := runPleiadesJSONWithHome(t, dir, home, "run", rb)
		if code != wantCode || rep.RunID == "" {
			t.Fatalf("run %s: exit %d, want %d\n%s", rb, code, wantCode, raw)
		}
		return rep.RunID
	}
	// The run to undo: counters cleared, which its runbook says needs no undo.
	target := run(home, readsRunbook(t, dir, "clear", "  - name: clear\n    net.ios.command:\n      command: clear counters\n      prompts:\n        - match: 'confirm\\]'\n          answer: \"y\"\n    rollback: []\n"), 0)
	refusedShow := run(home, readsRunbook(t, dir, "bogus", "  - name: a show IOS refuses\n    net.ios.command:\n      command: show bogus\n"), 1)
	handshake := run(wrongKey, readsRunbook(t, dir, "version", "  - name: a show that never logs in\n    net.ios.command:\n      command: show version\n"), 1)
	failedChange := run(home, readsRunbook(t, dir, "declined", "  - name: a change that failed\n    net.ios.command:\n      command: clear counters\n      prompts:\n        - match: 'confirm\\]'\n          answer: \"n\"\n"), 1)
	if strings.Contains(strings.Join(srv.Commands(), "\n"), "show version") {
		t.Fatal("the show with the wrong host key reached the router")
	}

	// The journal says which failed calls only read, of the parameters they
	// ran with.
	for id, want := range map[string]bool{refusedShow: true, handshake: true, failedChange: false} {
		body, err := os.ReadFile(filepath.Join(dir, ".pleiades", "journal", id+".jsonl")) // #nosec G304 -- this test's own project
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Contains(string(body), `"read_only_call":true`); got != want {
			t.Errorf("run %s's journal records read_only_call %v, want %v:\n%s", id, got, want, body)
		}
	}

	// Only the failed change stands in the way, by name.
	rep, raw, code := runPleiadesJSONWithHome(t, dir, home, "rollback", target)
	problems := rollbackProblems(t, rep.Rollback)
	if code == 0 || len(problems) != 1 || problems[0].Accept != "--despite-run "+failedChange {
		t.Fatalf("rollback: exit %d, problems %+v, want only %s named\n%s", code, problems, failedChange, raw)
	}
	for _, id := range []string{refusedShow, handshake} {
		if strings.Contains(raw, id) {
			t.Errorf("the rollback names run %s, whose only task was a failed read:\n%s", id, raw)
		}
	}
	// Named, it is accepted, and the rollback has nothing to run.
	rep, raw, code = runPleiadesJSONWithHome(t, dir, home, "rollback", target, "--despite-run", failedChange)
	if code != 0 || rep.Outcome.Status != "complete" || len(rollbackProblems(t, rep.Rollback)) != 0 {
		t.Fatalf("rollback --despite-run: exit %d\n%s", code, raw)
	}
}
