// Package main: how a command's arguments are split into its positional
// arguments and its flags.
//
// The stdlib flag package stops parsing at the first token that is not a
// flag, which would reject PLAN.md Section 7's own documented usage,
// `add-host webserver1 --type linux`, where the name comes first. So the
// positionals are pulled out wherever they sit and only the flag tokens
// reach flag.FlagSet.Parse. Telling a flag's value from a positional needs
// to know which flags are boolean (a bare `-flag` takes no value), and
// that is read from the command's own FlagSet, the same way the flag
// package itself decides it, so it cannot disagree with the flags the
// command really has.
package main

import (
	"errors"
	"flag"
	"fmt"
	"strings"
)

// errMissingPositional is parseOneArg's error for args with no positional
// argument, so a command whose positional is optional can tell that case
// from a real parse error.
var errMissingPositional = errors.New("missing positional argument")

// errExtraPositional is what parseOneArg's error matches when args hold
// more than the one positional argument the command takes.
var errExtraPositional = errors.New("unexpected extra argument")

// parsedHook, when a test sets it, is called by parseArgs with each
// command's FlagSet and positionals once its flags have parsed, and a
// non-nil error it returns is returned in place of the positionals. A test
// uses it to read how a real command splits its real arguments, then stop
// the command before it does anything else. It is nil outside tests.
var parsedHook func(fs *flag.FlagSet, positionals []string) error

// parseArgs parses args for a command whose flags are declared on fs. The
// positionals may come before, between or after the flags, and are
// returned in the order given. A flag error (an unknown flag, a bad value,
// -h) is returned before anything about the positionals is decided, so the
// command reports what is really wrong with its arguments.
func parseArgs(fs *flag.FlagSet, args []string) ([]string, error) {
	positionals, rest := splitArgs(fs, args)
	if err := fs.Parse(rest); err != nil {
		return nil, err
	}
	if parsedHook != nil {
		if err := parsedHook(fs, positionals); err != nil {
			return nil, err
		}
	}
	return positionals, nil
}

// parseOneArg is parseArgs for a command that takes exactly one positional
// argument (add-host's host name, forge new-device's vendor). Its error is
// a flag error, or one matching errMissingPositional or errExtraPositional.
func parseOneArg(fs *flag.FlagSet, args []string) (string, error) {
	positionals, err := parseArgs(fs, args)
	if err != nil {
		return "", err
	}
	switch len(positionals) {
	case 0:
		return "", errMissingPositional
	case 1:
		return positionals[0], nil
	}
	return "", fmt.Errorf("%w: %q", errExtraPositional, positionals[1])
}

// isPositionalCountErr reports whether err is parseOneArg's error for the
// wrong number of positional arguments, rather than a flag error.
func isPositionalCountErr(err error) bool {
	return errors.Is(err, errMissingPositional) || errors.Is(err, errExtraPositional)
}

// splitArgs returns every positional token in args, in order, and the flag
// tokens with their values for fs.Parse. A token starting with "-" (but
// not a lone "-", which names standard input or no hosts) is a flag. A
// flag written "--flag=value" carries its value; a bare "--flag" takes the
// next token as its value unless fs declares it boolean.
func splitArgs(fs *flag.FlagSet, args []string) (positionals, rest []string) {
	rest = make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") || a == "-" {
			positionals = append(positionals, a)
			continue
		}
		rest = append(rest, a)
		name, _, hasInlineValue := strings.Cut(strings.TrimLeft(a, "-"), "=")
		// A flag fs does not define takes the next token too; fs.Parse
		// then refuses it, which is the error the command reports.
		if !hasInlineValue && !isBoolFlag(fs, name) && i+1 < len(args) {
			i++
			rest = append(rest, args[i])
		}
	}
	return positionals, rest
}

// isBoolFlag reports whether fs declares name as a boolean flag, one whose
// bare form takes no value. It asks the flag's Value, as the flag package
// itself does, so fs.Bool, fs.BoolVar, fs.BoolFunc and any flag.Value with
// an IsBoolFlag method all count.
func isBoolFlag(fs *flag.FlagSet, name string) bool {
	f := fs.Lookup(name)
	return f != nil && boolValue(f.Value)
}

// boolValue reports whether v is a boolean flag's value: one with an
// IsBoolFlag method that returns true.
func boolValue(v flag.Value) bool {
	b, ok := v.(interface{ IsBoolFlag() bool })
	return ok && b.IsBoolFlag()
}
