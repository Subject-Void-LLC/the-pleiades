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
// # Why write has to be told where the gate started
//
// The receipt used to name HEAD as it was when the LAST line of the gate
// ran, twenty minutes after the first. A commit made inside that window
// therefore collected a receipt for a tree nothing had examined, and no
// check at the end could notice: committing leaves the tree clean, so the
// dirty-tree refusal below sees a perfectly tidy repository. That hole was
// demonstrated deliberately on this repository: the gate examined one
// commit, a commit made while it ran took the receipt, and the hook
// reported that unexamined commit as having passed.
//
// The fix is that the Makefile reads HEAD and the tree's state once, while
// make is still parsing the file, and hands both to `write` at the end
// (--started-at, --started-clean). A gate whose HEAD moved, or whose tree
// was already dirty when it started, writes no receipt. This is why write
// refuses to run without those flags rather than defaulting them: a
// default would be this command guessing at the one fact it cannot
// observe, and guessing in the permissive direction.
//
// # What a receipt cannot tell you
//
// Every check `make ci` runs is a pure function of the tree except one.
// `govulncheck` queries a live advisory database, so a receipt written a
// week ago says a commit had no known vulnerabilities A WEEK AGO, which is
// a different claim from the one a reader will take it for. That is what
// the age bound is for, and it is the only reason there is one: a commit
// hash cannot express "an advisory was published yesterday".
//
// It also says nothing about the commits BEHIND the one it names. A push
// sends every commit a ref's tip can reach, and a gate examines one tree:
// the tip's. An intermediate commit that does not compile travels under a
// green receipt, and always did, including under the hook that ran the
// suite inline. That is the reason a ref may also be pushed at a commit
// contained in the gated commit's history (see checkRef): those commits
// are travelling anyway, and refusing to let a tag or an older branch name
// one of them buys nothing while training `--no-verify`.
//
// # What a receipt is not
//
// It is not signed, and signing it would be theatre. Anything that can
// write .git/pleiades-gate.json can also type `git push --no-verify`, so a
// forgery buys nothing that was not already available. This is a device
// for keeping an honest developer honest about a suite that takes twenty
// minutes, not a boundary against a hostile one, and every refusal below
// is worded for the first reader rather than the second.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
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

// MaxClockSkew is how far into the future a receipt may be dated before it
// stops being evidence.
//
// The age bound above is a subtraction, and a subtraction against a clock
// that has moved backwards is negative: less than any bound, so a receipt
// dated next week would never expire at all. NTP stepping a laptop's clock
// back a few seconds during a twenty minute gate is ordinary and must not
// cost a developer the run; a receipt dated hours ahead is either a clock
// nothing should be trusted against or a file somebody edited.
const MaxClockSkew = 5 * time.Minute

// gateTargets are the gates allowed to write a receipt, each with what a
// pass from it actually means, printed with every accepted push.
//
// A closed set rather than free text, in both directions: `write` refuses
// a target nobody recognises, and `verify` refuses a receipt naming one,
// because the whole purpose of the field is to stop a tolerant pass being
// read as a strict one, and a value neither command understands cannot be
// read as either.
var gateTargets = map[string]string{
	"ci":        "strict",
	"push-gate": "tolerant",
}

