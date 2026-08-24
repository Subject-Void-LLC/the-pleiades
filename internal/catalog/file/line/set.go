package line

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// This file implements "file.line.set", which is ansible.builtin.lineinfile
// with state: present.

// The words this method reports under msg, which are lineinfile's own
// words for the same three outcomes so a converted playbook's later task
// reading msg keeps reading what it read before.
const (
	setMsgUnchanged = ""
	setMsgAdded     = "line added"
	setMsgReplaced  = "line replaced"
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: "file.line.set",
		Manifest: collection.Manifest{
			SupportedTransports: []string{
				"ssh",
			},
			RequiredCapabilities: []capability.Name{
				capability.NamePOSIXFileSystem,
			},
			// Left false even though most of what this gets pointed at lives
			// under /etc. Elevation is a property of the individual task, since
			// editing a file the connecting account already owns needs nothing,
			// and declaring it unconditionally would mark every task privileged
			// and teach a reader to skip the field.
			ExecutionContext: collection.ExecutionContext{
				RequiresElevation: false,
			},
			PlatformTargets: nil,
			EngineVersion:   ">=1.0.0",
			Status:          collection.StatusImplemented,
			// True, and the concrete instruction comes from the run rather than
			// from here: which bytes to put back is a property of what this run
			// found, not of what this method is. See lineRecordInverse.
			Reversibility: collection.Reversibility{
				Reversible: true,
				Notes: "A run that changed the file emits a file.copy carrying the whole text the file held beforehand, with " +
					"its mode, owner and group. The whole file, because a line edit is not reliably undone by another line " +
					"edit: a replacement has already thrown the old line away, and an anchored insert cannot be located " +
					"again. That makes the record as large as the file. A converged run emits nothing, since undoing a " +
					"change that was never made means doing nothing, and a run that fails partway emits nothing either. The " +
					"modification time is not restored, and file.copy itself is declared rather than implemented today.",
			},
			Doc: setDoc(),
		},
		Invoke: Set,
	})
}

// setDoc is this method's reference documentation, kept out of the
// registration above so the manifest fields stay readable.
//
// It is duplicated into internal/forge/catalogdata, the source the
// scaffolder is driven from, and internal/archtest compares the two for
// equality so the copies cannot drift.
func setDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Ensures one line matching a pattern is present in a file, replacing or appending it.",
		Description: "Makes sure one line is present in a text file, which is ansible.builtin.lineinfile with state: present. The whole file is read, the new text is worked out locally, and the file is written back only when the bytes really differ, so a run that finds the line already in place sends no write at all. When regexp is set, the last line it matches is replaced; when it is not set, a line already exactly equal to line means the task is done. A line that has to be added goes at the end of the file unless insertafter or insertbefore names a pattern to place it against. The file has to exist already, since Ansible's create is not implemented, so file.touch or file.copy is what makes one. Two differences from Ansible are deliberate and worth knowing. A regexp that does not match the line being placed is refused, because such a task adds the line again on every single run and never settles. And a file that ends without a newline keeps ending without one, where Ansible would add it. Patterns are Go's RE2, which has no backreferences and no lookaround; backrefs, search_string, firstmatch, create, backup and validate are not implemented. Lines are split on the newline byte alone, so a file with Windows endings carries its carriage return as part of each line's text and a pattern meant to match one has to say so.",
		Params: []collection.Param{
			{Name: lineParamPath, Type: "string", Required: true, Description: "The text file to edit. It has to exist already and it has to be a regular file. A symbolic link is refused rather than followed, because writing replaces the path and would leave a regular file where the link was."},
			{Name: lineParamLine, Type: "string", Required: true, Description: "The exact text of the line to make sure is present, written verbatim, so leading whitespace is part of the line. It may not contain a newline: this method places one line, and file.block.set is what places several."},
			{Name: lineParamRegexp, Type: "string", Description: "A pattern naming the line to replace. The last line it matches is replaced by line, and if nothing matches then line is added. It has to match line itself, and is refused when it does not, because otherwise no run would ever find the line it just added and the file would grow forever. Matched anywhere in a line unless anchored with ^ or $."},
			{Name: lineParamInsertAfter, Type: "string", Description: "Where to put the line when it has to be added: a pattern, and the line goes after the last line matching it. The value EOF means the end of the file, which is also what happens when this is left out and when the pattern matches nothing. Cannot be combined with insertbefore."},
			{Name: lineParamInsertBefore, Type: "string", Description: "Where to put the line when it has to be added: a pattern, and the line goes before the last line matching it. The value BOF means the start of the file. A pattern matching nothing puts the line at the end, which is Ansible's own behavior. Cannot be combined with insertafter."},
			{Name: sdk.ParamInsecureSkipHostKeyVerify, Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
		},
		Returns: []collection.ReturnField{
			{Name: lineStatPath, Type: "string", Returned: "always", Description: "The file this task edited."},
			{Name: lineStatMsg, Type: "string", Returned: "always", Description: "What happened, in lineinfile's own words: \"line added\", \"line replaced\", or empty when the file was already correct."},
			{Name: sdk.StatDiff, Type: "dict", Returned: "always", Description: "The file's state before and after, each holding exists, kind, mode, owner, group, size, mtime and the file's whole text. Recorded even when nothing changed, in which case the two halves are identical."},
		},
		Examples: []collection.Example{
			{
				Name:        "Correct a setting whether or not it is commented out",
				RunbookYAML: "- name: Turn off root login over SSH\n  file.line.set:\n    path: /etc/ssh/sshd_config\n    regexp: '^#?PermitRootLogin'\n    line: PermitRootLogin no\n",
			},
			{
				Name:        "Append an entry that is either there or not",
				RunbookYAML: "- name: Add the internal registry to the hosts file\n  file.line.set:\n    path: /etc/hosts\n    line: 10.0.4.12 registry.internal\n",
			},
			{
				Name:        "Place a line against an anchor",
				RunbookYAML: "- name: Put the include ahead of the defaults section\n  file.line.set:\n    path: /etc/app/app.conf\n    line: include /etc/app/conf.d/all.conf\n    insertbefore: '^\\[defaults\\]'\n",
			},
		},
		SeeAlso: []string{"file.line.remove", "file.block.set", "file.copy"},
	}
}

