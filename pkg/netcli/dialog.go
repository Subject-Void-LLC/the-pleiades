// A command that asks questions before it finishes: copy asks for a
// destination filename, delete and reload ask to confirm. Dialog answers
// the questions its caller listed, stops at the ones the caller refuses,
// and can hand a question nobody listed to a Chooser (a model, through
// pkg/aitask), which picks one of the caller's own answers or stops.
//
// Every answer is one the caller wrote. A Chooser picks among them; it
// never types text of its own, because on a CLI the same keystroke means
// different things at different questions (one Enter accepts a default
// filename and confirms a reload), so only the caller knows what an answer
// does.
package netcli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
)

// Prompt is one question a command may ask, and what to do about it.
type Prompt struct {
	// Match finds the question on the line the device is waiting on: the
	// last line of output, which a question leaves open (no line break
	// after it) because the device's cursor sits there.
	Match *regexp.Regexp

	// Answer is typed and then Enter. Empty is Enter alone.
	Answer string

	// Refuse ends the command at this question instead of answering it:
	// the session is closed, which a device takes as abandoning the
	// command, and Dialog fails naming the question.
	Refuse bool
}

// Choice is one answer a Chooser may pick for a question no Prompt
// matched.
type Choice struct {
	// ID names the choice to the Chooser.
	ID string
	// Answer is typed and then Enter, as Prompt.Answer is.
	Answer string
	// Means says what the answer does, in the caller's words.
	Means string
}

// The two decisions a Chooser may make besides a Choice's ID.
const (
	// ChoiceRefuse stops the command, as a refused Prompt does.
	ChoiceRefuse = "refuse"
	// ChoiceWait says the line is not a question: Dialog reads on.
	ChoiceWait = "wait"
)

// Decision is a Chooser's answer to one question.
type Decision struct {
	// ID is a Choice's ID, ChoiceRefuse or ChoiceWait.
	ID string
	// Reason is why, in the Chooser's words.
	Reason string
	// Detail is whatever else the Chooser reports (its provider, model,
	// confidence, review), kept with the exchange.
	Detail map[string]any
}

// Chooser picks, for question (the line the device is waiting on), one of
// choices, ChoiceRefuse or ChoiceWait. Its error fails the command.
type Chooser func(ctx context.Context, question string, choices []Choice) (Decision, error)

// DefaultAskMax is how many questions a Chooser is asked in one command
// when Ask.Max is zero.
const DefaultAskMax = 3

// QuestionShape is the default Ask.Shape: an open last line ending in a
// question mark, a closing bracket ("[confirm]"), or a bracket and colon
// ("[yes/no]:"). It is how a device's questions end, and how most of its
// output lines do not.
var QuestionShape = regexp.MustCompile(`(?:\?|\]|\]:)[ \t]*\z`)

// UnansweredShape is a question no Prompt answers when a configuration line
// is left waiting on it: an open last line ending in a question mark, in
// one of IOS's answer brackets ("[yes/no]:", "[confirm]", "[y/n]"), or in a
// request for a password or passphrase ("Password:", which crypto pki
// server's "no shutdown" asks after its "[yes/no]:" is answered, to protect
// the CA's key: measured on IOL XE 17.15, 2026-10-24). It is
// narrower than QuestionShape on purpose: a configuration line's output can
// leave "[OK]" open for a moment before its line break ("Building
// configuration...", crypto key generation), and failing on that would
// fail a line that succeeded. A question that matches it fails the line at
// once rather than waiting, unanswered, for the read's bound (the blind CVD
// lab lost five minutes to crypto pki server's "no shutdown" asking
// "Are you sure you want to do this? [yes/no]:", 2026-10-24).
var UnansweredShape = regexp.MustCompile(`(?i)(?:\?|\[(?:yes/no|yes|no|y/n|confirm)\]|\b(?:password|passphrase))[: \t]*\z`)

// Ask is how Dialog handles a question no Prompt matched. Without one,
// such a question is waited on until the context ends.
type Ask struct {
	// Choices are the answers Choose may pick from.
	Choices []Choice
	// Choose picks one.
	Choose Chooser
	// Max bounds how many questions Choose is asked in one command; zero
	// is DefaultAskMax. Past it the command is refused.
	Max int
	// Shape finds an open last line that reads as a question; nil is
	// QuestionShape. Its match must end the output read so far.
	Shape *regexp.Regexp
}

