// Package main: `pleiades ai review`, which lists, shows and decides the
// answers a run held for a person (internal/aireview), and the terminal
// prompter a run asks with when a person is at it. Deciding needs a
// person: an agent or a pipe approving a held answer would defeat holding
// it.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/user"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/aireview"
	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/prompt"
	"github.com/Subject-Void-LLC/the-pleiades/internal/termsafe"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// minKeyPrefix is the shortest key prefix a review may be named by.
const minKeyPrefix = 8

// reviewFlags parses a review command's flags: --dir, and --json where
// the command prints. A command naming a review takes its key anywhere
// among them, and returns it as the one element of rest.
func reviewFlags(name string, args []string, withJSON bool) (dir string, asJSON bool, comment string, rest []string, err error) {
	if name != "list" {
		key, flags, err := splitPositional(args, map[string]bool{"json": true})
		if err != nil {
			return "", false, "", nil, err
		}
		d, j, c, _, err := reviewFlags("list", flags, withJSON)
		return d, j, c, []string{key}, err
	}
	fs := flag.NewFlagSet("ai review "+name, flag.ContinueOnError)
	d := fs.String("dir", ".", "project directory")
	var j *bool
	if withJSON {
		j = fs.Bool("json", false, "print JSON")
	}
	c := fs.String("comment", "", "why, recorded with the decision")
	if err := fs.Parse(args); err != nil {
		return "", false, "", nil, err
	}
	if j != nil {
		asJSON = *j
	}
	return *d, asJSON, *c, fs.Args(), nil
}

// resolveKey finds the review a full key or a unique prefix names.
func resolveKey(dir, named string) (string, error) {
	if aireview.ValidKey(named) {
		return named, nil
	}
	if len(named) < minKeyPrefix || strings.Trim(named, "0123456789abcdef") != "" {
		return "", fmt.Errorf("%q is not a review key, or a prefix of one of at least %d characters", named, minKeyPrefix)
	}
	reviews, _, err := aireview.List(dir)
	if err != nil {
		return "", err
	}
	var found []string
	for _, r := range reviews {
		if strings.HasPrefix(r.Key, named) {
			found = append(found, r.Key)
		}
	}
	switch len(found) {
	case 0:
		return "", fmt.Errorf("no review's key begins %s", named)
	case 1:
		return found[0], nil
	}
	return "", fmt.Errorf("%d reviews' keys begin %s; give more of it", len(found), named)
}

// runAIReviewList lists every review, newest held first.
func runAIReviewList(args []string) error {
	dir, asJSON, _, _, err := reviewFlags("list", args, true)
	if err != nil {
		return err
	}
	reviews, broken, err := aireview.List(dir)
	if err != nil {
		return err
	}
	if asJSON {
		out := make([]map[string]any, 0, len(reviews))
		for _, r := range reviews {
			out = append(out, map[string]any{
				"key": r.Key, "status": r.Status, "method": r.Method, "provider": r.Provider, "model": r.Model,
				"reasons": r.Reasons, "held_at": r.HeldAt, "decided_by": r.DecidedBy, "decided_at": r.DecidedAt, "answer_sha256": r.AnswerSHA256,
			})
		}
		errs := map[string]string{}
		for k, e := range broken {
			errs[k] = e.Error()
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"reviews": out, "unreadable": errs})
	}
	if len(reviews) == 0 && len(broken) == 0 {
		fmt.Println("no reviews")
		return nil
	}
	for _, r := range reviews {
		fmt.Printf("%s  %-8s  %s %s/%s  held %s  %s\n", r.Key[:12], termsafe.EscapeLine(string(r.Status)), termsafe.EscapeLine(r.Method), termsafe.EscapeLine(r.Provider), termsafe.EscapeLine(r.Model), termsafe.EscapeLine(r.HeldAt), termsafe.EscapeLine(strings.Join(r.Reasons, "; ")))
	}
	for k, e := range broken {
		fmt.Printf("%s  UNREADABLE  %s\n", k[:12], termsafe.EscapeLine(e.Error()))
	}
	return nil
}

// runAIReviewShow prints one review whole, its answer escaped.
func runAIReviewShow(args []string) error {
	dir, asJSON, _, rest, err := reviewFlags("show", args, true)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return errors.New("usage: pleiades ai review show <key> [--json] [--dir .]")
	}
	key, err := resolveKey(dir, rest[0])
	if err != nil {
		return err
	}
	r, ok, err := aireview.Load(dir, key)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("there is no review %s", key)
	}
	if asJSON {
		return json.NewEncoder(os.Stdout).Encode(r)
	}
	printReview(os.Stdout, r)
	return nil
}

