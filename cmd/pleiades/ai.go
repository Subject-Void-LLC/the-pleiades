// Package main: the `pleiades ai` commands, the parts of the ai collection
// that need a person rather than a runbook: signing prompts, and deciding
// answers a run held for review.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/clispec"
	"github.com/Subject-Void-LLC/the-pleiades/internal/termsafe"
)

// aiCommands is the `pleiades ai` family.
var aiCommands = map[string]commandFunc{
	"prompt": runAIPrompt,
	"review": runAIReview,
}

// runAI dispatches the ai command family.
func runAI(args []string) error {
	return dispatch("ai", args, aiCommands)
}

// runAIPrompt dispatches `pleiades ai prompt`.
func runAIPrompt(args []string) error {
	return dispatch("ai prompt", args, map[string]commandFunc{"keygen": runAIPromptKeygen, "sign": runAIPromptSign})
}

// runAIReview dispatches `pleiades ai review`.
func runAIReview(args []string) error {
	return dispatch("ai review", args, map[string]commandFunc{
		"list": runAIReviewList, "show": runAIReviewShow, "approve": runAIReviewApprove, "deny": runAIReviewDeny,
	})
}

// dispatch runs the subcommand of path named by args[0], printing path's
// usage for help or an unknown command.
func dispatch(path string, args []string, commands map[string]commandFunc) error {
	usage := func() {
		spec := clispec.Root
		for _, name := range strings.Fields(path) {
			spec, _ = clispec.Find(spec, name)
		}
		fmt.Fprintf(os.Stderr, "usage: pleiades %s <command> [flags]\n\ncommands:\n", path)
		fmt.Fprint(os.Stderr, clispec.RenderList(spec.Subcommands))
	}
	if len(args) == 0 {
		usage()
		return errUnknownCommand
	}
	switch args[0] {
	case "-h", "--help", "help":
		usage()
		return nil
	}
	cmd, ok := commands[args[0]]
	if !ok {
		fmt.Fprintf(os.Stderr, "pleiades %s: unknown command %q\n", path, args[0])
		usage()
		return errUnknownCommand
	}
	return cmd(args[1:])
}

// printEscapedBlock prints text line by line, each escaped for the
// terminal and indented, so an answer's own escape sequences or line
// tricks cannot act on the terminal or pass for this program's output.
func printEscapedBlock(w *os.File, text string) {
	for _, line := range strings.Split(text, "\n") {
		fmt.Fprintf(w, "    | %s\n", termsafe.EscapeLine(line))
	}
}

// insideDir reports whether path is dir or under it.
func insideDir(path, dir string) bool {
	absPath, err1 := filepath.Abs(path)
	absDir, err2 := filepath.Abs(dir)
	if err1 != nil || err2 != nil {
		return true
	}
	rel, err := filepath.Rel(absDir, absPath)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
