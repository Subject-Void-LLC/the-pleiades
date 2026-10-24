// Package netcli_test: Session.ConfigWith against a scripted IOS device
// whose configuration lines ask questions and print notices the way IOL XE
// 17.15's did in the blind CVD lab, over a real SSH session.
package netcli_test

import (
	"context"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcli"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"golang.org/x/crypto/ssh"
)

// configIOS is a fake IOS device in configuration mode: crypto pki server's
// "no shutdown" asks to confirm, a line already in the configuration
// prints IOS's notice for it, a numbered access list entry already there is
// rejected, and "write" leaves "[OK]" open for a moment before its line
// break, as "Building configuration..." does. Every line it receives is
// kept, answers included.
type configIOS struct {
	mu    sync.Mutex
	lines []string
	mode  string
	// started is set when the CA was started, with "yes".
	started bool
	// hasKey is whether the key pair PLTEST exists.
	hasKey bool
}

func (d *configIOS) keyHeld() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.hasKey
}

func (d *configIOS) setKey(held bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.hasKey = held
}

func (d *configIOS) received() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.lines...)
}

func (d *configIOS) wasStarted() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.started
}

func (d *configIOS) prompt() string {
	if d.mode == "" {
		return "r1#"
	}
	return "r1(" + d.mode + ")#"
}

func (d *configIOS) serve(channel ssh.Channel) {
	reader := &crReader{r: channel}
	write := func(text string) { _, _ = channel.Write([]byte(text)) }
	read := func() (string, bool) {
		line, ok := reader.readLine()
		if ok {
			d.mu.Lock()
			d.lines = append(d.lines, line)
			d.mu.Unlock()
		}
		return line, ok
	}
	write(d.prompt())
	for {
		line, ok := read()
		if !ok {
			return
		}
		if line == "description why?" {
			// The echo of a line ending in "?", its line break late.
			write(line)
			time.Sleep(50 * time.Millisecond)
			write("\r\n" + d.prompt())
			continue
		}
		write(line + "\r\n")
		switch {
		case line == "configure terminal":
			d.mode = "config"
			write("Enter configuration commands, one per line.  End with CNTL/Z.\r\n")
		case line == "end":
			d.mode = ""
		case line == "crypto pki server CA":
			d.mode = "cs-server"
		case line == "no shutdown" && d.mode == "cs-server":
			write("%Some server settings cannot be changed after CA certificate generation.\r\n")
			write("Are you sure you want to do this? [yes/no]: ")
			answer, ok := read()
			if !ok {
				return
			}
			write(answer + "\r\n")
			d.mu.Lock()
			d.started = answer == "yes"
			d.mu.Unlock()
		case line == "permit ip any any":
			write("% Duplicate permit statement ignored\r\n")
		case line == "proposal P":
			write("% Policy already has same proposal set\r\n")
		case line == "match identity remote any":
			write("% Already found same 'match identity' statement in this profile\r\n")
		case line == "crypto key generate rsa label K modulus 2048":
			write("% Key pair was generated at: 17:00:00 UTC Oct 24 2026\r\n")
		case line == "10 permit ip any any":
			write("% Duplicate sequence number\r\n% Failed to add ace to access-list\r\n")
		// The key pair lines below are IOL XE 17.15's own, measured through
		// net.ios.config on the CVD lab's BR3 (2026-10-24).
		case line == "crypto key generate rsa general-keys label PLTEST modulus 2048":
			if d.keyHeld() {
				write("% You already have RSA keys defined named PLTEST.\r\n% They will be replaced.\r\n\r\n")
			} else {
				write("The name for the keys will be: PLTEST\r\n\r\n")
			}
			write("% The key modulus size is 2048 bits\r\n% Generating 2048 bit RSA keys, keys will be non-exportable...\r\n[OK] (elapsed time was 0 seconds)\r\n")
			d.setKey(true)
		case line == "crypto key zeroize rsa PLTEST" && !d.keyHeld():
			write("% The specified keypair does not exist (PLTEST).\r\n")
		case line == "crypto key zeroize rsa PLTEST":
			write("% Keys to be removed are named 'PLTEST'.\r\n% All router certs issued using these keys will also be removed.\r\nDo you really want to remove these keys? [yes/no]: ")
			answer, ok := read()
			if !ok {
				return
			}
			write(answer + "\r\n")
			d.setKey(answer != "yes")
		case line == "start ca":
			write("Password: ")
			if _, ok := read(); !ok {
				return
			}
			write("\r\n")
		case line == "frob":
			write("% Frob is deprecated and was ignored\r\n")
		case line == "write":
			write("Building configuration...\r\n[OK]")
			time.Sleep(50 * time.Millisecond)
			write("\r\n")
		}
		write(d.prompt())
	}
}