// printReview prints what a person decides on: what was asked of whom,
// why it was held, what it cost, and the answer, every value escaped.
func printReview(w *os.File, r sdk.Review) {
	fmt.Fprintf(w, "review %s: %s\n", termsafe.EscapeLine(r.Key), termsafe.EscapeLine(string(r.Status)))
	fmt.Fprintf(w, "%s to %s, model %s, held %s\n", termsafe.EscapeLine(r.Method), termsafe.EscapeLine(r.Provider), termsafe.EscapeLine(r.Model), termsafe.EscapeLine(r.HeldAt))
	if cost, ok := r.Usage["cost"]; ok {
		fmt.Fprintf(w, "cost %s %s\n", termsafe.EscapeLine(fmt.Sprint(cost)), termsafe.EscapeLine(fmt.Sprint(r.Usage["currency"])))
	}
	fmt.Fprintln(w, "held because:")
	for _, why := range r.Reasons {
		fmt.Fprintf(w, "  - %s\n", termsafe.EscapeLine(why))
	}
	if r.DecidedBy != "" {
		fmt.Fprintf(w, "%s by %s at %s %s\n", termsafe.EscapeLine(string(r.Status)), termsafe.EscapeLine(r.DecidedBy), termsafe.EscapeLine(r.DecidedAt), termsafe.EscapeLine(r.Comment))
	}
	fmt.Fprintf(w, "answer (sha256 %s):\n", r.AnswerSHA256)
	printEscapedBlock(w, r.Answer)
}

// runAIReviewApprove approves a held answer.
func runAIReviewApprove(args []string) error {
	return decideReview("approve", sdk.ReviewApproved, args)
}

// runAIReviewDeny denies a held answer.
func runAIReviewDeny(args []string) error { return decideReview("deny", sdk.ReviewDenied, args) }

// decideReview shows a pending review to the person at the terminal and
// records their decision, bound to the answer they were shown.
func decideReview(verb string, status sdk.ReviewStatus, args []string) error {
	dir, _, comment, rest, err := reviewFlags(verb, args, false)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return fmt.Errorf("usage: pleiades ai review %s <key> [--comment text] [--dir .]", verb)
	}
	term, err := prompt.OpenPerson("deciding a held answer")
	if err != nil {
		return err
	}
	defer func() { _ = term.Restore() }()
	key, err := resolveKey(dir, rest[0])
	if err != nil {
		return err
	}
	r, ok, err := aireview.Load(dir, key)
	switch {
	case err != nil:
		return err
	case !ok:
		return fmt.Errorf("there is no review %s", key)
	case r.Status != sdk.ReviewPending:
		return fmt.Errorf("review %s was already %s by %s at %s", key, termsafe.EscapeLine(string(r.Status)), termsafe.EscapeLine(r.DecidedBy), termsafe.EscapeLine(r.DecidedAt))
	}
	w := term.Out().(*os.File)
	printReview(w, r)
	answer, err := term.Line(fmt.Sprintf("\n%s this answer? type %s: ", verb, verb))
	if err != nil {
		return err
	}
	if strings.TrimSpace(answer) != verb {
		return errors.New("nothing decided")
	}
	if _, err := aireview.Decide(dir, key, status, decider(), time.Now().UTC().Format(time.RFC3339), comment, r.AnswerSHA256); err != nil {
		return err
	}
	fmt.Printf("review %s %s; run the runbook again to continue it\n", key[:12], status)
	return nil
}

// decider names the person deciding: their login on this machine.
func decider() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	return "unknown"
}

// errHoldForLater is a person at the terminal choosing not to decide now.
var errHoldForLater = errors.New("held for later")

// terminalReviewer asks the person at a run's terminal to decide each
// answer the run holds (engine.ReviewPrompter).
type terminalReviewer struct{ term *prompt.Terminal }

// Ask implements engine.ReviewPrompter.
func (t terminalReviewer) Ask(_ context.Context, r sdk.Review) (bool, string, string, error) {
	w := t.term.Out().(*os.File)
	fmt.Fprintln(w)
	printReview(w, r)
	answer, err := t.term.Line("\napprove, deny, or hold for later? [hold]: ")
	if err != nil {
		return false, "", "", err
	}
	switch strings.TrimSpace(answer) {
	case "approve":
		return true, decider(), "approved at the terminal during the run", nil
	case "deny":
		return false, decider(), "denied at the terminal during the run", nil
	}
	return false, "", "", errHoldForLater
}

// runReviewPrompter is the prompter a run asks with: the terminal's, when
// a person is at it and no agent drives the run; none otherwise, which
// keeps every held answer pending.
func runReviewPrompter() engine.ReviewPrompter {
	if _, agent := prompt.AgentCaller(); agent {
		return nil
	}
	term, err := prompt.OpenTerminal()
	if err != nil {
		return nil
	}
	return terminalReviewer{term: term}
}
