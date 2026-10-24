// Tests of attaching to a CML console line and answering a node's login,
// against a scripted shell that answers each read with what a real console
// server and IOS node print (the server's text captured from CML 2.7.2 in
// the DevNet sandbox, 2026-10-24).
package cmlconsole_test

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/cmlconsole"
)

// line is a console UUID.
const line = "0ba0f180-c5b2-4dcf-af64-bf9bb645a566"

// script is a shell whose reads return chunks in order, each read up to
// the first match in the chunks so far, and which records every line
// written.
type script struct {
	chunks  []string
	buf     string
	written []string
	closed  bool
}

func (s *script) WriteLine(_ context.Context, l string) error {
	if s.closed {
		return errors.New("closed")
	}
	s.written = append(s.written, l)
	return nil
}

func (s *script) ReadUntil(_ context.Context, re *regexp.Regexp, maxBytes int) (string, error) {
	for {
		if loc := re.FindStringIndex(s.buf); loc != nil {
			out := s.buf[:loc[1]]
			s.buf = s.buf[loc[1]:]
			return out, nil
		}
		if len(s.chunks) == 0 || len(s.buf) > maxBytes {
			return "", errors.New("read ended without a match")
		}
		s.buf += s.chunks[0]
		s.chunks = s.chunks[1:]
	}
}

func (s *script) Close() error { s.closed = true; return nil }

// serverPrompt is the console server's prompt as it draws it.
const serverPrompt = "\x1b[J\x1b[2K\rconsoles> "

// TestAttach_ConnectsByUUIDAndSaysWhyNot: the server's banner and prompt,
// then a connect by UUID that attaches; a console the server does not
// have is refused with the server's own words; and a line that is not a
// UUID never reaches the server's prompt.
func TestAttach_ConnectsByUUIDAndSaysWhyNot(t *testing.T) {
	ok := &script{chunks: []string{
		"\r\n****\r\nCML2 Console Server\r\n****\r\n" + serverPrompt,
		serverPrompt + "c" + serverPrompt + "connect " + line + "\r\nConnecting to console for R1\r\nConnected to CML terminalserver.\r\nEscape character is '^]'.\r\n",
	}}
	if err := cmlconsole.Attach(context.Background(), ok, line); err != nil {
		t.Fatal(err)
	}
	if strings.Join(ok.written, "|") != "connect "+line {
		t.Errorf("wrote %q", ok.written)
	}
	missing := &script{chunks: []string{serverPrompt, "connect " + line + "\r\nconsole \"" + line + "\" not found!\r\n" + serverPrompt}}
	if err := cmlconsole.Attach(context.Background(), missing, line); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("a console the server lacks: %v", err)
	}
	for _, bad := range []string{"/default-lab/R1/0", line + "\rexit", "", strings.ToUpper(line)} {
		s := &script{chunks: []string{serverPrompt}}
		if err := cmlconsole.Attach(context.Background(), s, bad); err == nil || len(s.written) != 0 {
			t.Errorf("line %q: %v, wrote %q", bad, err, s.written)
		}
	}
	if err := cmlconsole.Attach(context.Background(), &script{}, line); err == nil || !strings.Contains(err.Error(), "prompt") {
		t.Errorf("a server that never prompts: %v", err)
	}
}

// TestLogIn_AnswersWhatTheNodeAsks: each shape a node's line can be in
// when it is attached ends at its exec prompt, with that prompt asked for
// again for the next reader, and each answer sent once.
func TestLogIn_AnswersWhatTheNodeAsks(t *testing.T) {
	login := cmlconsole.Login{Username: "netops", Password: "pw"}
	for name, tc := range map[string]struct {
		chunks []string
		writes string
	}{
		"a login":         {[]string{"\r\nUser Access Verification\r\n\r\nUsername: ", "Password: ", "\r\nP1#"}, "|netops|pw|"},
		"already in":      {[]string{"\r\nR1>"}, "|"},
		"press return":    {[]string{"\r\nP1 con0 is now available\r\n\r\nPress RETURN to get started.\r\n", "\r\nUsername: ", "Password: ", "P1>"}, "||netops|pw|"},
		"config mode":     {[]string{"\r\nP1(config-if)#", "\r\nP1#"}, "|end|"},
		"boot then login": {[]string{"*Oct 24 19:01:02.003: %SYS-5-RESTART: System restarted --\r\n", "Press RETURN to get started!\r\n", "Username: ", "Password: ", "P1#"}, "||netops|pw|"},
	} {
		s := &script{chunks: tc.chunks}
		if err := cmlconsole.LogIn(context.Background(), s, login); err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		// Every dialog opens with a return and ends with one, asking the
		// prompt again for the next reader.
		if got := strings.Join(s.written, "|"); got != tc.writes {
			t.Errorf("%s: wrote %q, want %q", name, got, tc.writes)
		}
	}
}

// TestLogIn_Refusals: a username or password asked twice is a refused
// login, a login the node asks for that the device has none of is
// refused, a dialog past its steps and a line that goes quiet are errors,
// and no error ever carries the password.
func TestLogIn_Refusals(t *testing.T) {
	for name, tc := range map[string]struct {
		chunks []string
		login  cmlconsole.Login
		want   string
	}{
		"a wrong password": {[]string{"Username: ", "Password: ", "\r\n% Login invalid\r\n\r\nUsername: "}, cmlconsole.Login{Username: "netops", Password: "pw"}, "username again"},
		"a password twice": {[]string{"Password: ", "\r\nPassword: "}, cmlconsole.Login{Password: "pw"}, "password again"},
		"no username":      {[]string{"Username: "}, cmlconsole.Login{}, "username again"},
		"no password":      {[]string{"Password: "}, cmlconsole.Login{Username: "netops"}, "password again"},
		"endless returns":  {[]string{"Press RETURN to get started.\r\n", "Press RETURN to get started.\r\n", "Press RETURN to get started.\r\n", "Press RETURN to get started.\r\n", "Press RETURN to get started.\r\n", "Press RETURN to get started.\r\n", "Press RETURN to get started.\r\n"}, cmlconsole.Login{}, "steps"},
		"a quiet line":     {[]string{"booting..."}, cmlconsole.Login{}, "waiting for the node"},
	} {
		err := cmlconsole.LogIn(context.Background(), &script{chunks: tc.chunks}, tc.login)
		if err == nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "pw") && tc.login.Password != "" && strings.Contains(err.Error(), tc.login.Password) {
			t.Errorf("%s: %v, want an error mentioning %q", name, err, tc.want)
		}
	}
	closed := &script{closed: true}
	if err := cmlconsole.LogIn(context.Background(), closed, cmlconsole.Login{}); err == nil {
		t.Error("a closed line was logged in to")
	}
}

// TestLogOut_EndsTheSession: the logout leaves any mode and exits, and a
// line already gone is not an error.
func TestLogOut_EndsTheSession(t *testing.T) {
	s := &script{chunks: []string{"\r\nP1#"}}
	if err := cmlconsole.LogOut(s); err != nil || strings.Join(s.written, "|") != "end|exit" {
		t.Errorf("logout: %v, wrote %q", err, s.written)
	}
	if err := cmlconsole.LogOut(&script{closed: true}); err != nil {
		t.Errorf("a line already gone: %v", err)
	}
	if err := cmlconsole.LogOut(&script{}); err != nil {
		t.Errorf("a line that never answers: %v", err)
	}
}