// Receipt is what one passing gate run leaves behind.
type Receipt struct {
	// Commit is the revision the gate ran against, which is HEAD both when
	// the gate started and when the receipt was written, and, because the
	// tree was clean at both moments, is also exactly what was on disk.
	Commit string `json:"commit"`

	// Target names which gate ran, and it is recorded rather than assumed
	// because the two are not the same evidence. `ci` is strict;
	// `push-gate` tolerates a test failure that passes when re-run alone.
	// A reader who cannot tell them apart will eventually read a tolerated
	// pass as a strict one.
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

  write  --target <name> --started-at <sha> --started-clean <yes|no>
         record that the gate passed. Run it through `+"`make ci`"+` or
         `+"`make push-gate`"+`, which capture where the gate started; a
         receipt naming HEAD as it is when the gate FINISHES would cover a
         commit made while it ran.

  verify --push-stdin
         read git's pre-push lines from standard input and answer for every
         ref being pushed. This is what .githooks/pre-push runs.

  verify --commit <sha>
         ask about one commit by hand.

write refuses a dirty tree, because a receipt names a commit and a dirty
tree means the gate ran against something else.
`)
}

// runWrite records a pass, refusing whenever the run cannot be pinned to
// the commit that is now checked out.
func runWrite(args []string) error {
	fs := flag.NewFlagSet("write", flag.ContinueOnError)
	target := fs.String("target", "", "which gate ran: ci or push-gate")
	startedAt := fs.String("started-at", "", "the commit HEAD named when the gate STARTED, captured by the Makefile")
	startedClean := fs.String("started-clean", "", "yes if the working tree had no uncommitted changes when the gate started")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*target) == "" {
		return errors.New("gatereceipt write: --target is required, so a reader can tell a strict pass from a tolerant one")
	}
	if _, known := gateTargets[*target]; !known {
		return fmt.Errorf("gatereceipt write: --target %q is not a gate this command knows (%s), so a receipt naming it would not say what the pass means",
			*target, strings.Join(knownTargets(), " or "))
	}
	if !isObjectName(*startedAt) || (*startedClean != "yes" && *startedClean != "no") {
		// Refused rather than defaulted. What HEAD was twenty minutes ago
		// is the one fact this command cannot observe, and a default would
		// be a guess in the permissive direction.
		return errors.New("gatereceipt write: --started-at <sha> and --started-clean <yes|no> are required, and name where the gate started rather than where it ended.\n" +
			"Run the gate through `make ci` or `make push-gate`, which capture both before the first check instead of after the last")
	}

	head, err := headCommit()
	if err != nil {
		return err
	}
	if head != *startedAt {
		return fmt.Errorf("gatereceipt write: HEAD was %s when the gate started and is %s now, so the gate examined a commit that is no longer checked out and no receipt was written.\n"+
			"Nothing is wrong with the work; a commit was made while the gate ran, and the run says nothing about it. Re-run the gate on %s",
			short(*startedAt), short(head), short(head))
	}
	if *startedClean != "yes" {
		return errors.New("gatereceipt write: the working tree already had uncommitted changes when the gate started, so the run examined code that is in no commit and no receipt was written.\n" +
			"Commit the work, then run the gate again")
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
	warnIfHooksDisabled()
	return nil
}

// pushRef is one line of what git tells a pre-push hook it is sending.
type pushRef struct {
	// name is the local ref, as `refs/heads/main` or `refs/tags/v1`, and is
	// only ever printed: a refusal that does not say WHICH ref it is about
	// is unactionable on a push that sends several.
	name string

	// local is the object being pushed. For a branch it is a commit; for an
	// annotated tag it is the tag object, which is why checkRef peels it.
	local string

	// remote is what git advertised for this ref on the remote, all zeros
	// when the ref does not exist there yet. It is what tells a ref being
	// created or fast-forwarded apart from one being moved backwards.
	remote string
}

// runVerify answers whether a push may proceed.
func runVerify(args []string) error {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	commit := fs.String("commit", "", "ask about one commit by hand")
	pushStdin := fs.Bool("push-stdin", false, "read git's pre-push lines from standard input and answer for every ref being pushed")
	if err := fs.Parse(args); err != nil {
		return err
	}

	var refs []pushRef
	if *pushStdin {
		var err error
		if refs, err = readPushRefs(os.Stdin); err != nil {
			return err
		}
		if len(refs) == 0 {
			// A push that only deletes refs sends no code, so there is
			// nothing for any gate to have examined.
			fmt.Println("pre-push: this push only deletes refs, so it sends no code to check")
			return nil
		}
	} else {
		if !isObjectName(*commit) {
			return fmt.Errorf("gatereceipt verify: --commit must be a full git object name, got %q", *commit)
		}
		refs = []pushRef{{local: *commit}}
	}

	receipt, err := loadReceipt(refs)
	if err != nil {
		return err
	}

	// Every ref is answered before anything is returned. A push of several
	// refs that stopped at the first refusal would be fixed one twenty
	// minute gate run at a time.
	var refused []string
	for _, ref := range refs {
		accepted, err := checkRef(receipt, ref)
		if err != nil {
			refused = append(refused, err.Error())
			continue
		}
		fmt.Println(accepted)
	}
	if len(refused) > 0 {
		return errors.New(strings.Join(refused, "\n\n"))
	}
	return nil
}

// readPushRefs reads the lines git feeds a pre-push hook and returns the
// refs that send something.
//
// The format is one line per ref: `<local ref> <local sha> <remote ref>
// <remote sha>`. An all-zero local sha means that ref is being deleted,
// which adds no code to the remote and is dropped here, so a push that
// only deletes refs needs no receipt at all.
//
// This parsing lives in Go rather than in the hook because every decision
// made from these fields is tested, and shell that splits its own input is
// not: a ref name the loop mangled would fail closed, which reads as the
// gate refusing a commit that passed.
func readPushRefs(r io.Reader) ([]pushRef, error) {
	body, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("gatereceipt verify: reading git's pre-push lines: %w", err)
	}
	var refs []pushRef
	for _, line := range strings.Split(string(body), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if len(fields) != 4 {
			return nil, fmt.Errorf("gatereceipt verify: git's pre-push line %q does not have the four fields this hook is defined by (<local ref> <local sha> <remote ref> <remote sha>)", line)
		}
		if isZeroObjectName(fields[1]) {
			continue
		}
		refs = append(refs, pushRef{name: fields[0], local: fields[1], remote: fields[3]})
	}
	return refs, nil
}

