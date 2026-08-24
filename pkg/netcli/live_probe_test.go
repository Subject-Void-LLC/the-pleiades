package netcli_test

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
)

// TestLiveIOSPromptShapes is a diagnostic, not a Release Gate: it exists
// to pin the real prompt, echo, and error shapes a Cisco IOS XE device
// actually produces over an interactive PTY session, against
// pkg/remoteexec's raw Shell primitive with no netcli.Dialect involved
// at all. Phase 86.5's own predecessor spec fabricated these shapes
// (FAILURE_PATTERNS.md #202); this test is what replaces a guess with a
// real device's own answer.
//
// It touches no interface and applies no configuration a device reload
// would need to undo: only "terminal length 0", a read-only "show
// version", "configure terminal", one deliberately invalid line (to
// observe the error convention), and "end". This is deliberately more
// conservative than cmd/pleiades's own net_ios_config Release Gate,
// which does create and then remove a loopback interface -- this test's
// only job is to observe shapes, not to prove net.ios.config end to
// end.
//
// Gated on PLEIADES_E2E_IOS; skips rather than fails when unset, or when
// PLEIADES_E2E_IOS_USER/PLEIADES_E2E_IOS_PASS are unset. Credentials
// come from the environment only, never a literal in this file: the
// DevNet Catalyst 8000 Always-On sandbox issues a fresh, unique
// password per reservation, and hardcoding one here would both leak a
// live credential and silently rot the moment that reservation expires.
func TestLiveIOSPromptShapes(t *testing.T) {
	if os.Getenv("PLEIADES_E2E_IOS") == "" {
		t.Skip("set PLEIADES_E2E_IOS=1 to run this live diagnostic against a real Cisco IOS XE device")
	}
	user := os.Getenv("PLEIADES_E2E_IOS_USER")
	pass := os.Getenv("PLEIADES_E2E_IOS_PASS")
	if user == "" || pass == "" {
		t.Skip("PLEIADES_E2E_IOS_USER and PLEIADES_E2E_IOS_PASS must both be set (no default credential: the sandbox issues a unique password per reservation)")
	}
	host := os.Getenv("PLEIADES_E2E_IOS_HOST")
	if host == "" {
		host = "devnetsandboxiosxec8k.cisco.com"
	}

	runner := remoteexec.New(remoteexec.Options{InsecureSkipHostKeyVerify: true})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
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

	// First pass (run earlier, evidence kept in FAILURE_PATTERNS.md
	// candidate notes) used a "$"-anchored multiline pattern and got an
	// off-by-one: Go's regexp $ in (?m) mode is satisfied both
	// immediately before a "\n" AND at the end of the currently
	// buffered text, so a partial read ending right after "#\r" (with
	// the device's own trailing "\n" not yet arrived) matched early,
	// left that "\n" in Shell's carry buffer, and silently prefixed the
	// NEXT command's read. This second pass uses exact, literal prompt
	// text (no anchor, no ambiguity about "end of buffer" vs "end of
	// line") specifically to get unambiguous ground truth before this
	// probe's own analysis is trusted.
	// Second finding this probe surfaced (kept here for the record, not
	// because it's still needed): the SAME "\r\n" line terminator this
	// package used in its first draft made the device double-print its
	// prompt -- once for the real command, once more as an empty Enter
	// the trailing "\n" apparently submitted on its own. shell.go's
	// WriteLine now sends a bare "\r", the same thing a real interactive
	// terminal sends for Enter, and this third pass proves that fixed it
	// at the source: plain, non-repeated-group literal matching is
	// enough once the client stops sending a phantom second Enter.
	execPrompt := regexp.MustCompile(`Cat8kv#`)
	cfgPrompt := regexp.MustCompile(`Cat8kv\(config\)#`)

	step := func(label, line string, prompt *regexp.Regexp) string {
		t.Helper()
		if line != "" {
			if err := shell.WriteLine(ctx, line); err != nil {
				t.Fatalf("%s: WriteLine(%q): %v", label, line, err)
			}
		}
		out, err := shell.ReadUntil(ctx, prompt, 64<<10)
		if err != nil {
			t.Fatalf("%s: ReadUntil: %v", label, err)
		}
		fmt.Printf("=== %s (%d bytes) ===\n%q\n\n", label, len(out), out)
		return out
	}

	step("first prompt (no command sent)", "", execPrompt)
	step("terminal length 0", "terminal length 0", execPrompt)
	step("show version", "show version", execPrompt)
	step("configure terminal", "configure terminal", cfgPrompt)
	step("deliberately invalid config-mode line", "this-is-not-a-real-ios-command-12345", cfgPrompt)
	step("end", "end", execPrompt)
}

