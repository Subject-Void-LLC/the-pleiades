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

// This file implements "file.line.remove", which is
// ansible.builtin.lineinfile with state: absent.

// removeMsgAbsent is what this reports for a file that is not there, which
// is lineinfile's own wording for the same answer.
const removeMsgAbsent = "file not present"

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: "file.line.remove",
		Manifest: collection.Manifest{
			SupportedTransports: []string{
				"ssh",
			},
			RequiredCapabilities: []capability.Name{
				capability.NamePOSIXFileSystem,
			},
			// Left false for the same reason file.line.set leaves it false:
			// elevation belongs to the individual task, and marking every task
			// privileged teaches a reader to skip the field.
			ExecutionContext: collection.ExecutionContext{
				RequiresElevation: false,
				Site:              collection.SiteTarget,
				Device:            collection.DeviceRequired,
			},
			PlatformTargets: nil,
			EngineVersion:   ">=0.2.0",
			Status:          collection.StatusImplemented,
			// It reads the file and computes the edit before it writes, so
			// a check can predict through the same code (CheckRemove).
			SupportsCheck: true,
			// True, and what to put back is decided by the run rather than
			// here: this method may take one line out or fifty, from anywhere
			// in the file. See lineRecordInverse.
			Reversibility: collection.Reversibility{
				Reversible: true,
				Notes: "A run that removed something emits a file.copy carrying the whole text the file held beforehand, with " +
					"its mode, owner and group. The whole file, because this can take several lines from several places and " +
					"putting them back with file.line.set would pile them all at the end instead. That makes the record as " +
					"large as the file. A run that found nothing to remove emits nothing, and so does a run against a file " +
					"that was not there. The modification time is not restored, and file.copy itself is declared rather than " +
					"implemented today.",
				Inverses: []sdk.InverseSpec{
					{FQCN: "file.copy", Record: []string{"dest", "mode", "owner", "group"}, Withhold: []string{"content"}},
				},
			},
			Doc: removeDoc(),
		},
		Invoke: Remove,
		Check:  CheckRemove,
	})
}

// removeDoc is this method's reference documentation, kept out of the
// registration above so the manifest fields stay readable.
//
// It is duplicated into internal/forge/catalogdata, the source the
// scaffolder is driven from, and internal/archtest compares the two for
// equality so the copies cannot drift.
func removeDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Ensures no line matching a pattern remains in a file.",
		Description: "Makes sure no line matching a pattern remains in a text file, which is ansible.builtin.lineinfile with state: absent. The whole file is read, the surviving lines are worked out locally, and the file is written back only when the bytes really differ, so a run that finds nothing to remove sends no write at all. Every matching line goes, not only the first. A file that is not there is already in the wanted state, so the task reports no change instead of failing, which is what lets one runbook strip a setting from a fleet where not every host has the file. Three differences from Ansible are deliberate: setting both regexp and line is refused rather than silently preferring regexp, insertafter and insertbefore are refused rather than accepted and ignored, and a file that ends without a newline keeps ending without one. Patterns are Go's RE2, which has no backreferences and no lookaround; search_string, backup and validate are not implemented. Lines are split on the newline byte alone, so a file with Windows endings carries its carriage return as part of each line's text and a pattern meant to match one has to say so.",
		Params: []collection.Param{
			{Name: lineParamPath, Type: "string", Required: true, Description: "The text file to edit. A file that is not there is reported as no change rather than as an error, since it holds no lines to remove. A symbolic link is refused rather than followed, because writing replaces the path and would leave a regular file where the link was."},
			{Name: lineParamRegexp, Type: "string", Description: "Remove every line this pattern matches. Matched anywhere in a line unless anchored with ^ or $. Cannot be combined with line, and one of the two is required."},
			{Name: lineParamLine, Type: "string", Description: "Remove every line whose text is exactly this. It may not contain a newline, since a line is matched whole. Cannot be combined with regexp, and one of the two is required."},
			{Name: sdk.ParamInsecureSkipHostKeyVerify, Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
		},
		Returns: []collection.ReturnField{
			{Name: lineStatPath, Type: "string", Returned: "always", Description: "The file this task acted on."},
			{Name: lineStatFound, Type: "int", Returned: "always", Description: "How many lines were removed. Zero when the file held none matching, and zero when the file was not there at all."},
			{Name: lineStatMsg, Type: "string", Returned: "always", Description: "What happened, in lineinfile's own words: \"3 line(s) removed\", \"file not present\", or empty when the file held nothing to remove."},
			{Name: sdk.StatDiff, Type: "dict", Returned: "always", Description: "The file's state before and after, each holding exists and kind, plus mode, owner, group, size, mtime and the file's whole text when it is there. Recorded even when nothing changed, in which case the two halves are identical."},
		},
		Examples: []collection.Example{
			{
				Name:        "Drop a package source by pattern",
				RunbookYAML: "- name: Drop the retired package mirror\n  file.line.remove:\n    path: /etc/apt/sources.list\n    regexp: '^deb .*mirror\\.old\\.example\\.com'\n",
			},
			{
				Name:        "Drop one exact entry",
				RunbookYAML: "- name: Remove the decommissioned host entry\n  file.line.remove:\n    path: /etc/hosts\n    line: 10.0.4.9 registry.internal\n",
			},
			{
				Name:        "Act only when something was really removed",
				RunbookYAML: "- name: Strip every commented out override\n  file.line.remove:\n    path: /etc/app/app.conf\n    regexp: '^#\\s*override'\n  register: overrides\n\n- name: Reload the service that read them\n  exec.command:\n    cmd: systemctl reload app\n  when_cel: overrides.found > 0\n",
			},
		},
		SeeAlso: []string{"file.line.set", "file.block.remove", "file.remove"},
	}
}