// loadReceipt reads the receipt and refuses everything that makes it
// something other than evidence: missing, unreadable, naming a gate or a
// commit nobody can interpret, undated, dated ahead of the clock, or past
// the age bound.
//
// Each refusal names the push it is refusing, because a developer reads
// this while looking at a `git push` they expected to work.
func loadReceipt(refs []pushRef) (Receipt, error) {
	sending := describe(refs)
	dir, err := gitDir()
	if err != nil {
		return Receipt{}, err
	}
	path := filepath.Join(dir, receiptName)

	body, err := os.ReadFile(path) // #nosec G304 -- path is .git/<constant>, derived from git itself
	if errors.Is(err, os.ErrNotExist) {
		return Receipt{}, fmt.Errorf("no gate receipt: nothing on this machine has verified %s.\nRun `make push-gate` (or `make ci`), then push again", sending)
	}
	if err != nil {
		return Receipt{}, fmt.Errorf("gatereceipt verify: reading %s: %w", path, err)
	}

	var r Receipt
	if err := json.Unmarshal(body, &r); err != nil {
		// A receipt nobody can read is not a pass. Say which file, so the
		// fix is obvious rather than mysterious.
		return Receipt{}, fmt.Errorf("the gate receipt at %s is unreadable (%v), so it proves nothing.\nRun `make push-gate`, then push again", path, err)
	}
	if !isObjectName(r.Commit) {
		return Receipt{}, fmt.Errorf("the gate receipt at %s names %q, which is not a commit, so it says nothing about anything.\nRun `make push-gate`, then push again", path, r.Commit)
	}
	if r.Target == "" {
		return Receipt{}, fmt.Errorf("the gate receipt at %s does not say which gate ran, so a tolerant pass cannot be told from a strict one.\nRun `make push-gate`, then push again", path)
	}
	if _, known := gateTargets[r.Target]; !known {
		return Receipt{}, fmt.Errorf("the gate receipt at %s names %q, which is not a gate this repository has (%s), so it cannot say what the pass means.\nRun `make push-gate`, then push again",
			path, r.Target, strings.Join(knownTargets(), " or "))
	}
	if r.WrittenAt.IsZero() {
		return Receipt{}, fmt.Errorf("the gate receipt at %s does not say when it was written, so the age bound govulncheck needs cannot be applied to it.\nRun `make push-gate`, then push again", path)
	}
	if ahead := time.Until(r.WrittenAt); ahead > MaxClockSkew {
		// Its own refusal because it is its own cause. An age is a
		// subtraction, and a receipt dated ahead of the clock produces a
		// negative one, which is below every bound and so would have been
		// accepted forever.
		return Receipt{}, fmt.Errorf("the gate receipt for %s is dated %s in the future, so either this machine's clock moved or the file was edited, and an age bound cannot be applied to it.\nRun `make push-gate`, then push again",
			short(r.Commit), ahead.Round(time.Second))
	}
	if age := time.Since(r.WrittenAt); age > MaxReceiptAge {
		// Named separately from a commit mismatch, because the fix is the
		// same command for a completely different reason and a reader who
		// conflates them will not understand why a commit that passed is
		// being refused.
		return Receipt{}, fmt.Errorf("the gate receipt for %s is %s old, past the %s bound.\nEverything a gate checks is fixed by the commit except govulncheck, which reads a live advisory database, so an old pass no longer answers that one question.\nRun `make push-gate`, then push again",
			short(r.Commit), age.Round(time.Minute), MaxReceiptAge)
	}
	return r, nil
}

