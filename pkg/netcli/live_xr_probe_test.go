package netcli_test

import (
	"context"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
)

// This file is the CLI half of Phase 74a's IOS XR probe, and it exists
// because two things about netcli.IOS are already known to be wrong for
// XR, verified by reading this package's own source rather than by
// assumption:
//
//  1. iosExecPrompt is `(?m)^[\w.\-]+#\s*$`. Go's \w is [0-9A-Za-z_],
//     so with the added "." and "-" the class still excludes "/" and
//     ":". An XR prompt is shaped "RP/0/RP0/CPU0:hostname#", which
//     contains both. netcli.IOS therefore cannot match an XR prompt at
//     all, and every net.ios.* method would read until its bound and
//     fail as a timeout, which reads like a network problem rather than
//     a dialect mismatch.
//  2. Session.Config sends EnterConfigMode, the lines, then
//     ExitConfigMode, and IOS.ExitConfigMode is "end". XR's
//     configuration model is two-phase and requires an explicit "commit"
//     BEFORE leaving; "end" with uncommitted changes prompts, and the
//     wrong answer throws the change away. Dialect has no field for a
//     commit step, so an IOSXR dialect built by copying IOS and changing
//     the prompt would report success and change nothing, which is the
//     most dangerous shape a bug can take.
//
// So this probe's job is to pin what XR actually does, before either is
// fixed. It runs against the raw remoteexec.Shell primitive with no
// netcli.Dialect involved, because no Dialect that works here exists
// yet.
//
// READ-ONLY apart from entering and leaving configuration mode without
// applying a line. It never sends a configuration statement and never
// commits, so there is nothing for it to undo on a shared device.
//
// Gated on PLEIADES_E2E_XR; skips rather than fails when unset.
// Credentials come from the environment only, never a literal here: the
// sandbox issues a fresh password per reservation.
func requireIOSXR(t *testing.T) (host, user, pass string) {
	t.Helper()
	if os.Getenv("PLEIADES_E2E_XR") == "" {
		t.Skip("set PLEIADES_E2E_XR=1 to run this live diagnostic against the real IOS XR Always-On sandbox")
	}
	user = os.Getenv("PLEIADES_E2E_XR_USER")
	pass = os.Getenv("PLEIADES_E2E_XR_PASS")
	if user == "" || pass == "" {
		t.Skip("PLEIADES_E2E_XR_USER and PLEIADES_E2E_XR_PASS must both be set (no default credential: this sandbox issues a unique password per reservation)")
	}
	host = os.Getenv("PLEIADES_E2E_XR_HOST")
	if host == "" {
		host = "sandbox-iosxr-1.cisco.com"
	}
	return host, user, pass
}