// removeParams is everything file.line.remove needs from its task, already
// checked and compiled.
type removeParams struct {
	path string

	// match is the pattern deciding which lines go, or nil when the task gave
	// an exact line instead. Exactly one of match and line is set, which
	// removeRequest enforces so removeMatches never has to guess.
	match *regexp.Regexp
	line  string
}

// Remove implements the "file.line.remove" collection method: it makes sure
// no line matching a pattern remains in a text file.
//
// # The order of operations is the contract
//
// Everything the runbook got wrong is refused before the first packet, then
// the file is read, then the surviving lines are compared against what was
// read, and only a real difference is written.
//
// Reading first is what makes Changed mean anything. Writing a file always
// "succeeds", so a method that rendered and wrote unconditionally would look
// identical on the device, would report changed on every run forever, and
// would move the file's modification time each time it did.
//
// The read is also the only chance to capture the prior text, which is what
// the emitted inverse carries, and it matters more here than anywhere else
// in this namespace: the lines this method takes out exist nowhere else once
// it has run.
func Remove(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return remove(ctx, rc, device, params, collection.ModeExecute)
}

// CheckRemove is file.line.remove's check: the same read, the same refusals and the same
// edit computed in memory, through the one body both share, then a
// prediction instead of the write. Whether it changes anything is decided
// exactly as a real run decides it, by the rendered text against the
// bytes read, and the predicted diff carries the whole new text
// (linePredictDiff).
func CheckRemove(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return remove(ctx, rc, device, params, collection.ModeCheck)
}

// remove is Remove's and CheckRemove's one body; mode says which.
func remove(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	const fqcn = "file.line.remove"

	req, err := removeRequest(params)
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
		// A file that is not there holds no lines, so the task is already
		// done. This is lineinfile's own answer for state: absent, and it is
		// the right one: refusing would make a runbook that strips a setting
		// fail on every host that never had the file, which is exactly the
		// mixed fleet this kind of task is written for. It is the one place
		// these two methods disagree about an absent path, since file.line.set
		// cannot put a line in a file that is not there.
		return removeConverged(rc, fqcn, req.path, before, removeMsgAbsent, 0)
	}

	kept, found := removeApply(req, before.lines)
	content := lineRender(kept, before.trailing)

	// The rendered text against the bytes that were read is the ONLY thing
	// that decides changed, for the same reason it is in file.line.set: an
	// edit that renders back to the same bytes is not a change to the device.
	if content == before.content {
		return removeConverged(rc, fqcn, req.path, before, removeMsg(found), found)
	}

	if mode == collection.ModeCheck {
		if err := linePredictDiff(rc, before, content); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
		if err := removeRecordStats(rc, req.path, removeMsg(found), found); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
		return collection.Result{Changed: true}, nil
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
	if err := removeRecordStats(rc, req.path, removeMsg(found), found); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	return collection.Result{Changed: true}, nil
}