// checkRef decides one ref, returning the line to print when it passes.
//
// Three things are allowed through, and the second and third are why this
// is not a string comparison:
//
//   - the gated commit itself, which is the ordinary push;
//   - an object that PEELS to it, which is an annotated tag. Git hands the
//     hook the tag OBJECT's name, so a tag cut at the very commit that
//     just passed was refused by a comparison, at the one moment a
//     developer is most certain they are doing everything right;
//   - a commit CONTAINED in the gated commit's history, as long as the ref
//     is being created or fast-forwarded. Those commits are already
//     travelling inside the gated push, so refusing a tag or an older
//     branch that names one denies nothing and trains `--no-verify`. The
//     fast-forward condition is what keeps it honest: moving an existing
//     ref BACKWARDS would leave the remote pointing at a tree no gate
//     looked at, which is a different act from shipping history that is
//     going anyway.
func checkRef(receipt Receipt, ref pushRef) (string, error) {
	where := "this push"
	if ref.name != "" {
		where = ref.name
	}
	meaning := gateTargets[receipt.Target]
	age := time.Since(receipt.WrittenAt).Round(time.Minute)

	if ref.local == receipt.Commit {
		return fmt.Sprintf("pre-push: %s passed %s (%s) here %s ago", short(ref.local), receipt.Target, meaning, age), nil
	}

	commit, err := peelToCommit(ref.local)
	if err != nil {
		return "", fmt.Errorf("%s is being pushed at %s, which does not resolve to a commit in this clone (%v), so no gate can have examined it",
			where, short(ref.local), err)
	}
	if commit == receipt.Commit {
		return fmt.Sprintf("pre-push: %s points at %s, which passed %s (%s) here %s ago", where, short(commit), receipt.Target, meaning, age), nil
	}

	contained, err := containedInHistory(commit, receipt.Commit)
	if err != nil {
		return "", fmt.Errorf("%s is being pushed at %s and the gate receipt is for %s, and git could not say whether one contains the other (%v), so this push cannot be allowed on evidence nobody has",
			where, short(commit), short(receipt.Commit), err)
	}
	if !contained {
		return "", fmt.Errorf("the gate receipt is for %s, but %s is being pushed at %s.\n"+
			"Those are different commits and %s is not part of %s's history, so nothing on this machine has examined it.\n"+
			"Run `make push-gate` (or `make ci`) with %s checked out, then push again",
			short(receipt.Commit), where, short(commit), short(commit), short(receipt.Commit), short(commit))
	}

	forwards, why := movesForwards(ref)
	if !forwards {
		return "", fmt.Errorf("%s is being pushed at %s, which is part of the gated commit %s but %s.\n"+
			"A ref moved back to an older commit leaves the remote pointing at a tree no gate examined, which is not the same as sending history that travels with a gated tip.\n"+
			"Check out %s, run `make push-gate` there, then push again",
			where, short(commit), short(receipt.Commit), why, short(commit))
	}
	return fmt.Sprintf("pre-push: %s at %s is inside %s, which passed %s (%s) here %s ago",
		where, short(commit), short(receipt.Commit), receipt.Target, meaning, age), nil
}

// movesForwards reports whether a ref is being created or fast-forwarded,
// and, when it is not, the phrase that says why in a refusal.
//
// A ref git advertised as absent is being created, so there is nothing to
// move backwards from. Otherwise the remote's own commit has to be an
// ancestor of what is being sent, and a remote commit this clone does not
// have cannot be compared at all: that answer is unknown rather than yes,
// and unknown fails closed.
func movesForwards(ref pushRef) (bool, string) {
	if ref.remote == "" {
		return false, "nothing said what the remote has for it, so a rewind cannot be ruled out"
	}
	if isZeroObjectName(ref.remote) {
		return true, ""
	}
	if !isObjectName(ref.remote) {
		return false, fmt.Sprintf("the remote's own commit reads as %q, which is not an object name", ref.remote)
	}
	contained, err := containedInHistory(ref.remote, ref.local)
	if err != nil {
		return false, fmt.Sprintf("this clone does not have the remote's %s, so whether that is a rewind cannot be decided here", short(ref.remote))
	}
	if !contained {
		return false, fmt.Sprintf("it would move off the remote's %s, which is not an ancestor of it", short(ref.remote))
	}
	return true, ""
}

