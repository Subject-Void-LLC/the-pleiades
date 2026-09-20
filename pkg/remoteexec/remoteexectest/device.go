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

	write := func(s string) bool {
		_, err := io.WriteString(channel, s)
		return err == nil
	}
	if device.Banner != "" && !write(device.Banner+"\r\n") {
		return
	}
	if !write(device.Prompt) {
		return
	}

	// A client submits a line with a carriage return, as a terminal does
	// (remoteexec.Shell.WriteLine), so either terminator ends a line here
	// and a CRLF pair counts once.
	var line strings.Builder
	buf := make([]byte, 1)
	lastWasCR := false
	for {
		n, err := channel.Read(buf)
		if n > 0 {
			c := buf[0]
			switch {
			case c == '\n' && lastWasCR:
				lastWasCR = false
			case c == '\r' || c == '\n':
				lastWasCR = c == '\r'
				if !answer(channel, log, device, line.String()) {
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
// the scripted reply, and the prompt again. An empty line is a bare
// Enter, which a device answers with the prompt alone. It reports
// whether the channel is still writable.
func answer(channel cryptossh.Channel, log *commandLog, device *Device, line string) bool {
	write := func(s string) bool {
		_, err := io.WriteString(channel, s)
		return err == nil
	}
	if line != "" {
		log.add(line)
		if !write(line + "\r\n") {
			return false
		}
		reply, known := device.Replies[line]
		if !known {
			reply = device.Unknown
		}
		if reply != "" && !write(reply+"\r\n") {
			return false
		}
	}
	return write(device.Prompt)
}