// removeConverged records the run that changed nothing and reports it.
//
// It exists because this method reaches that outcome from two different
// places, a file that was not there and a file that held nothing matching,
// and the recording has to be identical either way. A diff IS still written:
// "it was already like this" is exactly what tells a rollback to do nothing,
// and an absent record cannot say that, it can only fail to say anything.
//
// No inverse is emitted, and that absence is the other half of the same
// statement. A run that changed nothing has nothing to undo, and emitting an
// instruction anyway would have a rollback rewrite a file this task never
// touched.
func removeConverged(rc sdk.RunbookContext, fqcn, path string, before lineFile, msg string, found int) (collection.Result, error) {
	if err := sdk.RecordDiff(rc, sdk.Unchanged(lineState(before.info, before.content))); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if err := removeRecordStats(rc, path, msg, found); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	return collection.Result{}, nil
}

// removeMsg is lineinfile's own wording for a count of removed lines, and
// its own empty answer when there were none.
func removeMsg(found int) string {
	if found == 0 {
		return ""
	}
	return fmt.Sprintf("%d line(s) removed", found)
}

// removeRecordStats writes the three stats this method returns.
func removeRecordStats(rc sdk.RunbookContext, path, msg string, found int) error {
	return lineRecordStats(rc, map[string]any{
		lineStatPath:  path,
		lineStatMsg:   msg,
		lineStatFound: found,
	})
}

// removeRequest reads and checks everything this method needs from the
// task's params, compiling the pattern while it is there.
//
// It runs before the connection is opened, on purpose: every refusal below
// is a mistake in the runbook rather than a condition on the device, and
// paying for a connect first is a slower answer to the same question.
func removeRequest(params map[string]any) (removeParams, error) {
	var req removeParams

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

	// Both anchors say where to PUT a line, and this method puts none.
	// Ansible accepts them with state: absent and ignores them, so a task that
	// carried one over from a converted playbook looks like it is doing
	// something it is not. Refusing says which parameter is doing nothing.
	for _, key := range []string{lineParamInsertAfter, lineParamInsertBefore} {
		if values[key] != "" {
			return req, fmt.Errorf("%s says where to insert a line and this method removes them: drop it, or use file.line.set if the line is meant to be there", key)
		}
	}

	pattern, text := values[lineParamRegexp], values[lineParamLine]
	if pattern == "" && text == "" {
		return req, fmt.Errorf("one of %s or %s is required: this method has to be told which lines to remove", lineParamRegexp, lineParamLine)
	}
	// Ansible accepts both and quietly uses regexp. Refused here, because a
	// task naming two different things to remove has one of them wrong, and
	// choosing for the author hides which one.
	if pattern != "" && text != "" {
		return req, fmt.Errorf("%s and %s both name what to remove: give one of them", lineParamRegexp, lineParamLine)
	}

	if text != "" {
		// A value carrying a newline can never match, since lines are compared
		// one at a time, so the task would report nothing removed forever
		// rather than failing.
		if strings.Contains(text, "\n") {
			return req, fmt.Errorf("%s contains a newline: lines are matched one at a time, and file.block.remove is what removes several together", lineParamLine)
		}
		req.line = text
		return req, nil
	}

	match, err := lineCompile(lineParamRegexp, pattern)
	if err != nil {
		return req, err
	}
	req.match = match
	return req, nil
}

// removeApply returns the lines that survive and how many did not.
//
// EVERY match goes, not just the first or the last. That is lineinfile's own
// behavior for state: absent and it is the only one that converges: leaving
// the second copy of a setting behind would mean the next run removes it and
// the run after that finds a third, so a file with duplicates would need as
// many runs as it had copies.
func removeApply(req removeParams, lines []string) ([]string, int) {
	kept := make([]string, 0, len(lines))
	for _, text := range lines {
		if removeMatches(req, text) {
			continue
		}
		kept = append(kept, text)
	}
	return kept, len(lines) - len(kept)
}

// removeMatches reports whether one line is one the task asked to remove.
//
// removeRequest has already guaranteed exactly one of the two ways of saying
// so is set, so this needs no third branch for "neither", which would be a
// branch that removed every line in the file if it were ever reached.
func removeMatches(req removeParams, text string) bool {
	if req.match != nil {
		return req.match.MatchString(text)
	}
	return text == req.line
}