// peelToCommit resolves an object being pushed to the commit it names, so
// an annotated tag is answered about the commit it points at.
//
// Asked of git through --batch-check, whose argv is fixed and whose input
// is a revision on stdin. That shape is deliberate: the name comes from
// git's own hook stdin, and putting it in an exec argument would be both a
// gosec G204 finding and a real one, since --stdin-style flags read from a
// stream cannot be mistaken for options. Anything that does not resolve
// (a tag on a blob, an object this clone does not have) comes back as a
// name git prints as missing rather than as an error, so the answer is
// checked for a real object name rather than for an exit code.
func peelToCommit(name string) (string, error) {
	if !isObjectName(name) {
		return "", fmt.Errorf("%q is not a git object name", name)
	}
	cmd := exec.Command("git", "cat-file", "--batch-check=%(objectname)")
	cmd.Stdin = strings.NewReader(name + "^{commit}\n")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git cat-file: %w", err)
	}
	peeled := strings.TrimSpace(string(out))
	if !isObjectName(peeled) {
		return "", fmt.Errorf("git answers %q", peeled)
	}
	return peeled, nil
}

// containedInHistory reports whether every commit reachable from candidate
// is also reachable from tip, which is the question "does the gated push
// already carry this".
//
// `git rev-list candidate ^tip` lists what candidate has and tip does not,
// so an empty answer is containment. Both revisions go in on stdin for the
// reason peelToCommit's do, and both are checked against isObjectName
// first, which matters more here than it looks: `rev-list --stdin` accepts
// pseudo-options such as `--all` on that stream, so an unvalidated value
// could widen the question being asked rather than answer it.
func containedInHistory(candidate, tip string) (bool, error) {
	if !isObjectName(candidate) || !isObjectName(tip) {
		return false, fmt.Errorf("%q and %q are not both git object names", candidate, tip)
	}
	cmd := exec.Command("git", "rev-list", "--stdin", "--max-count=1")
	cmd.Stdin = strings.NewReader(candidate + "\n^" + tip + "\n")
	out, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("git rev-list: %w", err)
	}
	return strings.TrimSpace(string(out)) == "", nil
}

// describe names what a push is sending, for the refusals that are about
// the receipt rather than about one ref.
func describe(refs []pushRef) string {
	const most = 3
	var parts []string
	for i, ref := range refs {
		if i == most {
			parts = append(parts, fmt.Sprintf("and %d more", len(refs)-most))
			break
		}
		if ref.name == "" {
			parts = append(parts, short(ref.local))
			continue
		}
		parts = append(parts, fmt.Sprintf("%s at %s", ref.name, short(ref.local)))
	}
	return strings.Join(parts, ", ")
}

// knownTargets lists the gates a receipt may name, in a fixed order so a
// refusal reads the same way twice.
func knownTargets() []string {
	return []string{"ci", "push-gate"}
}

// warnIfHooksDisabled says so when this clone will not check the receipt
// that was just written.
//
// Hooks are opt-in per clone, because git will not run one that arrived
// with a fetch until somebody asks. That is the right default and it means
// a fresh clone pushes with nothing checking anything, silently. The gate
// finishing is the one moment a developer is certainly paying attention,
// so it is where saying so costs least. A warning rather than a refusal:
// running the gate is useful whether or not this clone pushes through a
// hook.
func warnIfHooksDisabled() {
	out, err := exec.Command("git", "config", "--get", "core.hooksPath").Output()
	path := strings.TrimSpace(string(out))
	if err == nil && strings.HasSuffix(path, ".githooks") {
		return
	}
	fmt.Printf("gatereceipt: warning: this clone's hooks are not enabled (core.hooksPath is %q), so `git push` will not read this receipt back. Run `make hooks` once per clone.\n", path)
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

// isObjectName reports whether a string is a full git object name, in
// either hash this repository could be using.
//
// Checked everywhere a value from a receipt file or from git's hook stdin
// is about to become part of a question asked of git, which is the only
// reason it exists: every such value is validated before it travels.
func isObjectName(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// isZeroObjectName reports whether git is naming the absence of an object,
// which it does with a name of all zeros: a ref being deleted on the local
// side, or one that does not exist yet on the remote.
func isZeroObjectName(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	return strings.Trim(s, "0") == ""
}

// short renders a revision the way git logs do, and leaves anything that
// is not a full hash alone so an error message never lies about its input.
func short(rev string) string {
	if len(rev) < 12 {
		return rev
	}
	return rev[:12]
}
