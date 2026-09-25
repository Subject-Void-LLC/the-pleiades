// Tests for the WS-Man messages this package builds and the conversation
// it holds.
//
// The messages are checked by parsing their real serialized XML, not by
// reading a Go constant, because a wrong option value or an unescaped
// character is only visible in the bytes. The conversation is driven
// through a scripted poster standing in for the library's transport: it
// proves the order of messages, stdin handling, the receive loop and the
// failure paths. It proves nothing about what a real WinRM service does
// with them; the Release Gate in modes_release_gate_test.go does that
// against a real Windows host, capturing the same messages on the wire.
package winrmexec

import (
	"context"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/masterzen/winrm"
	"github.com/masterzen/winrm/soap"
)

// xmlNode is one element found in a serialized message.
type xmlNode struct {
	attrs map[string]string
	text  string
}

// findElements returns every element named local (ignoring namespace) in
// doc, failing the test if doc is not well-formed XML.
func findElements(t *testing.T, doc, local string) []xmlNode {
	t.Helper()
	dec := xml.NewDecoder(strings.NewReader(doc))
	var found []xmlNode
	var stack []*xmlNode
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return found
		}
		if err != nil {
			t.Fatalf("message is not well-formed XML: %v\n%s", err, doc)
		}
		switch el := tok.(type) {
		case xml.StartElement:
			var node *xmlNode
			if el.Name.Local == local {
				node = &xmlNode{attrs: map[string]string{}}
				for _, a := range el.Attr {
					node.attrs[a.Name.Local] = a.Value
				}
			}
			stack = append(stack, node)
		case xml.CharData:
			if n := len(stack); n > 0 && stack[n-1] != nil {
				stack[n-1].text += string(el)
			}
		case xml.EndElement:
			n := len(stack)
			if stack[n-1] != nil {
				found = append(found, *stack[n-1])
			}
			stack = stack[:n-1]
		}
	}
}

// option returns the value of the WS-Man option named name in doc.
func option(t *testing.T, doc, name string) (string, bool) {
	t.Helper()
	for _, opt := range findElements(t, doc, "Option") {
		if opt.attrs["Name"] == name {
			return opt.text, true
		}
	}
	return "", false
}

func TestCommandMessage_EscapesTheLineWithNamedEntitiesOnly(t *testing.T) {
	line := `prog "a b" ]]> <x/> & ' ` + "\"\t"
	doc := commandMessage("http://h:5985/wsman", *winrm.DefaultParameters, "SHELL-1", line, false).String()
	// FALSE, because Windows runs the command through cmd.exe whatever it
	// is asked; see cmdexe.go.
	if v, ok := option(t, doc, "WINRS_SKIP_CMD_SHELL"); !ok || v != "FALSE" {
		t.Errorf("WINRS_SKIP_CMD_SHELL = %q (present %v), want FALSE", v, ok)
	}
	// The WinRM service does not decode numeric character references, so
	// none may appear: a quote or a tab travels as itself.
	if strings.Contains(doc, "&#") {
		t.Errorf("the message holds a numeric character reference: %s", doc)
	}
	if v, _ := option(t, doc, "WINRS_CONSOLEMODE_STDIN"); v != "TRUE" {
		t.Errorf("WINRS_CONSOLEMODE_STDIN = %q with no stdin, want TRUE (a console)", v)
	}
	withStdin := commandMessage("http://h:5985/wsman", *winrm.DefaultParameters, "SHELL-1", line, true).String()
	if v, _ := option(t, withStdin, "WINRS_CONSOLEMODE_STDIN"); v != "FALSE" {
		t.Errorf("WINRS_CONSOLEMODE_STDIN = %q with stdin, want FALSE (a pipe)", v)
	}
	commands := findElements(t, doc, "Command")
	if len(commands) != 1 || commands[0].text != line {
		t.Fatalf("Command elements = %+v, want exactly one carrying %q", commands, line)
	}
	if strings.Contains(doc, "CDATA") {
		t.Error("the line travels as escaped text, never a CDATA section")
	}
	if len(findElements(t, doc, "Arguments")) != 0 {
		t.Error("no Arguments elements: the whole line is in Command")
	}
}

