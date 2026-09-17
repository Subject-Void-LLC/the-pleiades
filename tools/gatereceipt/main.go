// Command gatereceipt records that a local gate passed at one commit, and
// checks that record before a push.
//
// It exists because the gate and the push were one action and should not
// have been. `.githooks/pre-push` used to run `make push-gate` inline, and
// git opens its connection to the remote BEFORE running that hook: it
// needs the remote's ref advertisement to build the hook's stdin. A gate
// that takes twenty minutes therefore leaves git holding a connection that
// is long dead by the time it has anything to upload, and the push dies
// writing to it with SIGPIPE, exit 141, and not one word of output. That
// failure was reproduced five times on this repository before the cause
// was found, and it is invisible: the gate prints "all checks passed" and
// nothing reaches the remote.
//
// So the gate becomes a process a developer runs on its own schedule, and
// the hook becomes a question with an instant answer: has this exact
// commit passed. The evidence is a receipt this command writes on success
// and reads back at push time.
//
// # This is stricter than what it replaces, not looser
//
// A hook that runs the suite proves something about the WORKING TREE, and
// then pushes COMMITS. Those are not the same thing. A developer with
// uncommitted edits got a green gate describing code that was not what
// they were sending, and nothing anywhere noticed. A receipt is bound to a
// commit and is only issued from a clean tree, so the thing verified and
// the thing pushed are the same object by construction.
//
// # What a receipt cannot tell you
//
// Every check `make ci` runs is a pure function of the tree except one.
// `govulncheck` queries a live advisory database, so a receipt written a
// week ago says a commit had no known vulnerabilities A WEEK AGO, which is
// a different claim from the one a reader will take it for. That is what
// the age bound is for, and it is the only reason there is one: a commit
// hash cannot express "an advisory was published yesterday".
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// receiptName is the file, inside .git rather than the working tree.
//
// Inside .git deliberately. A receipt in the working tree would be a file
// the gate creates, which dirties the tree whose cleanliness the receipt
// is asserting: the act of recording the result would falsify it. It is
// also per-clone rather than shared, which is correct, because it records
// what THIS machine ran.
const receiptName = "pleiades-gate.json"

// MaxReceiptAge bounds how long a pass counts for.
//
// One day, and the number is govulncheck's rather than a guess about
// developer habits. Everything else a gate checks is decided entirely by
// the tree and stays true for as long as the commit does; only the
// vulnerability database moves underneath a green result. A day is short
// enough that an advisory published against a dependency is caught on the
// next push and long enough that a normal day's work does not re-run a
// twenty minute suite to send two commits.
const MaxReceiptAge = 24 * time.Hour

// Receipt is what one passing gate run leaves behind.
type Receipt struct {
	// Commit is the revision the gate ran against, which is HEAD at the
	// moment of writing and, because the tree was clean, is also exactly
	// what was on disk.
	Commit string `json:"commit"`

	// Target names which gate ran, and it is recorded rather than assumed
	// because the two are not the same evidence. `ci` is strict;
	// `push-gate` tolerates a test failure confined to a package
	// flaky-packages.json names. A reader who cannot tell them apart will
	// eventually read a tolerated pass as a strict one.
	Target string `json:"target"`

	// WrittenAt is when the gate finished, in UTC.
	WrittenAt time.Time `json:"written_at"`
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "write":
		err = runWrite(os.Args[2:])
	case "verify":
		err = runVerify(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `Usage: gatereceipt <command> [flags]

  write  --target <name>    record that the gate passed at HEAD
  verify --commit <sha>     check that a valid receipt exists for a commit

write refuses a dirty tree, because a receipt names a commit and a dirty
tree means the gate ran against something else.
`)
}