// setParams is everything file.line.set needs from its task, already
// checked and compiled.
type setParams struct {
	path string
	line string

	// find is the pattern deciding which line this task is about, or nil
	// when the task named none and the line's own text is the search.
	find *regexp.Regexp

	// anchor is the pattern naming where to insert a line that was not
	// found, with after saying which side of the match it goes on. Nil means
	// the end of the file, which is the default and also what an anchor that
	// matches nothing falls back to.
	anchor *regexp.Regexp
	after  bool

	// atStart is insertbefore: BOF, the one anchor that is a position rather
	// than a pattern.
	atStart bool
}

// Set implements the "file.line.set" collection method: it makes sure one
// line is present in a text file.
//
// # The order of operations is the contract
//
// Everything the runbook got wrong is refused before the first packet, so a
// mistake in a task costs no round trip and names the runbook rather than
// the device. Then the file is read, then the new text is worked out and
// compared against what was read, and only a real difference is written.
//
// Reading first is what makes Changed mean anything at all. Writing a file
// always "succeeds", so a method that rendered and wrote unconditionally
// would look identical on the device and would report changed on every run
// forever, and would move the file's modification time each time it did.
//
// The read is also the only chance to capture the prior text, which is what
// the emitted inverse carries. Once the file is rewritten it is gone.
func Set(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	const fqcn = "file.line.set"

	req, err := setRequest(params)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}

	conn, err := sdk.Connect(ctx, rc, device, params, fqcn)
	if err != nil {
		return collection.Result{}, err
	}
	defer func() { _ = conn.Close() }()

	before, err := lineLoad(ctx, conn, req.path)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if !before.info.Exists() {
		// Refused rather than created, which is lineinfile's own default
		// (create: false). The refusal names what does create, because "does
		// not exist" on its own reads as a mistake in the runbook's ordering
		// rather than as a deliberate boundary between methods.
		return collection.Result{}, fmt.Errorf("%s: %s does not exist: this method edits a file rather than creating one, and file.touch or file.copy is what creates", fqcn, req.path)
	}

	lines, msg := setApply(req, before.lines)
	content := lineRender(lines, before.trailing)

	// The rendered text against the bytes that were read is the ONLY thing
	// that decides changed. setApply's answer describes what it did to a list
	// of strings, and the two can honestly disagree: replacing a line with
	// itself is not a change to the device however it is described, and
	// neither is any edit that renders back to the same bytes.
	if content == before.content {
		if err := sdk.RecordDiff(rc, sdk.Unchanged(lineState(before.info, before.content))); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
		// setApply's own answer rather than a hardcoded setMsgUnchanged, so
		// there is exactly one thing deciding what msg says. The two cannot
		// disagree: setApply reports unchanged for the one case that renders
		// back to identical bytes, a found line that already reads the way the
		// task wants, and every other answer it gives adds or rewrites a line
		// and therefore cannot land here.
		if err := lineRecordStats(rc, map[string]any{lineStatPath: req.path, lineStatMsg: msg}); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
		// No inverse, and the absence is the record rather than an omission:
		// it is how the journal says undoing this task means doing nothing.
		// Emitting one here would have a rollback rewrite a file this run
		// never touched.
		return collection.Result{}, nil
	}

	after, afterContent, err := lineWrite(ctx, conn, req.path, content, before.info)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}

	if err := lineRecordDiff(rc, before, after, afterContent); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if err := lineRecordInverse(rc, req.path, before); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if err := lineRecordStats(rc, map[string]any{lineStatPath: req.path, lineStatMsg: msg}); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	return collection.Result{Changed: true}, nil
}