func TestLiveIOSXRPromptAndConfigShapes(t *testing.T) {
	host, user, pass := requireIOSXR(t)

	runner := remoteexec.New(remoteexec.Options{InsecureSkipHostKeyVerify: true})
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	conn, err := runner.Connect(ctx, nil, remoteexec.Target{Host: host, Port: 22}, remoteexec.PasswordAuth(user, pass))
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer conn.Close()

	shell, err := conn.Shell(ctx, remoteexec.ShellOptions{})
	if err != nil {
		t.Fatalf("Shell: %v", err)
	}
	defer shell.Close()

	// Deliberately permissive while probing: this reads to ANY line
	// ending in "#" or ">", because the whole question is what the
	// prompt looks like and a stricter pattern would beg it. The real
	// Dialect gets an anchored pattern derived from what this prints.
	anyPrompt := regexp.MustCompile(`(?m)^.*[#>]\s*$`)
	read := func(label string) string {
		t.Helper()
		out, err := shell.ReadUntil(ctx, anyPrompt, 1<<20)
		if err != nil {
			t.Fatalf("%s: ReadUntil: %v", label, err)
		}
		t.Logf("=== %s ===\n%s", label, out)
		return out
	}
	send := func(line string) {
		t.Helper()
		if err := shell.WriteLine(ctx, line); err != nil {
			t.Fatalf("WriteLine(%q): %v", line, err)
		}
	}

	banner := read("banner and first prompt")

	// The exec-mode prompt is the last line of what arrived. Reported on
	// its own because it is the single literal netcli.IOS gets wrong.
	lines := strings.Split(strings.TrimRight(banner, "\r\n"), "\n")
	execPrompt := strings.TrimSpace(lines[len(lines)-1])
	t.Logf("EXEC PROMPT (verbatim): %q", execPrompt)
	t.Logf("matched by netcli.IOS's own exec pattern `^[\\w.\\-]+#$`? %v",
		regexp.MustCompile(`(?m)^[\w.\-]+#\s*$`).MatchString(execPrompt))

	send("terminal length 0")
	read("terminal length 0")

	send("show version | include Version")
	read("show version")

	send("configure terminal")
	config := read("configure terminal")
	cfgLines := strings.Split(strings.TrimRight(config, "\r\n"), "\n")
	configPrompt := strings.TrimSpace(cfgLines[len(cfgLines)-1])
	t.Logf("CONFIG PROMPT (verbatim): %q", configPrompt)
	t.Logf("matched by netcli.IOS's own config pattern `^[\\w.\\-]+\\([^)]*\\)#$`? %v",
		regexp.MustCompile(`(?m)^[\w.\-]+\([^)]*\)#\s*$`).MatchString(configPrompt))

	// A deliberately invalid line, to observe the error convention. IOS
	// answers "% Invalid input detected"; whether XR uses the same "%"
	// leading character is exactly the sort of thing worth checking
	// rather than assuming, since netcli.IOS's ErrorPattern is `(?m)^% `.
	send("pleiades-not-a-real-command")
	bad := read("invalid configuration line")
	t.Logf("matched by netcli.IOS's own error pattern `^%% `? %v",
		regexp.MustCompile(`(?m)^% `).MatchString(bad))

	// Leave WITHOUT committing. Nothing was configured, so there should
	// be nothing to commit, and what this captures is how XR words its
	// exit when it believes there might be: that wording is what a
	// Dialect with a commit step has to be built against.
	send("abort")
	read("abort (leave configuration mode discarding anything staged)")

	t.Log("probe complete; no configuration line was applied and nothing was committed")
}

// TestLiveIOSXRShowRunningInterfaces captures the read-back command an
// XR Release Gate needs, which is not the same command IOS XE's gate
// uses. IOS XE answers "show running-config interface LoopbackN"; what
// XR answers, and how it words a refusal for an interface that does not
// exist, is what this records. The gate asserts on that refusal to prove
// a revert landed, so its exact wording is load-bearing rather than
// cosmetic.
func TestLiveIOSXRShowRunningInterfaces(t *testing.T) {
	host, user, pass := requireIOSXR(t)

	runner := remoteexec.New(remoteexec.Options{InsecureSkipHostKeyVerify: true})
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	conn, err := runner.Connect(ctx, nil, remoteexec.Target{Host: host, Port: 22}, remoteexec.PasswordAuth(user, pass))
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer conn.Close()

	shell, err := conn.Shell(ctx, remoteexec.ShellOptions{})
	if err != nil {
		t.Fatalf("Shell: %v", err)
	}
	defer shell.Close()

	anyPrompt := regexp.MustCompile(`(?m)^.*[#>]\s*$`)
	if _, err := shell.ReadUntil(ctx, anyPrompt, 1<<20); err != nil {
		t.Fatalf("reading the first prompt: %v", err)
	}
	for _, line := range []string{"terminal length 0"} {
		if err := shell.WriteLine(ctx, line); err != nil {
			t.Fatalf("WriteLine(%q): %v", line, err)
		}
		if _, err := shell.ReadUntil(ctx, anyPrompt, 1<<20); err != nil {
			t.Fatalf("reading after %q: %v", line, err)
		}
	}

	// An interface number no gate would ever use, so this is guaranteed
	// to be the "does not exist" case on a shared device.
	for _, cmd := range []string{
		"show running-config interface Loopback39999",
		"show interfaces brief | include Loopback",
	} {
		if err := shell.WriteLine(ctx, cmd); err != nil {
			t.Fatalf("WriteLine(%q): %v", cmd, err)
		}
		out, err := shell.ReadUntil(ctx, anyPrompt, 1<<20)
		if err != nil {
			t.Fatalf("reading after %q: %v", cmd, err)
		}
		t.Logf("=== %s ===\n%s", cmd, out)
	}
}