// DialogOptions are what Dialog answers, and how it ends.
type DialogOptions struct {
	// Prompts are the questions the caller expects, in order of
	// preference: the first unanswered one matching is used, and each is
	// answered at most once.
	Prompts []Prompt
	// Ask handles a question no Prompt matches; nil leaves it unanswered.
	Ask *Ask
	// ExpectDisconnect counts the device closing the session after at
	// least one answer as the command's success, which is how a reload
	// ends.
	ExpectDisconnect bool
}

// Exchange is one question the device asked and how it was answered.
type Exchange struct {
	// Question is the line the device waited on.
	Question string
	// Answer is what was typed before Enter.
	Answer string
	// Prompt is the index of the Prompt that answered, -1 for a Chooser.
	Prompt int
	// Choice, Reason and Detail are the Chooser's, when it answered.
	Choice string
	Reason string
	Detail map[string]any
}

// DialogResult is what a dialog came to.
type DialogResult struct {
	// Output is everything the device printed after the echoed command,
	// its questions and the echo of each answer included, without the
	// trailing prompt.
	Output string
	// Exchanges are the questions answered, in order. A Chooser's ChoiceWait
	// is not one.
	Exchanges []Exchange
	// Disconnected is whether the device closed the session after an
	// answer (DialogOptions.ExpectDisconnect).
	Disconnected bool
}

// credentialQuestion is a question asking for a secret. An answer in a
// runbook is never one, so Dialog sends no text to it.
var credentialQuestion = regexp.MustCompile(`(?i)\b(password|passphrase|secret)\b`)

// maxQuestionBytes bounds the question a Chooser is given and an error
// names.
const maxQuestionBytes = 256

// Dialog sends line and reads its output to the exec-mode prompt,
// answering the device's questions on the way as opts says. It fails, and
// closes the session, on a refused question, on a listed question asked
// again after its answer, on a question for a credential that would be
// answered with text, and when the context ends while the device waits on
// a question nobody answered, naming that question.
func (s *Session) Dialog(ctx context.Context, line string, opts DialogOptions) (DialogResult, error) {
	if err := checkDialog(opts); err != nil {
		return DialogResult{}, err
	}
	asked := 0
	return s.converse(ctx, line, s.dialect.Prompt(false), opts, false, &asked)
}

// converse sends line and reads its output to end, the prompt of the mode
// the line leaves the device in, answering the device's questions on the
// way as opts says; asked counts the questions a Chooser has been asked so
// far, so a batch of lines shares one limit. With failFast, a line left
// waiting on a question (UnansweredShape) that no prompt matches and no
// Chooser is there to answer fails at once, naming the question: the
// device would otherwise wait for an answer nothing will type until the
// read's bound ends it.
func (s *Session) converse(ctx context.Context, line string, end *regexp.Regexp, opts DialogOptions, failFast bool, asked *int) (DialogResult, error) {
	prompts := make([]*regexp.Regexp, len(opts.Prompts))
	for i, p := range opts.Prompts {
		prompts[i] = openLine(p.Match)
	}
	shape := askShape(opts.Ask)
	var unanswered *regexp.Regexp
	if failFast && shape == nil {
		unanswered = openLine(UnansweredShape)
	}
	until := dialogPattern(end, prompts, shape, unanswered)

	if err := s.shell.WriteLine(ctx, line); err != nil {
		return DialogResult{}, fmt.Errorf("netcli: %w", err)
	}
	var res DialogResult
	var transcript strings.Builder
	answered := make([]bool, len(opts.Prompts))
	for {
		raw, err := s.shell.ReadUntil(ctx, until, s.maxBytes)
		if err != nil {
			var partial []byte
			var re partialReader
			if errors.As(err, &re) {
				partial = re.PartialRead()
			}
			if opts.ExpectDisconnect && len(res.Exchanges) > 0 && errors.Is(err, io.EOF) {
				transcript.Write(partial)
				res.Output, res.Disconnected = stripEcho(transcript.String(), line, end), true
				return res, nil
			}
			return res, fmt.Errorf("netcli: %w%s", err, waitingOn(partial))
		}
		transcript.WriteString(raw)
		if endsWith(end, raw) {
			res.Output = stripEcho(transcript.String(), line, end)
			return res, nil
		}
		if echoing(transcript.String(), line) {
			// The device's echo of line itself, not yet ended by its line
			// break: a line that ends in "?" reads as a question until then.
			continue
		}
		question := lastLine(raw)
		if i, again := pickPrompt(prompts, answered, raw); i >= 0 || again {
			if again {
				return res, s.stop(question, "the device asked %q again after it was answered, so the answer did not take", question)
			}
			if opts.Prompts[i].Refuse {
				return res, s.stop(question, "the device asked %q, which this task refuses", question)
			}
			if err := s.answer(ctx, question, opts.Prompts[i].Answer); err != nil {
				return res, err
			}
			answered[i] = true
			res.Exchanges = append(res.Exchanges, Exchange{Question: question, Answer: opts.Prompts[i].Answer, Prompt: i})
			continue
		}
		if unanswered != nil && endsWith(unanswered, raw) {
			return res, s.stop(question, "the device asked %q, which no prompt answers: list it in prompts with its answer (or refuse: true), or let ask choose among answers the task writes", question)
		}
		if shape == nil || !endsWith(shape, raw) {
			// The pattern matched something none of its parts recognizes
			// at the end; read on rather than guess.
			continue
		}
		ex, done, err := s.choose(ctx, opts.Ask, question, asked)
		if err != nil {
			return res, err
		}
		if done {
			res.Exchanges = append(res.Exchanges, ex)
		}
	}
}

