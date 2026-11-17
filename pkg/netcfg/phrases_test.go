// Tests of the show output and log phrases the masker reads as holding no
// secret (FAILURE_PATTERNS 1268).
package netcfg_test

import (
	"os"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcfg"
)

// TestMask_KeepsMeasuredShowOutput holds real IOS show output and log lines,
// measured in the CML lab on 2026-11-17 (show lacp neighbor, show ip ssh,
// show logging and show ip http server status, the lab's addresses replaced
// by documentation ones), to coming back from the masker exactly as they
// went in: none of it holds a secret. Each line had text replaced by a
// placeholder before.
func TestMask_KeepsMeasuredShowOutput(t *testing.T) {
	raw, err := os.ReadFile("testdata/ios_show_output.txt")
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, line := range strings.Split(text, "\n") {
		if got := netcfg.Mask(line); got != line {
			t.Errorf("masked:\n%q\n%q", line, got)
		}
	}
	// Command output goes through MaskOutput, which reads CLI text with the
	// same masker and adds the crypt and structured shapes.
	masked, found, err := netcfg.MaskOutput(text)
	if err != nil || masked != text || len(found) != 0 {
		t.Errorf("MaskOutput changed the output (found %d, err %v):\n%s", len(found), err, masked)
	}
}

// TestMask_PhrasesAreWholeShapes holds each phrase to its whole shape: a line
// that shares only the secret word and the next word with one, or lacks the
// word before it, is still masked, and a configuration line a log message
// quotes keeps its secret masked.
func TestMask_PhrasesAreWholeShapes(t *testing.T) {
	for _, tc := range []struct {
		config, secret string
	}{
		// A server block's key, which could be any word.
		{"radius server r1\n key named", "named"},
		{"tacacs server t1\n key Key Number State", "Key Number State"},
		// No "Diffie Hellman" before it.
		{"tacacs-server key size : 2048", "size : 2048"},
		// An HSRP authentication string with no user name before it, or one
		// that is not quoted.
		{"interface Gi1\n standby 1 authentication for", "for"},
		{"x authentication for SSH2 Session from", "for"},
		{"User netops authentication for SSH2 Session from", "for"},
		// Not the whole phrase.
		{"User 'netops' authentication for SSH2", "for"},
		{"standby 1 authentication method:", "method:"},
		// A log line quoting a configuration command keeps its secret masked.
		{"*Nov 17 02:13:31.437: %PARSER-5-CFGLOG_LOGGEDCMD: User:netops  logged command:standby 1 authentication hsrpTxt", "hsrpTxt"},
		{"*Nov 17 02:13:31.437: %PARSER-5-CFGLOG_LOGGEDCMD: User:netops  logged command:username x secret 0 plainPw", "plainPw"},
	} {
		masked := netcfg.Mask(tc.config)
		if strings.Contains(masked, tc.secret) || !strings.Contains(masked, netcfg.MaskPrefix) {
			t.Errorf("%q kept its secret:\n%s", tc.config, masked)
		}
	}
}