func TestOpenShellMessage(t *testing.T) {
	spec := shellSpec{
		env:              []envVar{{name: "PLEIADES_A", value: `<&"'> $(x) %y%`}, {name: "PLEIADES_B", value: "two"}},
		workingDirectory: `C:\Work & Play`,
	}
	doc := openShellMessage("http://h:5985/wsman", *winrm.DefaultParameters, spec).String()
	if v, _ := option(t, doc, "WINRS_NOPROFILE"); v != "FALSE" {
		t.Errorf("WINRS_NOPROFILE = %q, want FALSE by default (see Options.NoProfile)", v)
	}
	vars := findElements(t, doc, "Variable")
	if len(vars) != 2 || vars[0].attrs["Name"] != "PLEIADES_A" || vars[0].text != spec.env[0].value || vars[1].text != "two" {
		t.Errorf("Variable elements = %+v", vars)
	}
	if wd := findElements(t, doc, "WorkingDirectory"); len(wd) != 1 || wd[0].text != spec.workingDirectory {
		t.Errorf("WorkingDirectory = %+v", wd)
	}
	doc = openShellMessage("u", *winrm.DefaultParameters, shellSpec{noProfile: true}).String()
	if v, _ := option(t, doc, "WINRS_NOPROFILE"); v != "TRUE" {
		t.Errorf("WINRS_NOPROFILE = %q with NoProfile set, want TRUE", v)
	}
	if len(findElements(t, doc, "Environment")) != 0 || len(findElements(t, doc, "WorkingDirectory")) != 0 {
		t.Error("an empty spec must send neither an Environment nor a WorkingDirectory")
	}
}

// FuzzCommandMessage checks, for any line CheckText accepts, that the
// Command message is well-formed and carries exactly that line in exactly
// one element: no line can add structure to the request.
func FuzzCommandMessage(f *testing.F) {
	f.Add("prog ]]> <a>")
	f.Add("]]]]><![CDATA[>")
	f.Add("&amp; &#x0; <!-- -->")
	f.Fuzz(func(t *testing.T, line string) {
		if CheckText("line", line) != nil {
			return
		}
		doc := commandMessage("http://h/wsman", *winrm.DefaultParameters, "S", line, false).String()
		commands := findElements(t, doc, "Command")
		// A carriage return travels as itself, and an XML parser turns a
		// literal one into a line feed, so it is left out of the exact
		// comparison. No command line reaches here with one: transparentLine
		// refuses it, because cmd.exe stops at a line break anyway.
		if strings.ContainsRune(line, '\r') {
			return
		}
		if len(commands) != 1 || commands[0].text != line {
			t.Fatalf("line %q became %q in %d Command elements", line, texts(commands), len(commands))
		}
		if strings.Contains(doc, "&#") {
			t.Fatalf("line %q was sent with a numeric character reference", line)
		}
	})
}

// scriptedPoster stands in for the library's transport: it records every
// message and answers each by action from a script of replies.
type scriptedPoster struct {
	sent    []string
	replies map[string][]reply
}

// reply is one scripted answer: a body, or an error.
type reply struct {
	body string
	err  error
}

// Post implements poster.
func (p *scriptedPoster) Post(_ *winrm.Client, message *soap.SoapMessage) (string, error) {
	doc := message.String()
	p.sent = append(p.sent, doc)
	action := actionOf(doc)
	queue := p.replies[action]
	if len(queue) == 0 {
		return okReply, nil
	}
	next := queue[0]
	p.replies[action] = queue[1:]
	return next.body, next.err
}

// actionOf returns the short name of doc's WS-Addressing action.
func actionOf(doc string) string {
	start := strings.Index(doc, "Action")
	end := strings.Index(doc[start:], "</")
	value := doc[start : start+end]
	return value[strings.LastIndex(value, "/")+1:]
}