// echoing reports whether everything read so far is the device's echo of
// line, or the start of it, its line break not yet arrived: an echo can
// arrive in pieces, and "service password" is a prefix of a line, not a
// question.
func echoing(read, line string) bool {
	read = strings.TrimRight(read, " \t")
	return read != "" && strings.HasPrefix(line, read)
}

// choose asks the Chooser about question and acts on its decision: an
// answer typed (done), a wait (not done), or the command stopped.
func (s *Session) choose(ctx context.Context, ask *Ask, question string, asked *int) (Exchange, bool, error) {
	limit := ask.Max
	if limit <= 0 {
		limit = DefaultAskMax
	}
	if *asked >= limit {
		return Exchange{}, false, s.stop(question, "the device asked %q, and the %d questions this task lets a chooser answer are used", question, limit)
	}
	*asked++
	d, err := ask.Choose(ctx, question, ask.Choices)
	if err != nil {
		left := s.abandon(question)
		return Exchange{}, false, fmt.Errorf("netcli: choosing an answer to %q: %w; %s", question, err, left)
	}
	switch d.ID {
	case ChoiceWait:
		return Exchange{}, false, nil
	case ChoiceRefuse:
		return Exchange{}, false, s.stop(question, "the device asked %q, and the chooser refused it: %s", question, d.Reason)
	}
	for _, c := range ask.Choices {
		if c.ID == d.ID {
			if err := s.answer(ctx, question, c.Answer); err != nil {
				return Exchange{}, false, err
			}
			return Exchange{Question: question, Answer: c.Answer, Prompt: -1, Choice: c.ID, Reason: d.Reason, Detail: d.Detail}, true, nil
		}
	}
	return Exchange{}, false, s.stop(question, "the chooser picked %q for %q, which is not one of this task's choices", d.ID, question)
}

// answer types text and Enter at question, refusing text for a question
// that asks for a credential.
func (s *Session) answer(ctx context.Context, question, text string) error {
	if text != "" && credentialQuestion.MatchString(question) {
		return s.stop(question, "the device asked %q for a credential, and an answer written in a task is never a secret, so none is sent", question)
	}
	if err := s.shell.WriteLine(ctx, text); err != nil {
		return fmt.Errorf("netcli: answering %q: %w", question, err)
	}
	return nil
}

// Abandoner is a Shell whose device keeps a question open after the shell
// closes: a console line, whose node outlives the connection to its console
// server, so closing it abandons nothing, and whatever its Close types next
// would answer the question. Session closes such a shell only after
// Abandon, which declines the question itself where a decline is certain,
// or leaves the line alone; it reports whether it declined.
type Abandoner interface {
	Abandon(question string) (declined bool)
}

// stop ends the command at question, whose reason format and args give,
// and returns that reason as an error saying what became of the command.
func (s *Session) stop(question, format string, args ...any) error {
	return fmt.Errorf("netcli: "+format+"; %s", append(args, s.abandon(question))...)
}

// abandon closes the session at question and says what became of the
// command. Closing a session abandons it, except on an Abandoner, which
// declines the question first or reports the line left waiting on it.
func (s *Session) abandon(question string) string {
	a, ok := s.shell.(Abandoner)
	if !ok {
		_ = s.shell.Close()
		return "the session was closed, which abandons the command"
	}
	declined := a.Abandon(question)
	_ = s.shell.Close()
	if declined {
		return "the question was declined and the session closed"
	}
	return fmt.Sprintf("the line was left waiting at %q, unanswered: clear it at the device's console", question)
}
