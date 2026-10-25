// Package netcli_test: Session.Dialog against a scripted IOS device that asks
// questions the way a real one does, over a real SSH session.
package netcli_test

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcli"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"golang.org/x/crypto/ssh"
)

// askingIOS is a fake IOS device whose commands ask questions the way a
// real one's do: copy asks for a destination filename and, when the file
// exists, whether to overwrite it; delete asks for the filename and to
// confirm; reload asks to confirm and then drops the session. Each
// question is printed with no line break after it, as IOS prints it, and
// the device waits for the next line as its answer.
//
// The question texts are IOS XE's (live_probe_test.go records them from a
// real device).
type askingIOS struct {
	mu       sync.Mutex
	hostname string
	files    map[string]bool
	lines    []string // every line received, answers included
	reloaded bool
}

func (d *askingIOS) received() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.lines...)
}

func (d *askingIOS) has(name string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.files[name]
}

// serve runs one session: the first prompt, then each command's script.
func (d *askingIOS) serve(channel ssh.Channel) {
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
	// ask prints a question and returns the answer, echoed as a terminal
	// echoes it.
	ask := func(question string) (string, bool) {
		write(question)
		answer, ok := read()
		write(answer + "\r\n")
		return answer, ok
	}
	prompt := d.hostname + "#"

	write(prompt)
	for {
		line, ok := read()
		if !ok {
			return
		}
		write(line + "\r\n")
		switch {
		case strings.HasPrefix(line, "copy running-config flash:"):
			name := strings.TrimPrefix(line, "copy running-config flash:")
			answer, ok := ask("Destination filename [" + name + "]? ")
			if !ok {
				return
			}
			if answer != "" {
				name = answer
			}
			if d.has(name) {
				write("%Warning:There is a file already existing with this name\r\n")
				if confirm, ok := ask("Do you want to over write? [confirm]"); !ok || confirm != "" {
					write("%Error: copy aborted\r\n" + prompt)
					continue
				}
			}
			d.mu.Lock()
			d.files[name] = true
			d.mu.Unlock()
			write("5184 bytes copied in 0.067 secs (77373 bytes/sec)\r\n" + prompt)
		case strings.HasPrefix(line, "delete flash:"):
			name := strings.TrimPrefix(line, "delete flash:")
			if _, ok := ask("Delete filename [" + name + "]? "); !ok {
				return
			}
			if confirm, ok := ask("Delete bootflash:/" + name + "? [confirm]"); !ok || confirm != "" {
				write(prompt)
				continue
			}
			d.mu.Lock()
			delete(d.files, name)
			d.mu.Unlock()
			write(prompt)
		case line == "reload":
			if confirm, ok := ask("Proceed with reload? [confirm]"); !ok || confirm != "" {
				write(prompt)
				continue
			}
			d.mu.Lock()
			d.reloaded = true
			d.mu.Unlock()
			return // the session drops as the device reloads
		case line == "drop":
			return
		case line == "ask twice":
			if _, ok := ask("Proceed? [confirm]"); !ok {
				return
			}
			if _, ok := ask("Proceed? [confirm]"); !ok {
				return
			}
			write(prompt)
		case line == "copy scp: flash:":
			if _, ok := ask("Password: "); !ok {
				return
			}
			write(prompt)
		case line == "split question":
			write("Destination fil")
			time.Sleep(50 * time.Millisecond)
			answer, ok := ask("ename [x.bin]? ")
			if !ok {
				return
			}
			write("copied " + answer + "\r\n" + prompt)
		case line == "show secret":
			write("enable secret 9 $9$abc")
		case line == "finished question":
			write("Is this a question?\r\nNo, it is output.\r\n" + prompt)
		default:
			write(prompt)
		}
	}
}

