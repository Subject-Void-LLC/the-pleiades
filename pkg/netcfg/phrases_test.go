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
// show logging and show ip http server status, the lab's addresses, MAC
// addresses and user replaced by documentation ones), to coming back from
// the masker exactly as they went in: none of it holds a secret. Each line
// had text replaced by a placeholder before.
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
	// The measured log lines keep their text with a sequence number, a time
	// zone, a year or an uptime in the timestamp, or none.
	for _, kept := range []string{
		"000123: *Nov 17 02:13:31.437 UTC: %SSH-5-SSH2_USERAUTH: User 'operator' authentication for SSH2 Session from 192.0.2.11 (tty = 0) Succeeded",
		"Nov 17 2026 02:02:14: %CRYPTO_ENGINE-5-KEY_ADDITION: A key named TP-self-signed-1 has been generated or imported by crypto-engine",
		"1d02h: %CRYPTO_ENGINE-5-KEY_DELETED: A key named TP-self-signed-1 has been removed from key storage",
		"%CRYPTO_ENGINE-5-KEY_REPLACE: A key named k has been replaced by crypto-engine with new key data",
	} {
		if got := netcfg.Mask(kept); got != kept {
			t.Errorf("masked:\n%q\n%q", kept, got)
		}
	}
}

// TestMask_PhrasesAreWholeShapes holds each phrase to its own context. Every
// case carries a phrase's exact words, so only the context guard can mask
// it: a configuration line whose host is named like a phrase's word before
// ("tacacs-server host A key named Xyz123" held its key in the clear when
// the word before was the whole guard), a log tag after a command rather
// than a timestamp, a tag's words out of place, and a phrase's words in a
// server block.
func TestMask_PhrasesAreWholeShapes(t *testing.T) {
	for _, tc := range []struct {
		config, secret string
	}{
		{"tacacs-server host A key named Xyz123", "Xyz123"},
		{"radius-server host A key named Rad123", "Rad123"},
		{"radius-server host A auth-port 1812 acct-port 1813 key A key named Rad456", "Rad456"},
		// NX-OS writes its servers the same way.
		{"tacacs-server host A key named NxTac1", "NxTac1"},
		{"tacacs-server host Age key Key Number State", "Key Number State"},
		{"tacacs-server host Hellman key size : 2048", "size : 2048"},
		{"tacacs-server host new key data", "data"},
		{"tacacs-server host server authentication method:", "method:"},
		{"username 'x' authentication for SSH2 Session from", "for"},
		{"interface Gi1\n standby 1 'x' authentication for SSH2 Session from", "for"},
		// A log tag that does not open the message after a timestamp.
		{"logging host %CRYPTO_ENGINE-5-KEY_ADDITION: A key named Lg123", "Lg123"},
		{"tacacs-server host %CRYPTO_ENGINE-5-KEY_REPLACE: new key data", "data"},
		{"*Nov 17 02:13:31.437: %SSH-5-SSH2_USERAUTH: x 'operator' authentication for SSH2 Session from", "for"},
		{"*Nov 17 02:13:31.437: %CRYPTO_ENGINE-5-KEY_ADDITION: B key named Lg456", "Lg456"},
		// A server block's key, which could be any word.
		{"radius server r1\n key named", "named"},
		{"tacacs server t1\n key Key Number State", "Key Number State"},
		{"interface Gi1\n standby 1 authentication for", "for"},
		// Not the whole phrase.
		{"*Nov 17 02:13:31.437: %SSH-5-SSH2_USERAUTH: User 'operator' authentication for SSH2", "for"},
		// A log line quoting a configuration command keeps its secret masked.
		{"*Nov 17 02:13:31.437: %PARSER-5-CFGLOG_LOGGEDCMD: User:operator  logged command:standby 1 authentication hsrpTxt", "hsrpTxt"},
		{"*Nov 17 02:13:31.437: %PARSER-5-CFGLOG_LOGGEDCMD: User:operator  logged command:username x secret 0 plainPw", "plainPw"},
	} {
		masked := netcfg.Mask(tc.config)
		if strings.Contains(masked, tc.secret) || !strings.Contains(masked, netcfg.MaskPrefix) {
			t.Errorf("%q kept its secret:\n%s", tc.config, masked)
		}
	}
}
