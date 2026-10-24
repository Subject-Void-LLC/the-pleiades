// Tests that what a ledger stores carries no machine's details: no
// user@host, no IP address, no home directory, no secret, and no more than
// maxText bytes, while ordinary text survives.
package main

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestScrub_RemovesWhatNamesAMachine: each kind of detail is replaced by its
// placeholder, and what looks like one but is not survives.
func TestScrub_RemovesWhatNamesAMachine(t *testing.T) {
	cases := map[string]struct{ in, want string }{
		"user at an address": {"macOS on someone@192.0.2.10: stopped at reachable: someone@192.0.2.10 (192.0.2.10:22) did not answer",
			"macOS on <user@host>: stopped at reachable: <user@host> (<ip>:22) did not answer"},
		"user at a name":      {"ssh dev@build-box.example.com refused", "ssh <user@host> refused"},
		"loopback with port":  {"read from shell on 127.0.0.1:35569: EOF", "read from shell on <ip>:35569: EOF"},
		"an IPv6 address":     {"dial tcp [2001:db8::1]:22: i/o timeout", "dial tcp [<ip>]:22: i/o timeout"},
		"IPv6 any":            {"msg=Started address=[::]:8080", "msg=Started address=[<ip>]:8080"},
		"a Linux home":        {"open /home/someone/auto-roboto/x.go: denied", "open ~/auto-roboto/x.go: denied"},
		"a Mac home":          {"cd /Users/someone/src failed", "cd ~/src failed"},
		"a Windows home":      {`C:\Users\someone\AppData\Local\Temp gone`, `~\AppData\Local\Temp gone`},
		"a WSL view of it":    {"/mnt/c/Users/someone/iso missing", "~/iso missing"},
		"a module version":    {"golang.org/x/tools@v0.30.0 needs go1.24", "golang.org/x/tools@v0.30.0 needs go1.24"},
		"a time of day":       {"at 07:01:32.97 it stopped", "at 07:01:32.97 it stopped"},
		"a version string":    {"docker daemon version 28.0.4", "docker daemon version 28.0.4"},
		"colour and a tab":    {"\x1b[36;1mexit 1\x1b[0m\there", "exit 1 here"},
		"only the first line": {"first\nsecond", "first"},
	}
	for name, c := range cases {
		if got := scrub(c.in); got != c.want {
			t.Errorf("%s: scrub(%q) = %q, want %q", name, c.in, got, c.want)
		}
	}
}

// TestScrub_MasksSecrets: the shared ruleset's shapes and a secret-named
// environment variable's value never survive.
func TestScrub_MasksSecrets(t *testing.T) {
	t.Setenv("FAILURES_TEST_TOKEN", "s3cr3t-value-0123456789")
	learnSecrets()
	for _, in := range []string{
		"clone https://user:hunter2hunter2@git.example.com/x.git failed",
		"the token s3cr3t-value-0123456789 was refused",
	} {
		got := scrub(in)
		for _, secret := range []string{"hunter2hunter2", "s3cr3t-value-0123456789"} {
			if strings.Contains(got, secret) {
				t.Errorf("scrub(%q) = %q, which still holds %q", in, got, secret)
			}
		}
	}
}

// TestScrub_CapsAtACharacterBoundary: text over maxText is cut to it, never
// through a character.
func TestScrub_CapsAtACharacterBoundary(t *testing.T) {
	got := scrub(strings.Repeat("a", maxText-1) + "\u00e9\u00e9")
	if len(got) > maxText || !utf8.ValidString(got) {
		t.Errorf("scrub kept %d bytes (valid UTF-8: %v), want at most %d", len(got), utf8.ValidString(got), maxText)
	}
	if got := truncate("short", maxText); got != "short" {
		t.Errorf("truncate changed text under the cap: %q", got)
	}
}