// newConfigSession opens a real IOS-dialect session over SSH against d.
func newConfigSession(t *testing.T, d *configIOS) *netcli.Session {
	t.Helper()
	conn := dialFakeIOSServer(t, newFakeIOSServer(t, d.serve))
	shell, err := conn.Shell(context.Background(), remoteexec.ShellOptions{})
	if err != nil {
		t.Fatalf("Shell: %v", err)
	}
	t.Cleanup(func() { _ = shell.Close() })
	session, err := netcli.Open(context.Background(), shell, netcli.IOS, netcli.Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return session
}

// confirmCA answers crypto pki server's confirmation.
func confirmCA() netcli.Prompt {
	return netcli.Prompt{Match: regexp.MustCompile(`Are you sure you want to do this\? \[yes/no\]:`), Answer: "yes"}
}

// TestConfigWith_AnswersAListedQuestion: a configuration line's question
// is answered as a listed prompt says, and the batch goes on to its end.
func TestConfigWith_AnswersAListedQuestion(t *testing.T) {
	d := &configIOS{}
	session := newConfigSession(t, d)
	res, err := session.ConfigWith(context.Background(), []string{"crypto pki server CA", "no shutdown"}, netcli.ConfigOptions{Dialog: netcli.DialogOptions{Prompts: []netcli.Prompt{confirmCA()}}})
	if err != nil {
		t.Fatal(err)
	}
	if !d.wasStarted() || len(res.Exchanges) != 1 || res.Exchanges[0].Question != "Are you sure you want to do this? [yes/no]:" || res.Exchanges[0].Answer != "yes" {
		t.Fatalf("started %v, exchanges %+v", d.wasStarted(), res.Exchanges)
	}
	if got := strings.Join(d.received(), "|"); !strings.HasSuffix(got, "configure terminal|crypto pki server CA|no shutdown|yes|end") {
		t.Errorf("device received %q", got)
	}
}

// TestConfigWith_AnUnansweredQuestionFailsAtOnce: with nothing to answer
// it, a question fails the line at once, naming it and how to answer it.
// The context has no deadline and the read limit is far off, so a session
// that waited on the question would never return: this one returns, and
// nothing was typed at the question.
func TestConfigWith_AnUnansweredQuestionFailsAtOnce(t *testing.T) {
	d := &configIOS{}
	session := newConfigSession(t, d)
	err := session.Config(context.Background(), []string{"crypto pki server CA", "no shutdown", "never sent"})
	if err == nil || !strings.Contains(err.Error(), `"Are you sure you want to do this? [yes/no]:"`) || !strings.Contains(err.Error(), "prompts") {
		t.Fatalf("err %v", err)
	}
	if got := d.received(); got[len(got)-1] != "no shutdown" || d.wasStarted() {
		t.Errorf("device received %q after the question, started %v", got, d.wasStarted())
	}
}

// TestConfigWith_OutputThatIsNoQuestion: "[OK]" left open for a moment, and
// the echo of a line that ends in "?", are not questions.
func TestConfigWith_OutputThatIsNoQuestion(t *testing.T) {
	session := newConfigSession(t, &configIOS{})
	if err := session.Config(context.Background(), []string{"write", "description why?"}); err != nil {
		t.Fatal(err)
	}
}

// TestConfigWith_NoticesAreNotRejections: each of IOS's measured notices,
// and a line the task accepts, lets the batch go on and is returned; any
// other "% " line still rejects.
func TestConfigWith_NoticesAreNotRejections(t *testing.T) {
	session := newConfigSession(t, &configIOS{})
	res, err := session.ConfigWith(context.Background(),
		[]string{"permit ip any any", "proposal P", "match identity remote any", "crypto key generate rsa label K modulus 2048", "frob"},
		netcli.ConfigOptions{Accept: []*regexp.Regexp{regexp.MustCompile(`^% Frob is deprecated`)}})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"% Duplicate permit statement ignored",
		"% Policy already has same proposal set",
		"% Already found same 'match identity' statement in this profile",
		"% Key pair was generated at: 17:00:00 UTC Oct 24 2026",
		"% Frob is deprecated and was ignored",
	}
	if strings.Join(res.Notices, "|") != strings.Join(want, "|") {
		t.Fatalf("notices %q", res.Notices)
	}
	for _, line := range []string{"10 permit ip any any", "frob"} {
		err := newConfigSession(t, &configIOS{}).Config(context.Background(), []string{line})
		if err == nil || !strings.Contains(err.Error(), "device rejected") {
			t.Errorf("%q: %v, want a rejection", line, err)
		}
	}
}

