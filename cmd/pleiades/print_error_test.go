// Tests the second layer of escaping on its own: what main prints for a
// command's error and for a help request's answer.
package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// TestPrintError_EscapesWhatATerminalActsOn is the second layer alone: an
// error carrying a raw ANSI escape and a raw right-to-left override,
// printed as a command's error and as a help request's answer, reaches
// stderr with both escaped.
func TestPrintError_EscapesWhatATerminalActsOn(t *testing.T) {
	err := errors.New("usage: pleiades thing <name>: sw\x1b[2Jevil\u202eflip")
	for name, write := range map[string]func(error){
		"printError":      printError,
		"printHelpAnswer": printHelpAnswer,
	} {
		stderr := captureStderr(t, func() { write(fmt.Errorf("%w: %w", err, errMissingPositional)) })
		for _, raw := range []string{"\x1b", "\u202e"} {
			if strings.Contains(stderr, raw) {
				t.Errorf("%s wrote %q raw:\n%q", name, raw, stderr)
			}
		}
		if !strings.Contains(stderr, `sw\x1b[2Jevil\u202eflip`) {
			t.Errorf("%s did not show the text escaped:\n%s", name, stderr)
		}
	}
}