// actions lists the short action names of every message sent, in order.
func (p *scriptedPoster) actions() []string {
	out := make([]string, len(p.sent))
	for i, doc := range p.sent {
		out[i] = actionOf(doc)
	}
	return out
}

const (
	okReply     = `<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"><s:Body/></s:Envelope>`
	shellReply  = `<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope" xmlns:a="http://schemas.xmlsoap.org/ws/2004/08/addressing" xmlns:rsp="http://schemas.microsoft.com/wbem/wsman/1/windows/shell"><s:Header><a:Action>http://schemas.xmlsoap.org/ws/2004/09/transfer/CreateResponse</a:Action></s:Header><s:Body><rsp:Shell><rsp:ShellId>SHELL-1</rsp:ShellId></rsp:Shell></s:Body></s:Envelope>`
	cmdReply    = `<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope" xmlns:a="http://schemas.xmlsoap.org/ws/2004/08/addressing" xmlns:rsp="http://schemas.microsoft.com/wbem/wsman/1/windows/shell"><s:Header><a:Action>http://schemas.microsoft.com/wbem/wsman/1/windows/shell/CommandResponse</a:Action></s:Header><s:Body><rsp:CommandResponse><rsp:CommandId>CMD-1</rsp:CommandId></rsp:CommandResponse></s:Body></s:Envelope>`
	outputFmt   = `<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope" xmlns:rsp="http://schemas.microsoft.com/wbem/wsman/1/windows/shell"><s:Body><rsp:ReceiveResponse><rsp:Stream Name="stdout" CommandId="CMD-1">%s</rsp:Stream><rsp:Stream Name="stderr" CommandId="CMD-1">%s</rsp:Stream><rsp:CommandState CommandId="CMD-1" State="http://schemas.microsoft.com/wbem/wsman/1/windows/shell/CommandState/%s">%s</rsp:CommandState></rsp:ReceiveResponse></s:Body></s:Envelope>`
	faultReplyF = `http error 500: <s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"><s:Body><s:Fault><s:Reason><s:Text xml:lang="">%s</s:Text></s:Reason></s:Fault></s:Body></s:Envelope>`
)

// output builds a Receive reply carrying stdout and stderr, done with
// exitCode when exitCode is not negative.
func output(stdout, stderr string, exitCode int) string {
	state, code := "Running", ""
	if exitCode >= 0 {
		state, code = "Done", fmt.Sprintf("<rsp:ExitCode>%d</rsp:ExitCode>", exitCode)
	}
	enc := base64.StdEncoding.EncodeToString
	return fmt.Sprintf(outputFmt, enc([]byte(stdout)), enc([]byte(stderr)), state, code)
}

// newScripted builds an exchange over a scripted poster.
func newScripted(replies map[string][]reply) (*exchange, *scriptedPoster) {
	p := &scriptedPoster{replies: replies}
	return &exchange{transport: p, url: "http://h:5985/wsman", params: *winrm.DefaultParameters}, p
}

