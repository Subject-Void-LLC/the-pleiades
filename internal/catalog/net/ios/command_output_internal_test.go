// Package ios: what net.ios.command makes of a command's output over a real
// SSH session against a scripted device: a read-only command's "% " lines
// are its data and it reports unchanged, ping and traceroute among them; the
// secrets in its output are masked; accept takes a further "% " line; and a
// timeout says what the device last printed, masked (FAILURE_PATTERNS 646,
// 650, 651).
package ios

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	inventorytest "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
)

// The scripted device's lines.
const (
	pemLine     = "show crypto pki certificates pem"
	keyringLine = "show running-config | section keyring"
	pingLine    = "ping 10.0.0.1"
	clearLine   = "clear crypto session remote 10.0.0.9"
	bannerLine  = "banner motd %"
	badPingLine = "ping no-such-host"
	outputPSK   = "OutputPsk-4242"
	secondPSK   = "SecondPsk-8686"
	chainKey    = "ChainKey-5151"
)

// outputDevice starts a scripted IOS device for these tests.
func outputDevice(t *testing.T) (*remoteexectest.Server, *sshDevice) {
	t.Helper()
	srv, err := remoteexectest.Start(remoteexectest.Options{Device: &remoteexectest.Device{
		Prompt: "r1#",
		Replies: map[string]string{
			pemLine:     "% Self-signed CA certificate:\r\n-----BEGIN CERTIFICATE-----\r\nMIIB\r\n-----END CERTIFICATE-----",
			keyringLine: "crypto ikev2 keyring GATE\r\n peer R2\r\n  pre-shared-key " + outputPSK,
			pingLine:    "Sending 5, 100-byte ICMP Echos to 10.0.0.1, timeout is 2 seconds:\r\n!!!!!\r\nSuccess rate is 100 percent (5/5)",
			clearLine:   "% No active sessions for this peer",
			badPingLine: "Translating \"no-such-host\"\r\n% Unrecognized host or address, or protocol not running.",
		},
		Asks: map[string][]remoteexectest.Question{
			// A tail holding several secrets, one a key chain's, as a keyring's
			// or key chain's show does (the review's F1).
			bannerLine: {{Text: " peer A\r\n  pre-shared-key " + outputPSK + "\r\n peer B\r\n  pre-shared-key " + secondPSK + "\r\nkey chain K\r\n key 1\r\n  key-string 7 " + chainKey + "\r\nEnter TEXT message.  End with the character '%'.\r\n", Want: "never"}},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	return srv, &sshDevice{
		Stub: &inventorytest.Stub{StubName: "r1", Caps: []capability.Name{capability.NameSSHTransport}},
		host: srv.Host, port: srv.Port,
	}
}

// TestCommand_AReadOnlyCommandsPercentLinesAreItsData: a "% " line in what a
// show prints is output, the run is unchanged, and the secret in the output is
// masked; ping reports unchanged as show does.
func TestCommand_AReadOnlyCommandsPercentLinesAreItsData(t *testing.T) {
	srv, device := outputDevice(t)
	for _, line := range []string{pemLine, keyringLine, pingLine} {
		rc := &secretContext{stubContext: newStubContext(), secrets: srv.Secrets()}
		result, err := Command(context.Background(), rc, device, sessionParams(map[string]any{"command": line}))
		if err != nil || result.Changed {
			t.Fatalf("%s: result %+v, err %v", line, result, err)
		}
		out, _ := rc.stats[statCommandStdout].(string)
		if strings.Contains(out, outputPSK) {
			t.Errorf("%s: stdout holds the pre-shared key: %q", line, out)
		}
		if line == keyringLine && !strings.Contains(out, "pre-shared-key pleiades-masked-") {
			t.Errorf("%s: stdout %q, want the key masked in place", line, out)
		}
	}
}

// TestCommand_APingToAnUnknownHostFails: a ping IOS could not even send,
// to a name it cannot resolve, fails as it did before ping became a
// read-only command (the review's F4).
func TestCommand_APingToAnUnknownHostFails(t *testing.T) {
	srv, device := outputDevice(t)
	rc := &secretContext{stubContext: newStubContext(), secrets: srv.Secrets()}
	if _, err := Command(context.Background(), rc, device, sessionParams(map[string]any{"command": badPingLine})); err == nil || !strings.Contains(err.Error(), "Unrecognized host") {
		t.Fatalf("err %v, want the unknown host refused", err)
	}
}

// TestCommand_AcceptTakesAFurtherPercentLine: a command that changes things
// still fails on a "% " line, unless accept names it.
func TestCommand_AcceptTakesAFurtherPercentLine(t *testing.T) {
	srv, device := outputDevice(t)
	rc := &secretContext{stubContext: newStubContext(), secrets: srv.Secrets()}
	if _, err := Command(context.Background(), rc, device, sessionParams(map[string]any{"command": clearLine})); err == nil || !strings.Contains(err.Error(), "No active sessions") {
		t.Fatalf("without accept: err %v, want the line refused", err)
	}
	rc = &secretContext{stubContext: newStubContext(), secrets: srv.Secrets()}
	result, err := Command(context.Background(), rc, device, sessionParams(map[string]any{"command": clearLine, "accept": []any{"% No active sessions"}}))
	if err != nil || !result.Changed {
		t.Fatalf("with accept: result %+v, err %v", result, err)
	}
	if _, err := Command(context.Background(), rc, device, sessionParams(map[string]any{"command": clearLine, "accept": []any{"No active"}})); err == nil || !strings.Contains(err.Error(), "does not begin with %") {
		t.Fatalf("an accept pattern without %%: err %v", err)
	}
}

// TestCommand_ATimeoutSaysWhatTheDeviceLastPrinted: a command left waiting
// on something no question shape names fails at its timeout with the
// device's last lines, masked.
func TestCommand_ATimeoutSaysWhatTheDeviceLastPrinted(t *testing.T) {
	srv, device := outputDevice(t)
	rc := &secretContext{stubContext: newStubContext(), secrets: srv.Secrets()}
	// Long enough that a loaded machine's dial and login fit inside it, so
	// the failure is the banner's wait and not the connection's.
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	_, err := Command(ctx, rc, device, sessionParams(map[string]any{"command": bannerLine}))
	if err == nil || !strings.Contains(err.Error(), "the device last printed") || !strings.Contains(err.Error(), "End with the character") {
		t.Fatalf("err %v, want the device's last lines", err)
	}
	for _, secret := range []string{outputPSK, secondPSK, chainKey} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("the error holds a secret: %v", err)
		}
	}
}

// TestLastLines_Bounds: the tail is at most maxLastLines lines and
// maxLastBytes, one line of text.
func TestLastLines_Bounds(t *testing.T) {
	var many []string
	for i := 0; i < 50; i++ {
		many = append(many, strings.Repeat("x", 60))
	}
	got := lastLines(strings.Join(many, "\r\n"))
	if strings.Count(got, " | ") > maxLastLines-1 || len(got) > maxLastBytes+3 || strings.ContainsAny(got, "\r\n") {
		t.Fatalf("lastLines = %d bytes, %d separators", len(got), strings.Count(got, " | "))
	}
}