// runWrite records a pass, refusing when the tree does not match HEAD.
func runWrite(args []string) error {
	fs := flag.NewFlagSet("write", flag.ContinueOnError)
	target := fs.String("target", "", "which gate ran, for example push-gate or ci")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*target) == "" {
		return errors.New("gatereceipt write: --target is required, so a reader can tell a strict pass from a tolerant one")
	}

	dirty, err := workingTreeDirty()
	if err != nil {
		return err
	}
	if dirty {
		// Not an error the gate should have to recover from, and not a
		// warning either. A receipt says "this commit passed"; issuing
		// one from a dirty tree would make that sentence false, and the
		// whole point of the receipt is that it is not.
		return errors.New("gatereceipt write: the working tree has uncommitted changes, so this run proved nothing about any commit and no receipt was written")
	}

	head, err := headCommit()
	if err != nil {
		return err
	}
	dir, err := gitDir()
	if err != nil {
		return err
	}

	body, err := json.MarshalIndent(Receipt{
		Commit:    head,
		Target:    *target,
		WrittenAt: time.Now().UTC(),
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("gatereceipt write: encoding the receipt: %w", err)
	}
	path := filepath.Join(dir, receiptName)
	if err := os.WriteFile(path, append(body, '\n'), 0o600); err != nil {
		return fmt.Errorf("gatereceipt write: %w", err)
	}
	fmt.Printf("gatereceipt: %s passed at %s; a push of that commit is allowed for %s\n",
		*target, short(head), MaxReceiptAge)
	return nil
}

// runVerify answers whether one commit may be pushed.
func runVerify(args []string) error {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	commit := fs.String("commit", "", "the commit being pushed")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*commit) == "" {
		return errors.New("gatereceipt verify: --commit is required")
	}

	dir, err := gitDir()
	if err != nil {
		return err
	}
	path := filepath.Join(dir, receiptName)

	body, err := os.ReadFile(path) // #nosec G304 -- path is .git/<constant>, derived from git itself
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("no gate receipt: nothing has verified %s on this machine.\nRun `make push-gate` (or `make ci`), then push again", short(*commit))
	}
	if err != nil {
		return fmt.Errorf("gatereceipt verify: reading %s: %w", path, err)
	}

	var r Receipt
	if err := json.Unmarshal(body, &r); err != nil {
		// A receipt nobody can read is not a pass. Say which file, so the
		// fix is obvious rather than mysterious.
		return fmt.Errorf("the gate receipt at %s is unreadable (%v), so it proves nothing.\nRun `make push-gate`, then push again", path, err)
	}

	if r.Commit != *commit {
		return fmt.Errorf("the gate receipt is for %s, but this push sends %s.\nThose are different commits, so the receipt says nothing about what is being pushed.\nRun `make push-gate`, then push again",
			short(r.Commit), short(*commit))
	}
	if age := time.Since(r.WrittenAt); age > MaxReceiptAge {
		// Named separately from a commit mismatch, because the fix is the
		// same command for a completely different reason and a reader who
		// conflates them will not understand why a commit that passed is
		// being refused.
		return fmt.Errorf("the gate receipt for %s is %s old, past the %s bound.\nEverything a gate checks is fixed by the commit except govulncheck, which reads a live advisory database, so an old pass no longer answers that one question.\nRun `make push-gate`, then push again",
			short(*commit), age.Round(time.Minute), MaxReceiptAge)
	}

	fmt.Printf("pre-push: %s passed %s here %s ago\n", short(*commit), r.Target, time.Since(r.WrittenAt).Round(time.Minute))
	return nil
}

// workingTreeDirty reports whether anything is uncommitted, tracked or not.
//
// --porcelain rather than a diff, so an untracked file counts: a new file
// the gate compiled is part of what it tested and is not part of HEAD.
func workingTreeDirty() (bool, error) {
	out, err := exec.Command("git", "status", "--porcelain").Output()
	if err != nil {
		return false, fmt.Errorf("gatereceipt: git status: %w", err)
	}
	return len(strings.TrimSpace(string(out))) > 0, nil
}

// headCommit is the revision a receipt names.
//
// HEAD is a literal rather than a parameter, and deliberately so: this
// command resolves exactly one revision, and taking it as a variable is
// both a gosec G204 finding and an invitation to pass something a caller
// controls into a subprocess argument.
func headCommit() (string, error) {
	out, err := exec.Command("git", "rev-parse", "HEAD").Output()
	if err != nil {
		return "", fmt.Errorf("gatereceipt: git rev-parse HEAD: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// gitDir is where the receipt lives, asked of git rather than assumed to
// be ".git": it is a file rather than a directory in a worktree, and this
// repository's own tooling is run from worktrees.
func gitDir() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--absolute-git-dir").Output()
	if err != nil {
		return "", fmt.Errorf("gatereceipt: locating the git directory: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// short renders a revision the way git logs do, and leaves anything that
// is not a full hash alone so an error message never lies about its input.
func short(rev string) string {
	if len(rev) < 12 {
		return rev
	}
	return rev[:12]
}