// newAskingSession opens a real session over SSH against d.
func newAskingSession(t *testing.T, d *askingIOS) *netcli.Session {
	t.Helper()
	if d.files == nil {
		d.files = map[string]bool{}
	}
	conn := dialFakeIOSServer(t, newFakeIOSServer(t, d.serve))
	shell, err := conn.Shell(context.Background(), remoteexec.ShellOptions{})
	if err != nil {
		t.Fatalf("Shell: %v", err)
	}
	t.Cleanup(func() { _ = shell.Close() })
	session, err := netcli.Open(context.Background(), shell, netcli.FromPrompt("r1#"), netcli.Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return session
}

// destination answers IOS's destination filename question with Enter.
func destination() netcli.Prompt {
	return netcli.Prompt{Match: regexp.MustCompile(`Destination filename \[[^\]]*\]\?`)}
}

func TestDialog_AnswersAListedQuestion(t *testing.T) {
	d := &askingIOS{hostname: "r1"}
	session := newAskingSession(t, d)

	res, err := session.Dialog(context.Background(), "copy running-config flash:a.cfg", netcli.DialogOptions{Prompts: []netcli.Prompt{destination()}})
	if err != nil {
		t.Fatal(err)
	}
	if !d.has("a.cfg") || !strings.Contains(res.Output, "bytes copied") || strings.HasSuffix(res.Output, "r1#") {
		t.Fatalf("output %q, file written %v", res.Output, d.has("a.cfg"))
	}
	if len(res.Exchanges) != 1 || res.Exchanges[0].Question != "Destination filename [a.cfg]?" || res.Exchanges[0].Prompt != 0 || res.Exchanges[0].Answer != "" {
		t.Fatalf("exchanges %+v", res.Exchanges)
	}
	if got := d.received(); strings.Join(got, ",") != "copy running-config flash:a.cfg," {
		t.Fatalf("device received %q, want the command and one Enter", got)
	}
}

func TestDialog_AnswersEachQuestionOfACommand(t *testing.T) {
	d := &askingIOS{hostname: "r1", files: map[string]bool{"old.cfg": true}}
	session := newAskingSession(t, d)

	res, err := session.Dialog(context.Background(), "delete flash:old.cfg", netcli.DialogOptions{Prompts: []netcli.Prompt{
		{Match: regexp.MustCompile(`Delete filename \[[^\]]*\]\?`)},
		{Match: regexp.MustCompile(`Delete \S+\? \[confirm\]`)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if d.has("old.cfg") || len(res.Exchanges) != 2 {
		t.Fatalf("file still there: %v; exchanges %+v", d.has("old.cfg"), res.Exchanges)
	}
}

func TestDialog_RefusedQuestionAbandonsTheCommand(t *testing.T) {
	d := &askingIOS{hostname: "r1", files: map[string]bool{"a.cfg": true}}
	session := newAskingSession(t, d)

	_, err := session.Dialog(context.Background(), "copy running-config flash:a.cfg", netcli.DialogOptions{Prompts: []netcli.Prompt{
		destination(),
		{Match: regexp.MustCompile(`over ?write\? \[confirm\]`), Refuse: true},
	}})
	if err == nil || !strings.Contains(err.Error(), `"Do you want to over write? [confirm]", which this task refuses`) {
		t.Fatalf("err %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	if got := d.received(); len(got) != 2 {
		t.Fatalf("device received %q: nothing may answer the refused question", got)
	}
}

func TestDialog_FailsWhenAnAnsweredQuestionComesBack(t *testing.T) {
	session := newAskingSession(t, &askingIOS{hostname: "r1"})

	_, err := session.Dialog(context.Background(), "ask twice", netcli.DialogOptions{Prompts: []netcli.Prompt{{Match: regexp.MustCompile(`Proceed\? \[confirm\]`)}}})
	if err == nil || !strings.Contains(err.Error(), "again after it was answered") {
		t.Fatalf("err %v", err)
	}
}

func TestDialog_ListingAQuestionTwiceAnswersItTwice(t *testing.T) {
	session := newAskingSession(t, &askingIOS{hostname: "r1"})
	proceed := netcli.Prompt{Match: regexp.MustCompile(`Proceed\? \[confirm\]`)}

	res, err := session.Dialog(context.Background(), "ask twice", netcli.DialogOptions{Prompts: []netcli.Prompt{proceed, proceed}})
	if err != nil || len(res.Exchanges) != 2 || res.Exchanges[1].Prompt != 1 {
		t.Fatalf("err %v, exchanges %+v", err, res.Exchanges)
	}
}

func TestDialog_NamesTheQuestionItWasLeftWaitingOn(t *testing.T) {
	session := newAskingSession(t, &askingIOS{hostname: "r1"})
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	_, err := session.Dialog(ctx, "copy running-config flash:a.cfg", netcli.DialogOptions{})
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), `waiting on "Destination filename [a.cfg]?"`) {
		t.Fatalf("err %v", err)
	}
}

func TestDialog_NeverNamesOutputThatIsNotAQuestion(t *testing.T) {
	session := newAskingSession(t, &askingIOS{hostname: "r1"})
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	_, err := session.Dialog(ctx, "show secret", netcli.DialogOptions{})
	if err == nil || strings.Contains(err.Error(), "$9$") || strings.Contains(err.Error(), "waiting on") {
		t.Fatalf("err %v: a stalled line that is not a question must not be named", err)
	}
}

func TestDialog_ADisconnectAfterAnAnswerIsSuccessWhenExpected(t *testing.T) {
	d := &askingIOS{hostname: "r1"}
	session := newAskingSession(t, d)

	res, err := session.Dialog(context.Background(), "reload", netcli.DialogOptions{
		Prompts:          []netcli.Prompt{{Match: regexp.MustCompile(`Proceed with reload\? \[confirm\]`)}},
		ExpectDisconnect: true,
	})
	if err != nil || !res.Disconnected || len(res.Exchanges) != 1 {
		t.Fatalf("err %v, result %+v", err, res)
	}
}

func TestDialog_ADisconnectIsAFailureWithoutAnAnswerOrUnexpected(t *testing.T) {
	for _, tc := range []struct {
		name, line string
		opts       netcli.DialogOptions
	}{
		{"before any answer", "drop", netcli.DialogOptions{ExpectDisconnect: true}},
		{"not expected", "reload", netcli.DialogOptions{Prompts: []netcli.Prompt{{Match: regexp.MustCompile(`Proceed with reload\?`)}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session := newAskingSession(t, &askingIOS{hostname: "r1"})
			if res, err := session.Dialog(context.Background(), tc.line, tc.opts); err == nil || res.Disconnected {
				t.Fatalf("err %v, result %+v", err, res)
			}
		})
	}
}

func TestDialog_SendsNoTextToACredentialQuestion(t *testing.T) {
	d := &askingIOS{hostname: "r1"}
	session := newAskingSession(t, d)

	_, err := session.Dialog(context.Background(), "copy scp: flash:", netcli.DialogOptions{Prompts: []netcli.Prompt{{Match: regexp.MustCompile(`Password:`), Answer: "hunter2"}}})
	if err == nil || !strings.Contains(err.Error(), "for a credential") {
		t.Fatalf("err %v", err)
	}
	for _, line := range d.received() {
		if strings.Contains(line, "hunter2") {
			t.Fatal("the answer reached the device")
		}
	}
}

func TestDialog_AQuestionSplitAcrossReadsIsStillAnswered(t *testing.T) {
	session := newAskingSession(t, &askingIOS{hostname: "r1"})

	res, err := session.Dialog(context.Background(), "split question", netcli.DialogOptions{Prompts: []netcli.Prompt{{Match: regexp.MustCompile(`Destination filename`), Answer: "y.bin"}}})
	if err != nil || !strings.Contains(res.Output, "copied y.bin") {
		t.Fatalf("err %v, output %q", err, res.Output)
	}
}

// chooser is a Chooser that answers from a script and records what it
// was asked.
type chooser struct {
	decisions []netcli.Decision
	asked     []string
	err       error
}

func (c *chooser) choose(_ context.Context, question string, _ []netcli.Choice) (netcli.Decision, error) {
	c.asked = append(c.asked, question)
	if c.err != nil {
		return netcli.Decision{}, c.err
	}
	d := c.decisions[0]
	c.decisions = c.decisions[1:]
	return d, nil
}

// overwriteChoices are the answers a chooser may pick at IOS's overwrite
// question.
func overwriteChoices() []netcli.Choice {
	return []netcli.Choice{
		{ID: "confirm", Answer: "", Means: "Press Enter to confirm."},
		{ID: "decline", Answer: "n", Means: "Answer no."},
	}
}

func TestDialog_AChooserAnswersAQuestionNobodyListed(t *testing.T) {
	d := &askingIOS{hostname: "r1", files: map[string]bool{"a.cfg": true}}
	session := newAskingSession(t, d)
	c := &chooser{decisions: []netcli.Decision{{ID: "confirm", Reason: "the file is this run's own", Detail: map[string]any{"confidence": 0.9}}}}

	res, err := session.Dialog(context.Background(), "copy running-config flash:a.cfg", netcli.DialogOptions{
		Prompts: []netcli.Prompt{destination()},
		Ask:     &netcli.Ask{Choices: overwriteChoices(), Choose: c.choose},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.asked) != 1 || c.asked[0] != "Do you want to over write? [confirm]" {
		t.Fatalf("chooser asked %q: only the unlisted question goes to it", c.asked)
	}
	ex := res.Exchanges[1]
	if ex.Prompt != -1 || ex.Choice != "confirm" || ex.Reason == "" || ex.Detail["confidence"] != 0.9 {
		t.Fatalf("exchange %+v", ex)
	}
}

func TestDialog_AChoosersRefusalOrUnknownPickStopsTheCommand(t *testing.T) {
	for _, tc := range []struct {
		name string
		c    *chooser
		want string
	}{
		{"refuse", &chooser{decisions: []netcli.Decision{{ID: netcli.ChoiceRefuse, Reason: "not this run's file"}}}, "the chooser refused it: not this run's file"},
		{"unknown", &chooser{decisions: []netcli.Decision{{ID: "format_flash"}}}, `picked "format_flash"`},
		{"error", &chooser{err: errors.New("held for review")}, "held for review"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := &askingIOS{hostname: "r1", files: map[string]bool{"a.cfg": true}}
			session := newAskingSession(t, d)
			_, err := session.Dialog(context.Background(), "copy running-config flash:a.cfg", netcli.DialogOptions{
				Prompts: []netcli.Prompt{destination()},
				Ask:     &netcli.Ask{Choices: overwriteChoices(), Choose: tc.c.choose},
			})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err %v, want %q", err, tc.want)
			}
		})
	}
}

func TestDialog_AChoosersWaitReadsOnAndItsMaxIsKept(t *testing.T) {
	session := newAskingSession(t, &askingIOS{hostname: "r1"})
	c := &chooser{decisions: []netcli.Decision{{ID: netcli.ChoiceWait}, {ID: netcli.ChoiceWait}}}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	_, err := session.Dialog(ctx, "ask twice", netcli.DialogOptions{Ask: &netcli.Ask{Choices: overwriteChoices(), Choose: c.choose, Max: 1}})
	if !errors.Is(err, context.DeadlineExceeded) || len(c.asked) != 1 {
		t.Fatalf("err %v after %d choices: a wait leaves the question to the deadline", err, len(c.asked))
	}

	session = newAskingSession(t, &askingIOS{hostname: "r1"})
	c = &chooser{decisions: []netcli.Decision{{ID: "confirm"}, {ID: "confirm"}}}
	_, err = session.Dialog(context.Background(), "ask twice", netcli.DialogOptions{Ask: &netcli.Ask{Choices: overwriteChoices(), Choose: c.choose, Max: 1}})
	if err == nil || !strings.Contains(err.Error(), "the 1 questions this task lets a chooser answer are used") || len(c.asked) != 1 {
		t.Fatalf("err %v after %d choices", err, len(c.asked))
	}
}

func TestDialog_AFinishedOutputLineIsNotAQuestion(t *testing.T) {
	session := newAskingSession(t, &askingIOS{hostname: "r1"})
	c := &chooser{}

	res, err := session.Dialog(context.Background(), "finished question", netcli.DialogOptions{Ask: &netcli.Ask{Choices: overwriteChoices(), Choose: c.choose}})
	if err != nil || len(c.asked) != 0 || !strings.Contains(res.Output, "No, it is output.") {
		t.Fatalf("err %v, chooser asked %q, output %q", err, c.asked, res.Output)
	}
}

func TestDialog_RefusesOptionsItCouldOnlyActOnWrongly(t *testing.T) {
	re := regexp.MustCompile(`x`)
	choose := (&chooser{}).choose
	for _, tc := range []struct {
		name string
		opts netcli.DialogOptions
	}{
		{"no pattern", netcli.DialogOptions{Prompts: []netcli.Prompt{{}}}},
		{"refuse and answer", netcli.DialogOptions{Prompts: []netcli.Prompt{{Match: re, Refuse: true, Answer: "y"}}}},
		{"answer with a line break", netcli.DialogOptions{Prompts: []netcli.Prompt{{Match: re, Answer: "y\rshow run"}}}},
		{"no chooser", netcli.DialogOptions{Ask: &netcli.Ask{Choices: overwriteChoices()}}},
		{"no choices", netcli.DialogOptions{Ask: &netcli.Ask{Choose: choose}}},
		{"negative max", netcli.DialogOptions{Ask: &netcli.Ask{Choices: overwriteChoices(), Choose: choose, Max: -1}}},
		{"reserved id", netcli.DialogOptions{Ask: &netcli.Ask{Choices: []netcli.Choice{{ID: netcli.ChoiceWait}}, Choose: choose}}},
		{"empty id", netcli.DialogOptions{Ask: &netcli.Ask{Choices: []netcli.Choice{{Answer: "y"}}, Choose: choose}}},
		{"duplicate id", netcli.DialogOptions{Ask: &netcli.Ask{Choices: []netcli.Choice{{ID: "a"}, {ID: "a"}}, Choose: choose}}},
		{"choice with a line break", netcli.DialogOptions{Ask: &netcli.Ask{Choices: []netcli.Choice{{ID: "a", Answer: "y\n"}}, Choose: choose}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Refused before any I/O, so a nil session's shell is never
			// reached.
			if _, err := (&netcli.Session{}).Dialog(context.Background(), "x", tc.opts); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}

// abandoningShell is a shell that keeps a question open after it closes,
// as a console line does, recording each question it was asked to abandon
// and declining it or not as told.
type abandoningShell struct {
	netcli.Shell
	declines  bool
	abandoned []string
}

// Abandon implements netcli.Abandoner.
func (a *abandoningShell) Abandon(question string) bool {
	a.abandoned = append(a.abandoned, question)
	return a.declines
}

// TestDialog_AnAbandonerHearsTheQuestionBeforeItCloses: a question the task
// refuses, or whose chooser fails, on a shell that keeps it open after
// closing, is handed to that shell's Abandon before the session closes,
// and the error says whether it was declined or left waiting; nothing
// confirms it.
func TestDialog_AnAbandonerHearsTheQuestionBeforeItCloses(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	refuse := netcli.DialogOptions{Prompts: []netcli.Prompt{{Match: regexp.MustCompile(`\[confirm\]`), Refuse: true}}}
	failing := netcli.DialogOptions{Ask: &netcli.Ask{
		Choices: []netcli.Choice{{ID: "confirm", Answer: "", Means: "Reload."}},
		Choose: func(context.Context, string, []netcli.Choice) (netcli.Decision, error) {
			return netcli.Decision{}, errors.New("the model is not answering")
		},
	}}
	for name, tc := range map[string]struct {
		opts     netcli.DialogOptions
		declines bool
		want     string
	}{
		"refused, declined":     {refuse, true, "the question was declined and the session closed"},
		"refused, left waiting": {refuse, false, `left waiting at "Proceed with reload? [confirm]"`},
		"a chooser that fails":  {failing, true, "the model is not answering; the question was declined"},
	} {
		d := &askingIOS{hostname: "r1", files: map[string]bool{}}
		conn := dialFakeIOSServer(t, newFakeIOSServer(t, d.serve))
		raw, err := conn.Shell(ctx, remoteexec.ShellOptions{})
		if err != nil {
			t.Fatal(err)
		}
		shell := &abandoningShell{Shell: raw, declines: tc.declines}
		session, err := netcli.Open(ctx, shell, netcli.FromPrompt("r1#"), netcli.Options{})
		if err != nil {
			t.Fatal(err)
		}
		_, err = session.Dialog(ctx, "reload", tc.opts)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want it to say %q", name, err, tc.want)
		}
		if len(shell.abandoned) != 1 || shell.abandoned[0] != "Proceed with reload? [confirm]" {
			t.Errorf("%s: abandoned %q", name, shell.abandoned)
		}
		d.mu.Lock()
		reloaded := d.reloaded
		d.mu.Unlock()
		if reloaded {
			t.Errorf("%s: the device reloaded", name)
		}
	}
}