// setRequest reads and checks everything this method needs from the task's
// params, compiling the patterns while it is there.
//
// It runs before the connection is opened, on purpose. Every refusal below
// is a mistake in the runbook rather than a condition on the device, and one
// that costs a TCP connect, a key exchange and an authentication round
// before reporting itself is a slower answer to the same question.
func setRequest(params map[string]any) (setParams, error) {
	var req setParams

	// All five read through one loop rather than five near identical blocks:
	// the type refusal is the same sentence for each of them, and five copies
	// of a sentence is five places for one of them to drift.
	values := make(map[string]string, 5)
	for _, key := range []string{lineParamPath, lineParamLine, lineParamRegexp, lineParamInsertAfter, lineParamInsertBefore} {
		value, err := lineTextParam(params, key)
		if err != nil {
			return req, err
		}
		values[key] = value
	}

	req.path = values[lineParamPath]
	if req.path == "" {
		return req, fmt.Errorf("%s is required", lineParamPath)
	}

	req.line = values[lineParamLine]
	if req.line == "" {
		return req, fmt.Errorf("%s is required: this method makes sure one line is present and has to be told which one", lineParamLine)
	}
	// One line, exactly. A value carrying a newline would be written as
	// several lines and then never found again by the single line search that
	// decides convergence, so every later run would add the whole thing once
	// more and the file would grow without limit.
	if strings.Contains(req.line, "\n") {
		return req, fmt.Errorf("%s contains a newline: this method places one line, and file.block.set is what places several", lineParamLine)
	}

	if err := setPattern(&req, values[lineParamRegexp]); err != nil {
		return req, err
	}
	if err := setAnchor(&req, values[lineParamInsertAfter], values[lineParamInsertBefore]); err != nil {
		return req, err
	}
	return req, nil
}

// setPattern compiles regexp and refuses one that could never converge.
//
// The refusal is the interesting half. A pattern that does not match the
// line being placed is the classic lineinfile footgun and it is silent: on
// the first run the pattern finds nothing, so the line is appended; on the
// second run it still finds nothing, because the line just added does not
// match it either, so the line is appended again, and again on every run
// after that. Ansible accepts this and grows the file forever. Catching it
// costs one comparison and no round trip, and the fix is nearly always to
// widen the pattern, ^PermitRootLogin rather than ^PermitRootLogin\s+yes.
func setPattern(req *setParams, pattern string) error {
	if pattern == "" {
		return nil
	}
	find, err := lineCompile(lineParamRegexp, pattern)
	if err != nil {
		return err
	}
	if !find.MatchString(req.line) {
		return fmt.Errorf("%s %q does not match %s %q: no run would ever find the line it just added, so the file would grow on every run. Widen the pattern until it matches the line being placed",
			lineParamRegexp, pattern, lineParamLine, req.line)
	}
	req.find = find
	return nil
}