// TestLiveIOSConfigSubmodePrompt is a second diagnostic pass, run after
// TestLiveIOSPromptShapes established that a bare "\r" (not "\r\n")
// keeps the device's prompt from double-printing. Its own job is
// narrower: pin the prompt shape for a configuration SUB-mode (entered
// by "interface ..."), because net.ios.config's own Release Gate
// creates a loopback interface and sets its description, which means
// Session.Config must recognize "(config-if)#", not just "(config)#".
// It creates one interface, reads its running-config back, then removes
// the interface again in the same run: no interface here carries
// traffic, so nothing it does can affect the shared sandbox's own
// connectivity, and nothing survives this test.
func TestLiveIOSConfigSubmodePrompt(t *testing.T) {
	if os.Getenv("PLEIADES_E2E_IOS") == "" {
		t.Skip("set PLEIADES_E2E_IOS=1 to run this live diagnostic against a real Cisco IOS XE device")
	}
	user := os.Getenv("PLEIADES_E2E_IOS_USER")
	pass := os.Getenv("PLEIADES_E2E_IOS_PASS")
	if user == "" || pass == "" {
		t.Skip("PLEIADES_E2E_IOS_USER and PLEIADES_E2E_IOS_PASS must both be set")
	}
	host := os.Getenv("PLEIADES_E2E_IOS_HOST")
	if host == "" {
		host = "devnetsandboxiosxec8k.cisco.com"
	}

	runner := remoteexec.New(remoteexec.Options{InsecureSkipHostKeyVerify: true})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
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

	execPrompt := regexp.MustCompile(`Cat8kv#`)

	// A distinctive, high, unlikely-to-collide interface number: this
	// sandbox is a shared resource, and a real reservation's own
	// legitimate loopbacks are far more likely to sit in a low, ordinary
	// range.
	const ifName = "Loopback8986"

	step := func(label, line string, prompt *regexp.Regexp) string {
		t.Helper()
		if err := shell.WriteLine(ctx, line); err != nil {
			t.Fatalf("%s: WriteLine(%q): %v", label, line, err)
		}
		out, err := shell.ReadUntil(ctx, prompt, 32<<10)
		if err != nil {
			t.Fatalf("%s: ReadUntil: %v", label, err)
		}
		fmt.Printf("=== %s (%d bytes) ===\n%q\n\n", label, len(out), out)
		return out
	}

	if _, err := shell.ReadUntil(ctx, execPrompt, 4096); err != nil {
		t.Fatalf("reading first prompt: %v", err)
	}

	step("configure terminal", "configure terminal", regexp.MustCompile(`Cat8kv\(config\)#`))
	step("interface "+ifName, "interface "+ifName, regexp.MustCompile(`Cat8kv\(config-if\)#`))
	step("description", "description pleiades-live-probe-diagnostic", regexp.MustCompile(`Cat8kv\(config-if\)#`))
	step("exit (back to config)", "exit", regexp.MustCompile(`Cat8kv\(config\)#`))
	step("no interface (cleanup)", "no interface "+ifName, regexp.MustCompile(`Cat8kv\(config\)#`))
	step("end", "end", execPrompt)

	confirm := step("show run interface "+ifName+" (confirm removed)", "show running-config interface "+ifName, execPrompt)
	if strings.Contains(confirm, ifName) && !strings.Contains(confirm, "% Invalid input") {
		t.Errorf("cleanup may have failed: %s still appears in running-config: %q", ifName, confirm)
	}
}
