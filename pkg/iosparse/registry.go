// The command registry, Parse and Commands.
package iosparse

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// ErrEmpty reports output a parser found nothing in, which Genie refuses
// with SchemaEmptyParserError. A caller tests for it with errors.Is.
var ErrEmpty = errors.New("parser found nothing in the output (genie's SchemaEmptyParserError)")

// UnsupportedCommandError reports a command no ported parser covers.
type UnsupportedCommandError struct {
	// Command is the command as given.
	Command string
	// Reason says why, when the command is close to a covered one.
	Reason string
}

// Error implements error, naming every command that is covered.
func (e *UnsupportedCommandError) Error() string {
	msg := fmt.Sprintf("no parser covers %q", e.Command)
	if e.Reason != "" {
		msg += ": " + e.Reason
	}
	return msg + "; covered commands: " + strings.Join(Commands(), ", ")
}

// ParserError reports output the parser failed on, where genieparser's
// own parser raises a Python exception (a KeyError, a ValueError) on the
// same output rather than returning a result.
type ParserError struct {
	// Parser is the genieparser class.
	Parser string
	// Exception is the Python exception genieparser raises.
	Exception string
	// Message says what was missing or malformed.
	Message string
}

// Error implements error.
func (e *ParserError) Error() string {
	if e.Exception == portLimit {
		return fmt.Sprintf("%s refused this output: %s", e.Parser, e.Message)
	}
	return fmt.Sprintf("%s failed on this output (genie raises %s: %s)", e.Parser, e.Exception, e.Message)
}

// parserDef is one ported genieparser class.
type parserDef struct {
	// name is the genieparser class name.
	name string
	// parse is the port of the class's cli method: it takes the output
	// and the command's arguments and returns the parsed dict.
	parse func(out string, args map[string]string) *dict
	// schema is the class's schema, checked on every result.
	schema schema
}

// form is one command form a parser covers.
type form struct {
	// text is the form as genieparser writes it, such as
	// "show ip route vrf {vrf}".
	text string
	// re matches a whole command of this form, capturing each argument
	// under its name.
	re *regexp.Regexp
	// def is the parser the form runs.
	def *parserDef
	// refuse, when set, returns why a command of this form cannot be
	// parsed from its output alone, or "".
	refuse func(args map[string]string) string
}

// forms is every covered command form, in the order a command is tried
// against them: a more specific form comes before a general one.
var forms []form

// register adds a parser's forms. Each pattern is a form's text with each
// "{name}" replaced by the expression its argument must match.
func register(def *parserDef, refuse func(map[string]string) string, patterns ...[2]string) {
	for _, p := range patterns {
		forms = append(forms, form{
			text:   p[0],
			re:     regexp.MustCompile(`(?i)^` + p[1] + `$`),
			def:    def,
			refuse: refuse,
		})
	}
}

// Commands returns every covered command form, sorted, with each argument
// written as genieparser writes it, such as "show ip route vrf {vrf}".
func Commands() []string {
	out := make([]string, 0, len(forms))
	for _, f := range forms {
		out = append(out, f.text)
	}
	sort.Strings(out)
	return out
}

// Parse runs the ported genieparser parser that covers command on output
// and returns the parser's class name and its result, in genieparser's
// own shape: nested maps keyed by string, ints as int64, booleans as bool,
// lists as []any, and None as nil.
//
// output is the device's output with or without "\r\n" line ends. A
// command no parser covers is refused with an *UnsupportedCommandError
// before output is read. Output with nothing in it, or nothing the parser
// recognizes, is ErrEmpty. Output the genieparser parser itself fails on
// is a *ParserError, and a result its schema refuses is a *SchemaError.
func Parse(command, output string) (parser string, parsed map[string]any, err error) {
	f, args, err := lookup(command)
	if err != nil {
		return "", nil, err
	}
	name := f.def.name
	out := universalNewlines(output)
	if strip(out) == "" {
		return name, nil, fmt.Errorf("%s: %w", name, ErrEmpty)
	}
	result, err := run(f.def, out, args)
	if err != nil {
		return name, nil, err
	}
	if result.size() == 0 {
		return name, nil, fmt.Errorf("%s: %w", name, ErrEmpty)
	}
	if err := check(f.def.schema, result, nil); err != nil {
		var se *SchemaError
		if errors.As(err, &se) {
			se.Parser = name
		}
		return name, nil, err
	}
	return name, toPlain(result).(map[string]any), nil
}

// Covers reports which parser Parse would run for command, without any
// output: the class name, or the same *UnsupportedCommandError Parse
// returns. A caller uses it to refuse a command before reaching a device.
func Covers(command string) (parser string, err error) {
	f, _, err := lookup(command)
	if err != nil {
		return "", err
	}
	return f.def.name, nil
}

// lookup finds the form covering command and its arguments.
func lookup(command string) (form, map[string]string, error) {
	cmd := strings.TrimSpace(command)
	for _, f := range forms {
		m := f.re.FindStringSubmatch(cmd)
		if m == nil {
			continue
		}
		args := map[string]string{}
		for i, name := range f.re.SubexpNames() {
			if name != "" {
				args[name] = m[i]
			}
		}
		if f.refuse != nil {
			if reason := f.refuse(args); reason != "" {
				return form{}, nil, &UnsupportedCommandError{Command: command, Reason: reason}
			}
		}
		return f, args, nil
	}
	return form{}, nil, &UnsupportedCommandError{Command: command}
}

// run calls a parser, turning a Python exception it raises into a
// *ParserError.
func run(def *parserDef, out string, args map[string]string) (result *dict, err error) {
	defer func() {
		if r := recover(); r != nil {
			pe, ok := r.(*pyErr)
			if !ok {
				panic(r)
			}
			err = &ParserError{Parser: def.name, Exception: pe.kind, Message: pe.msg}
		}
	}()
	return def.parse(out, args), nil
}