// setAnchor works out where an added line goes from insertafter and
// insertbefore.
//
// Ansible declares the two mutually exclusive in its own argument spec, and
// the reason is not style: a line cannot go in two places, so there is no
// answer when both patterns match. The two cross anchors are refused rather
// than treated as patterns because Ansible's own code quietly accepts
// insertafter: BOF and treats it as insertbefore: BOF, and a reader of the
// runbook would have to know that quirk to know what the task does.
func setAnchor(req *setParams, after, before string) error {
	if after != "" && before != "" {
		return fmt.Errorf("%s and %s cannot both be set: they name two different places to put the line", lineParamInsertAfter, lineParamInsertBefore)
	}

	switch {
	case after == lineAnchorBOF:
		return fmt.Errorf("%s %s is not an anchor: write %s: %s to put the line at the start of the file", lineParamInsertAfter, lineAnchorBOF, lineParamInsertBefore, lineAnchorBOF)
	case before == lineAnchorEOF:
		return fmt.Errorf("%s %s is not an anchor: leave both anchors out to put the line at the end of the file, which is what this method already does", lineParamInsertBefore, lineAnchorEOF)
	case before == lineAnchorBOF:
		req.atStart = true
	case after != "" && after != lineAnchorEOF:
		anchor, err := lineCompile(lineParamInsertAfter, after)
		if err != nil {
			return err
		}
		req.anchor, req.after = anchor, true
	case before != "":
		anchor, err := lineCompile(lineParamInsertBefore, before)
		if err != nil {
			return err
		}
		req.anchor = anchor
	}
	// Everything left over, both empty or insertafter: EOF, means the end of
	// the file, which is what a nil anchor and a false atStart already say.
	return nil
}

// setApply returns the file's lines with the task's line in place, and
// which of the three things happened.
//
// It never writes into the slice it was handed. The caller still holds the
// text those lines were split from and compares against it afterwards, and
// a helper that edited its argument in place would be a very quiet way to
// make that comparison lie.
func setApply(req setParams, lines []string) ([]string, string) {
	found := setFind(req, lines)
	if found < 0 {
		return setInsert(req, lines), setMsgAdded
	}
	if lines[found] == req.line {
		return lines, setMsgUnchanged
	}

	replaced := make([]string, len(lines))
	copy(replaced, lines)
	replaced[found] = req.line
	return replaced, setMsgReplaced
}

// setFind returns the index of the line this task is about, or -1.
//
// With a pattern, the LAST match wins, which is Ansible's default and the
// useful answer for a configuration file: a setting written twice is
// effectively whichever came last, so that is the copy worth correcting.
// Ansible's firstmatch is not implemented.
//
// Without a pattern, the line's own text is the search and the first exact
// match ends it. That is what makes a task with no regexp idempotent: once
// the line is in the file, every later run finds it and stops.
func setFind(req setParams, lines []string) int {
	found := -1
	for i, text := range lines {
		if req.find == nil {
			if text == req.line {
				return i
			}
			continue
		}
		if req.find.MatchString(text) {
			found = i
		}
	}
	return found
}

// setInsert returns the lines with the task's line added where its anchors
// say it goes.
func setInsert(req setParams, lines []string) []string {
	at := len(lines)
	switch {
	case req.atStart:
		at = 0
	case req.anchor != nil:
		// The last match again, matching setFind so the two halves of this
		// method agree about what "the" matching line means. An anchor that
		// matches nothing leaves at where it started, the end of the file,
		// which is Ansible's documented fallback for both anchors: naming a
		// place that is not there is a weaker statement than "do not add this".
		for i, text := range lines {
			if !req.anchor.MatchString(text) {
				continue
			}
			at = i
			if req.after {
				at = i + 1
			}
		}
	}

	// A fresh slice with room for everything, so appending the tail cannot
	// scribble over the caller's backing array.
	out := make([]string, 0, len(lines)+1)
	out = append(out, lines[:at]...)
	out = append(out, req.line)
	return append(out, lines[at:]...)
}