func TestExchange_RunsTheWholeConversation(t *testing.T) {
	x, p := newScripted(map[string][]reply{
		"Create":  {{body: shellReply}},
		"Command": {{body: cmdReply}},
		"Receive": {
			{err: errors.New("http error 500: " + timedOutFaultBody(t))},
			{body: output("half ", "", -1)},
			{body: output("done", "warn", 123)},
		},
	})
	res, err := x.run(context.Background(), "prog arg", "", shellSpec{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Stdout != "half done" || res.Stderr != "warn" || res.ExitCode != 123 {
		t.Errorf("result = %+v", res)
	}
	want := "Create Command Send Receive Receive Receive Delete"
	if got := strings.Join(p.actions(), " "); got != want {
		t.Errorf("messages = %s, want %s", got, want)
	}
	// An empty stdin still closes the stream, so a reader sees end of file.
	if sends := findElements(t, p.sent[2], "Stream"); len(sends) != 1 || sends[0].attrs["End"] != "true" {
		t.Errorf("stdin close = %+v", sends)
	}
}

func TestExchange_StdinIsChunkedAndClosedOnce(t *testing.T) {
	stdin := strings.Repeat("0123456789", (stdinChunk*2+100)/10)
	x, p := newScripted(map[string][]reply{
		"Create": {{body: shellReply}}, "Command": {{body: cmdReply}},
		"Receive": {{body: output("", "", 0)}},
	})
	if _, err := x.run(context.Background(), "prog", stdin, shellSpec{}); err != nil {
		t.Fatalf("run: %v", err)
	}
	var got strings.Builder
	var ends []string
	for i, action := range p.actions() {
		if action != "Send" {
			continue
		}
		stream := findElements(t, p.sent[i], "Stream")[0]
		chunk, err := base64.StdEncoding.DecodeString(stream.text)
		if err != nil {
			t.Fatalf("chunk %d: %v", i, err)
		}
		got.Write(chunk)
		ends = append(ends, stream.attrs["End"])
	}
	if got.String() != stdin {
		t.Errorf("stdin arrived as %d bytes, want %d", got.Len(), len(stdin))
	}
	if strings.Join(ends, ",") != ",,true" {
		t.Errorf("End markers = %q, want only the last chunk to close", ends)
	}
}

func TestExchange_FailureBeforeTheCommandIsNotStarted(t *testing.T) {
	x, p := newScripted(map[string][]reply{"Create": {{err: errors.New("dial tcp: connection refused")}}})
	_, err := x.run(context.Background(), "prog", "", shellSpec{})
	var notStarted *NotStartedError
	if !errors.As(err, &notStarted) {
		t.Fatalf("err = %v, want a *NotStartedError", err)
	}
	if got := strings.Join(p.actions(), " "); got != "Create" {
		t.Errorf("messages = %s, want nothing after the failed Create", got)
	}
}

func TestExchange_ARefusedCommandReadsAsItsFault(t *testing.T) {
	x, p := newScripted(map[string][]reply{
		"Create":  {{body: shellReply}},
		"Command": {{err: fmt.Errorf(faultReplyF, "The system cannot find the file specified. ")}},
	})
	_, err := x.run(context.Background(), "nosuch.exe", "", shellSpec{})
	if err == nil || err.Error() != "winrm: starting the command: The system cannot find the file specified." {
		t.Errorf("err = %v", err)
	}
	var notStarted *NotStartedError
	if errors.As(err, &notStarted) {
		t.Error("a refused Command is not a NotStartedError: the retry policy must not treat it as a connection failure")
	}
	if got := strings.Join(p.actions(), " "); got != "Create Command Delete" {
		t.Errorf("messages = %s, want the shell closed after the refusal", got)
	}
}

func TestExchange_CancelTerminatesAndCloses(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	x, p := newScripted(map[string][]reply{"Create": {{body: shellReply}}, "Command": {{body: cmdReply}}})
	_, err := x.run(ctx, "prog", "", shellSpec{})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	if got := strings.Join(p.actions(), " "); got != "Create Command Send Signal Delete" {
		t.Errorf("messages = %s, want the command signaled and the shell closed", got)
	}
}

func TestExchange_AReceiveFailureIsReported(t *testing.T) {
	x, _ := newScripted(map[string][]reply{
		"Create": {{body: shellReply}}, "Command": {{body: cmdReply}},
		"Receive": {{err: errors.New("connection reset by peer")}},
	})
	if _, err := x.run(context.Background(), "prog", "", shellSpec{}); err == nil || !strings.Contains(err.Error(), "receiving output") {
		t.Errorf("err = %v", err)
	}
}

func TestMessageID(t *testing.T) {
	a, b := messageID(), messageID()
	if a == b || !strings.HasPrefix(a, "uuid:") || len(a) != len("uuid:")+36 || a[5+14] != '4' {
		t.Errorf("messageID() = %q, %q: want distinct version 4 UUIDs", a, b)
	}
}

// texts returns each node's text, for a failure message that quotes
// control characters rather than printing them.
func texts(nodes []xmlNode) []string {
	out := make([]string, len(nodes))
	for i, n := range nodes {
		out[i] = n.text
	}
	return out
}
