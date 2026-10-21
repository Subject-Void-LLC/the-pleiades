// Package remoteexectest: the interactive half of the server, for the
// methods that open a shell rather than run a command.
//
// # Why this half is scripted where the exec half is not
//
// Everything else here hands work to a real /bin/sh, because a stand-in
// that pattern-matched on a command string would prove nothing about what
// a method builds. A network device's CLI has no local equivalent: there
// is no IOS to run, and /bin/sh over a channel with no terminal prints no
// prompt at all, which is the one thing an interactive session
// synchronizes on. So a Device is a script: a prompt, and a reply per
// line.
//
// That is enough for what it is for, and not more. It covers a method
// OPENING a session against something that speaks the protocol (the
// channel, the pty-req and shell requests, the first prompt, the paging
// command a dialect sends, a line, its echo, its reply, and the close),
// which nothing else exercises outside a release gate against real
// hardware. It proves nothing about a real device's own conventions;
// pkg/netcli tests those against bytes captured from real hardware, and a
// method's release gate runs against the hardware itself.
package remoteexectest

import (
	"io"
	"strings"
	"sync"

	cryptossh "golang.org/x/crypto/ssh"
)

// Device is a scripted CLI: what the server answers a shell request
// with. The zero Device is not usable; a Prompt is required, since a
// session with no prompt is one no client can synchronize on.
type Device struct {
	// Prompt is printed after the banner and after every reply, on its
	// own line. It has to match whatever pattern the client waits for,
	// which for a vendor dialect is that vendor's own (netcli.IOS wants
	// something like "r1#").
	Prompt string

	// Banner is printed once, before the first prompt, as a real device's
	// login banner is.
	Banner string

	// Replies answers one line by its exact text. A line with no entry is
	// answered with Unknown.
	Replies map[string]string

	// Unknown answers a line Replies does not name. Empty means the line
	// is answered with the prompt alone, which is what a device does for
	// a command that prints nothing.
	Unknown string

	// Asks scripts a line that asks questions before it finishes, as a
	// copy, a delete or a reload does: each Question is printed with no
	// line break after it, and the next line received is its answer,
	// echoed and recorded in the command log even when empty. Once every
	// question is answered as it wants, the line's reply from Replies
	// follows (none when Replies has no entry). A line named here is
	// never answered with Unknown.
	Asks map[string][]Question

	// Hangup names lines after which the device closes the session once
	// their questions are answered, as a device that reloads does.
	Hangup map[string]bool

	// AfterHangup answers a line in place of Replies once a Hangup line
	// has run, as a device that reloaded into new software answers "show
	// version" differently. A line it does not name keeps its Replies
	// answer.
	AfterHangup map[string]string

	// Config, when set, is a configuration mode: its Enter line switches
	// the prompt to its Prompt until its Exit line, as IOS's "configure
	// terminal" and "end" do. Lines sent in it are recorded like any
	// other.
	Config *ConfigMode

	// mu guards the state every session of one server shares: whether a
	// Hangup line has run, for AfterHangup, and whether the device is in
	// its configuration mode.
	mu       sync.Mutex
	hungUp   bool
	inConfig bool
}

// ConfigMode is a scripted configuration mode.
type ConfigMode struct {
	Enter, Exit, Prompt string
}

// prompt is the prompt the device prints now, switching into or out of
// its configuration mode on line.
func (d *Device) prompt(line string) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.Config != nil {
		switch line {
		case d.Config.Enter:
			d.inConfig = true
		case d.Config.Exit:
			d.inConfig = false
		}
		if d.inConfig {
			return d.Config.Prompt
		}
	}
	return d.Prompt
}

// reply is the scripted answer to line: AfterHangup's once the device has
// hung up, else Replies', else Unknown.
func (d *Device) reply(line string) string {
	d.mu.Lock()
	hungUp := d.hungUp
	d.mu.Unlock()
	if r, ok := d.AfterHangup[line]; ok && hungUp {
		return r
	}
	if r, ok := d.Replies[line]; ok {
		return r
	}
	if _, asks := d.Asks[line]; asks {
		return ""
	}
	return d.Unknown
}

// hangUp records that a Hangup line ran.
func (d *Device) hangUp() {
	d.mu.Lock()
	d.hungUp = true
	d.mu.Unlock()
}

// Question is one question a scripted line asks.
type Question struct {
	// Text is printed as it is, with no line break after it.
	Text string
	// Want is the answer that lets the line go on; empty is Enter alone.
	Want string
	// Declined is printed when the answer is anything else, and the line
	// ends there with the prompt.
	Declined string
}