// TestConfigWith_AChooserAnswersAQuestionNobodyListed: with ask, the
// question goes to the chooser, whose pick is typed.
func TestConfigWith_AChooserAnswersAQuestionNobodyListed(t *testing.T) {
	d := &configIOS{}
	session := newConfigSession(t, d)
	c := &chooser{decisions: []netcli.Decision{{ID: "start", Reason: "the task starts the CA"}}}
	res, err := session.ConfigWith(context.Background(), []string{"crypto pki server CA", "no shutdown"}, netcli.ConfigOptions{Dialog: netcli.DialogOptions{
		Ask: &netcli.Ask{Choices: []netcli.Choice{{ID: "start", Answer: "yes", Means: "Start the CA."}, {ID: "keep", Answer: "no", Means: "Leave it stopped."}}, Choose: c.choose},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !d.wasStarted() || len(c.asked) != 1 || len(res.Exchanges) != 1 || res.Exchanges[0].Choice != "start" {
		t.Fatalf("started %v, asked %q, exchanges %+v", d.wasStarted(), c.asked, res.Exchanges)
	}
}

// TestConfigWith_RefusesWhatItCannotDo: expecting a disconnect and options
// Dialog refuses are refused before any line is sent.
func TestConfigWith_RefusesWhatItCannotDo(t *testing.T) {
	d := &configIOS{}
	session := newConfigSession(t, d)
	for name, opts := range map[string]netcli.ConfigOptions{
		"a disconnect":   {Dialog: netcli.DialogOptions{ExpectDisconnect: true}},
		"a bare pattern": {Dialog: netcli.DialogOptions{Prompts: []netcli.Prompt{{}}}},
	} {
		if _, err := session.ConfigWith(context.Background(), []string{"never sent"}, opts); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	for _, line := range d.received() {
		if line == "never sent" || line == "configure terminal" {
			t.Errorf("the device was sent %q", line)
		}
	}
}

// TestConfigWith_APasswordPromptNothingAnswersFailsAtOnce: a line left at a
// password prompt fails at once, as an unanswered [yes/no] does.
func TestConfigWith_APasswordPromptNothingAnswersFailsAtOnce(t *testing.T) {
	d := &configIOS{}
	err := newConfigSession(t, d).Config(context.Background(), []string{"start ca"})
	if err == nil || !strings.Contains(err.Error(), `"Password:"`) {
		t.Fatalf("err %v", err)
	}
	if got := d.received(); got[len(got)-1] != "start ca" {
		t.Errorf("device received %q after the prompt", got)
	}
}

// TestConfigWith_AKeyPairMadeTwiceAndRemoved: making a key pair and making
// it again are notices only; removing it answers its question, its
// preamble a notice; and removing one that is gone rejects unless the task
// accepts it, all in IOS's own words.
func TestConfigWith_AKeyPairMadeTwiceAndRemoved(t *testing.T) {
	d := &configIOS{}
	make := "crypto key generate rsa general-keys label PLTEST modulus 2048"
	res, err := newConfigSession(t, d).ConfigWith(context.Background(), []string{make, make}, netcli.ConfigOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Notices) != 6 || !slices.Contains(res.Notices, "% They will be replaced.") {
		t.Errorf("making it twice: notices %q", res.Notices)
	}
	remove := netcli.Prompt{Match: regexp.MustCompile(`Do you really want to remove these keys\? \[yes/no\]`), Answer: "yes"}
	res, err = newConfigSession(t, d).ConfigWith(context.Background(), []string{"crypto key zeroize rsa PLTEST"}, netcli.ConfigOptions{
		Dialog: netcli.DialogOptions{Prompts: []netcli.Prompt{remove}},
	})
	if err != nil || d.keyHeld() || len(res.Exchanges) != 1 || len(res.Notices) != 2 {
		t.Fatalf("removing it: %v, key kept %v, exchanges %+v, notices %q", err, d.keyHeld(), res.Exchanges, res.Notices)
	}
	if _, err := newConfigSession(t, d).ConfigWith(context.Background(), []string{"crypto key zeroize rsa PLTEST"}, netcli.ConfigOptions{}); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("removing it again: %v", err)
	}
	res, err = newConfigSession(t, d).ConfigWith(context.Background(), []string{"crypto key zeroize rsa PLTEST"}, netcli.ConfigOptions{
		Accept: []*regexp.Regexp{regexp.MustCompile(`^% The specified keypair does not exist`)},
	})
	if err != nil || len(res.Notices) != 1 {
		t.Errorf("removing it again, accepted: %v, notices %q", err, res.Notices)
	}
}
