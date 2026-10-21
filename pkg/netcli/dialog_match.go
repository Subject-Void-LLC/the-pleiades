// How Dialog finds the end of a read: the exec prompt, a listed question,
// or a line shaped like a question, each on the line the device is
// waiting on.
package netcli

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// checkDialog refuses options Dialog could only act on wrongly: a Prompt
// with no pattern, a refusal that also answers, an answer or choice that
// would submit a second line, and a Chooser with nothing to choose from or
// a choice named like one of its own decisions.
func checkDialog(opts DialogOptions) error {
	for i, p := range opts.Prompts {
		switch {
		case p.Match == nil:
			return fmt.Errorf("netcli: prompt %d has no pattern", i)
		case p.Refuse && p.Answer != "":
			return fmt.Errorf("netcli: prompt %d both refuses and answers", i)
		case strings.ContainsAny(p.Answer, "\r\n"):
			return fmt.Errorf("netcli: prompt %d's answer holds a line break, which would submit a second line", i)
		}
	}
	if opts.Ask == nil {
		return nil
	}
	if opts.Ask.Choose == nil || len(opts.Ask.Choices) == 0 {
		return errors.New("netcli: ask needs a chooser and at least one choice")
	}
	if opts.Ask.Max < 0 {
		return errors.New("netcli: ask's max is negative")
	}
	seen := make(map[string]bool, len(opts.Ask.Choices))
	for _, c := range opts.Ask.Choices {
		switch {
		case c.ID == "" || c.ID == ChoiceRefuse || c.ID == ChoiceWait:
			return fmt.Errorf("netcli: a choice's id must be set and not %q or %q", ChoiceRefuse, ChoiceWait)
		case seen[c.ID]:
			return fmt.Errorf("netcli: choice %q is listed twice", c.ID)
		case strings.ContainsAny(c.Answer, "\r\n"):
			return fmt.Errorf("netcli: choice %q's answer holds a line break, which would submit a second line", c.ID)
		}
		seen[c.ID] = true
	}
	return nil
}

// openLine anchors re to the line the device is waiting on: its match
// must sit on the last line of what has been read, with no line break
// after it. A device prints a question and waits with its cursor after
// it, so a question is the one line output leaves open; a finished line of
// output that happens to contain the same words does not match.
func openLine(re *regexp.Regexp) *regexp.Regexp {
	return regexp.MustCompile(`(?:` + re.String() + `)[^\n]*\z`)
}

// askShape is the anchored shape a Chooser's questions are found by, nil
// without an Ask.
func askShape(ask *Ask) *regexp.Regexp {
	if ask == nil {
		return nil
	}
	if ask.Shape != nil {
		return openLine(ask.Shape)
	}
	return openLine(QuestionShape)
}

// dialogPattern is what one read waits for: the exec prompt, any listed
// question, or a question-shaped line. A read must end at whichever comes
// first, since a read that times out closes the session for good.
func dialogPattern(exec *regexp.Regexp, prompts []*regexp.Regexp, shape *regexp.Regexp) *regexp.Regexp {
	parts := []string{`(?:` + exec.String() + `)`}
	for _, p := range prompts {
		parts = append(parts, `(?:`+p.String()+`)`)
	}
	if shape != nil {
		parts = append(parts, `(?:`+shape.String()+`)`)
	}
	return regexp.MustCompile(strings.Join(parts, "|"))
}

// endsWith reports whether re matches at the very end of text.
func endsWith(re *regexp.Regexp, text string) bool {
	locs := re.FindAllStringIndex(text, -1)
	return len(locs) > 0 && locs[len(locs)-1][1] == len(text)
}

// pickPrompt is the first unanswered prompt matching text, or -1. again is
// set when only answered prompts match: the device asked a question
// already answered.
func pickPrompt(prompts []*regexp.Regexp, answered []bool, text string) (int, bool) {
	again := false
	for i, p := range prompts {
		if !p.MatchString(text) {
			continue
		}
		if !answered[i] {
			return i, false
		}
		again = true
	}
	return -1, again
}

// lastLine is the open last line of text, trimmed and bounded: the
// question a device is waiting on.
func lastLine(text string) string {
	line := text[strings.LastIndexByte(text, '\n')+1:]
	line = strings.TrimSpace(strings.ReplaceAll(line, "\r", ""))
	if len(line) > maxQuestionBytes {
		line = line[len(line)-maxQuestionBytes:]
	}
	return line
}

// waitingOn names the question a failed read stopped on, for its error,
// or nothing. Only a line shaped like a question is named: a read that
// stopped partway through other output (a configuration, say) may have
// stopped on a secret.
func waitingOn(partial []byte) string {
	line := lastLine(string(partial))
	if line == "" || !QuestionShape.MatchString(line) {
		return ""
	}
	return fmt.Sprintf("; the device was waiting on %q", line)
}