// serveDeviceSession answers pty-req and shell, then runs the script
// until the client closes the channel: the banner and first prompt, then
// for each line the echo a real terminal produces, the reply, and the
// prompt again.
//
// Every line is recorded in the server's command log, so a test reads
// back what the method sent exactly as it does for exec commands.
func serveDeviceSession(channel cryptossh.Channel, requests <-chan *cryptossh.Request, log *commandLog, device *Device) {
	defer func() { _ = channel.Close() }()

	shell := make(chan struct{})
	var once sync.Once
	go func() {
		for req := range requests {
			switch req.Type {
			case "pty-req", "env", "window-change":
				if req.WantReply {
					_ = req.Reply(true, nil)
				}
			case "shell":
				if req.WantReply {
					_ = req.Reply(true, nil)
				}
				once.Do(func() { close(shell) })
			default:
				if req.WantReply {
					_ = req.Reply(false, nil)
				}
			}
		}
		once.Do(func() { close(shell) })
	}()

	<-shell

	// A new session starts in exec mode, whatever an earlier one left.
	device.mu.Lock()
	device.inConfig = false
	device.mu.Unlock()
	if device.Banner != "" && !write(channel, device.Banner+"\r\n") {
		return
	}
	if !write(channel, device.Prompt) {
		return
	}

	// A client submits a line with a carriage return, as a terminal does
	// (remoteexec.Shell.WriteLine), so either terminator ends a line here
	// and a CRLF pair counts once.
	var line strings.Builder
	buf := make([]byte, 1)
	lastWasCR := false
	var asking *asking
	for {
		n, err := channel.Read(buf)
		if n > 0 {
			c := buf[0]
			switch {
			case c == '\n' && lastWasCR:
				lastWasCR = false
			case c == '\r' || c == '\n':
				lastWasCR = c == '\r'
				var open bool
				if asking != nil {
					asking, open = asking.answer(channel, log, device, line.String())
				} else {
					asking, open = answer(channel, log, device, line.String())
				}
				if !open {
					return
				}
				line.Reset()
			default:
				lastWasCR = false
				line.WriteByte(c)
			}
		}
		if err != nil {
			return
		}
	}
}

// answer replies to one submitted line: the echo a terminal produces,
// the scripted reply, and the prompt again, or for a line that asks, its
// first question and the state that reads its answer. An empty line is a
// bare Enter, which a device answers with the prompt alone. It reports
// whether the channel is still open.
func answer(channel cryptossh.Channel, log *commandLog, device *Device, line string) (*asking, bool) {
	if line == "" {
		return nil, write(channel, device.prompt(line))
	}
	log.add(line)
	if !write(channel, line+"\r\n") {
		return nil, false
	}
	if questions := device.Asks[line]; len(questions) > 0 {
		return &asking{line: line, questions: questions}, write(channel, questions[0].Text)
	}
	if reply := device.reply(line); reply != "" && !write(channel, reply+"\r\n") {
		return nil, false
	}
	return nil, write(channel, device.prompt(line))
}

// asking is a scripted line partway through its questions.
type asking struct {
	line      string
	questions []Question
	next      int
}

// answer takes text as the answer to the question being asked: it is
// echoed and recorded, and the line goes on to its next question, ends
// declined, or finishes with its reply and the prompt, or with the session
// closed for a line that hangs up.
func (a *asking) answer(channel cryptossh.Channel, log *commandLog, device *Device, text string) (*asking, bool) {
	log.add(text)
	if !write(channel, text+"\r\n") {
		return nil, false
	}
	q := a.questions[a.next]
	if text != q.Want {
		if q.Declined != "" && !write(channel, q.Declined+"\r\n") {
			return nil, false
		}
		return nil, write(channel, device.prompt(""))
	}
	a.next++
	if a.next < len(a.questions) {
		return a, write(channel, a.questions[a.next].Text)
	}
	if reply := device.reply(a.line); reply != "" && !write(channel, reply+"\r\n") {
		return nil, false
	}
	if device.Hangup[a.line] {
		device.hangUp()
		return nil, false
	}
	return nil, write(channel, device.prompt(""))
}

// write sends s, reporting whether the channel took it.
func write(channel cryptossh.Channel, s string) bool {
	_, err := io.WriteString(channel, s)
	return err == nil
}
