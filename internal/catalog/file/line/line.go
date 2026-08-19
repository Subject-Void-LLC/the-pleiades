// Package line implements the two Collection methods that edit a single
// line of a text file on a device: "file.line.set" and "file.line.remove".
//
// They are ansible.builtin.lineinfile's two states, split into two FQCNs
// instead of one method taking a state parameter. That split is this
// project's namespacing principle doing real work rather than decoration:
// "make sure this line is there" and "make sure this line is gone" need
// different parameters, and one method covering both has to accept every
// parameter either could want and then refuse most of the combinations at
// run time. Two names let each one refuse in its own argument check, before
// anything connects.
//
// # Everything is decided in Go, and nothing is decided by sed
//
// Both methods read the whole file, work out the new text here, and write
// the whole file back. Sending "sed -i" with the task's own pattern in it
// is the obvious shortcut and it is wrong three separate ways. sed does not
// have one dialect: GNU sed, BusyBox sed and BSD sed disagree about +, ?,
// | and \b, so one runbook would edit two devices differently and neither
// result would be a bug anybody could find. A runbook's pattern would stop
// being a pattern and start being shell syntax the moment it held a slash
// or a quote. And sed -i cannot report whether it changed anything, so
// deciding whether the run converged would mean reading the file back
// afterwards anyway.
//
// # The cost, stated rather than hidden
//
// Reading the whole file means it crosses the network twice and sits in
// memory here, and the record a changed run writes carries the file's prior
// text as well. These methods are for configuration files, which are
// kilobytes. A log file is not what they are for.
package line

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remotefile"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// Parameter names, which are ansible.builtin.lineinfile's own names. This
// platform is a superset of Ansible rather than a new vocabulary, so
// somebody converting a playbook that sets a line in a file renames
// nothing.
//
// state is deliberately missing: the FQCN carries it. Everything else
// lineinfile accepts and these methods do not (backrefs, search_string,
// firstmatch, create, backup, validate) is absent because it is not
// implemented, and each method's Doc says so by name rather than leaving a
// reader to discover it.
const (
	lineParamPath         = "path"
	lineParamLine         = "line"
	lineParamRegexp       = "regexp"
	lineParamInsertAfter  = "insertafter"
	lineParamInsertBefore = "insertbefore"
)

// Stat names these methods report under, which are lineinfile's own return
// names for the same reason the parameters are: a converted playbook's
// later tasks keep reading what they already read.
const (
	lineStatPath  = "path"
	lineStatMsg   = "msg"
	lineStatFound = "found"
)

// The two anchor words lineinfile gives a meaning other than "pattern".
// They are compared exactly and case sensitively, which is how Ansible
// compares them, so a lower case "eof" is a pattern and matches lines
// containing that text.
const (
	lineAnchorEOF = "EOF"
	lineAnchorBOF = "BOF"
)

// The inverse both methods emit is a file.copy that writes the file's prior
// text back, so these are ansible.builtin.copy's parameter names.
//
// file.copy is registered and declared and is NOT implemented yet, which
// means the instruction recorded here is one nothing can run today. That is
// the right way round rather than a reason to wait: the prior text exists
// only while the forward run is holding it, so a record written after
// file.copy lands would be missing it for every run that already happened.
const (
	lineInverseFQCN      = "file.copy"
	lineCopyParamDest    = "dest"
	lineCopyParamContent = "content"
	lineCopyParamMode    = "mode"
	lineCopyParamOwner   = "owner"
	lineCopyParamGroup   = "group"
)

// lineDiffContent is the key each half of the recorded diff carries the
// file's whole text under, alongside the keys remotefile.Info.Map writes.
const lineDiffContent = "content"

// lineTextParam reads one string parameter, refusing a value that arrived
// as something other than text.
//
// sdk.StringParam is the usual reader and it treats a non-string as absent,
// which is the wrong answer here. A pattern written as regexp: 8080 without
// quotes is the number 8080 once YAML has decoded it, and a reader that
// shrugged at that would go on to report "one of regexp or line is
// required" and send the author hunting for a parameter they did write.
func lineTextParam(params map[string]any, key string) (string, error) {
	raw, present := params[key]
	if !present || raw == nil {
		return "", nil
	}
	text, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("%s is %T, not text: quote it in the runbook, since YAML turns an unquoted value into a number or a boolean rather than the characters you wrote", key, raw)
	}
	return text, nil
}

// lineCompile builds one of the task's patterns, naming the parameter and
// the dialect when it will not compile.
//
// Go's regexp is RE2 and Ansible's is Python's re, and the difference is
// worth stating in the error rather than leaving somebody to discover it:
// RE2 has no backreferences and no lookaround, so a pattern carried over
// from a playbook that used either is refused here instead of quietly
// matching something else. Everything a lineinfile pattern normally holds,
// anchors, character classes, alternation and repetition, is spelled
// identically in both.
func lineCompile(key, pattern string) (*regexp.Regexp, error) {
	compiled, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("%s %q will not compile: %w (patterns here are RE2, which has no backreferences and no lookaround)", key, pattern, err)
	}
	return compiled, nil
}

// lineSplit breaks a file's text into lines and reports whether the file
// ended with a newline.
//
// That flag is the whole reason this is a function rather than a call to
// strings.Split. A text file conventionally ends with a newline and plenty
// do not, and a method that imposed one convention on every file it touched
// would rewrite files nobody asked it to change: asking for a line that is
// already present in a file with no final newline would render different
// bytes than were read, so the task would report a change and write, once,
// for nothing. Ansible's lineinfile does exactly that. This does not.
//
// A line keeps its carriage return, if it has one. Splitting on the newline
// byte alone is what lets a file with Windows endings round trip byte for
// byte, and the price is that a pattern or a line meant to match one has to
// carry the carriage return too. Stripping it would be the opposite trade:
// easier to match, and every line ending in the file silently rewritten.
func lineSplit(content string) ([]string, bool) {
	if content == "" {
		// No lines at all, and the flag is true so the first line added to an
		// empty file gets the newline a text file is expected to end with.
		// lineRender still turns no lines back into no bytes, so an empty file
		// that stays empty is not rewritten by the flag being set here.
		return nil, true
	}
	trailing := strings.HasSuffix(content, "\n")
	return strings.Split(strings.TrimSuffix(content, "\n"), "\n"), trailing
}

// lineRender turns lines back into a file's text, restoring the trailing
// newline only when the file had one.
//
// It is the exact inverse of lineSplit, and that is load bearing rather
// than tidy. Both methods decide whether they changed anything by comparing
// the rendered text against the bytes they read, so a render that did not
// round trip would report a change on a file it had not touched, and the
// change it then wrote would be real.
func lineRender(lines []string, trailing bool) string {
	if len(lines) == 0 {
		return ""
	}
	text := strings.Join(lines, "\n")
	if trailing {
		text += "\n"
	}
	return text
}

// lineFile is one read of the target: what the path is, and, when it is a
// regular file, its whole text already split into lines.
type lineFile struct {
	// info is what remotefile.Stat reported. It is the prior state the
	// emitted inverse restores the mode, owner and group from, and the only
	// chance to capture them: once the file has been rewritten, what it
	// carried before is gone.
	info remotefile.Info

	// content is the text exactly as it was read, kept beside lines because
	// "did this run change anything" is answered by comparing rendered text
	// against this, byte for byte, rather than by trusting a flag threaded
	// through the editing logic.
	content string

	lines    []string
	trailing bool
}

// lineLoad reads the path and refuses anything that is not a regular file.
//
// It decides the kind from the stat rather than letting remotefile.Read
// answer, because Read's own probe is `[ -f ]`, which follows a symbolic
// link and reports a directory as simply not there. Both of those answers
// would be wrong in a way an operator could not act on: a confident "no
// such file" about a directory that is plainly present, and a successful
// edit of whatever a link happened to point at.
//
// An absent path is NOT an error here. The two methods disagree about what
// it means, since setting a line in a file that does not exist is a mistake
// and removing one from it is already done, so each decides for itself.
func lineLoad(ctx context.Context, conn *remoteexec.Conn, path string) (lineFile, error) {
	info, err := remotefile.Stat(ctx, conn, path)
	if err != nil {
		return lineFile{}, err
	}
	if !info.Exists() {
		return lineFile{info: info}, nil
	}

	// A link is refused rather than followed. remotefile.Write replaces a
	// path by renaming a temporary over it, so writing "through" a link would
	// leave a regular file where the link was, and everything else that
	// resolved through that link would stop seeing later edits without
	// anything reporting an error.
	if info.Kind == remotefile.KindSymlink {
		return lineFile{}, fmt.Errorf("%s is a symbolic link to %s: point the task at the target, since writing this path replaces it and would leave a regular file where the link is", path, info.Target)
	}
	if info.Kind != remotefile.KindFile {
		return lineFile{}, fmt.Errorf("%s is a %s rather than a regular file: these methods edit the text inside a file", path, info.Kind)
	}

	// The "was it found" answer is discarded on purpose. The stat one round
	// trip ago already established a regular file is here, so a false here
	// means something removed the file in between. That race cannot be closed
	// from this side, only narrowed, and both methods behave sanely if it
	// happens: removing lines from no text finds none and writes nothing,
	// and setting a line rewrites the file the way any read-modify-write
	// would.
	content, _, err := remotefile.Read(ctx, conn, path)
	if err != nil {
		return lineFile{}, err
	}

	lines, trailing := lineSplit(content)
	return lineFile{info: info, content: content, lines: lines, trailing: trailing}, nil
}

// lineWrite replaces the file's text, puts back the mode, owner and group
// it had, and returns what the path really looks like and really holds
// afterwards.
//
// # Restoring the attributes is not optional
//
// remotefile.Write is atomic because it writes a temporary in the same
// directory and renames it over the target, and a rename replaces the
// inode. mktemp creates that temporary as 0600 owned by the connecting
// account, so what is left behind carries the TEMPORARY's mode and owner,
// not the file's. Editing one line of /etc/hosts would leave it readable
// only by the account that ran the task, and the task would report success.
// Nothing else in this namespace hits this, because nothing else replaces a
// file that already exists.
//
// The repair is the shared primitive rather than a second write path:
// re-stat, then hand remotefile.Apply the attributes that were there. Apply
// compares before it acts, so a file that already matches, which is what a
// 0600 file owned by the connecting account does, costs no command at all.
//
// # Why it reads the device back afterwards
//
// The first re-stat is what Apply has to compare against, and it has to be
// the state AFTER the write rather than the state before it. The second
// happens only when Apply really changed something. The final read is what
// makes the after half of the recorded diff a description of the device
// instead of an echo of what this function asked for, which is the failure
// shape that hides longest: a method that records its own request looks
// correct in every test that reads only what it recorded.
func lineWrite(ctx context.Context, conn *remoteexec.Conn, path, content string, before remotefile.Info) (remotefile.Info, string, error) {
	if err := remotefile.Write(ctx, conn, path, []byte(content)); err != nil {
		return remotefile.Info{}, "", err
	}

	written, err := remotefile.Stat(ctx, conn, path)
	if err != nil {
		return remotefile.Info{}, "", fmt.Errorf("%w: the new text is already in place, so fix the cause and re-run rather than expecting the file to be untouched", err)
	}

	restored, err := remotefile.Apply(ctx, conn, path, remotefile.Attributes{
		Mode:  before.Mode,
		Owner: before.Owner,
		Group: before.Group,
	}, written)
	if err != nil {
		return remotefile.Info{}, "", fmt.Errorf("%w: the new text is in place but the file now carries the permissions of the temporary it was written through, so fix the cause and re-run rather than leaving it that way", err)
	}

	after := written
	if restored {
		if after, err = remotefile.Stat(ctx, conn, path); err != nil {
			return remotefile.Info{}, "", fmt.Errorf("%w: the new text and the original permissions are both in place", err)
		}
	}

	readBack, _, err := remotefile.Read(ctx, conn, path)
	if err != nil {
		return remotefile.Info{}, "", fmt.Errorf("%w: the write itself succeeded, so this is a failure to confirm it rather than a failure to make it", err)
	}
	return after, readBack, nil
}

// lineState renders one half of the recorded diff: everything
// remotefile.Info reports about the path, plus the file's whole text.
//
// The text is left out for a path that is not there, rather than recorded
// as an empty string, so a reader can tell "the file was absent" from "the
// file was empty". Those are different states and they undo differently.
func lineState(info remotefile.Info, content string) map[string]any {
	state := info.Map()
	if info.Exists() {
		state[lineDiffContent] = content
	}
	return state
}

// lineRecordDiff writes the before and after halves of a run that changed
// something.
//
// The file's text is in there deliberately and it is the expensive part of
// this record. A diff of a line edit that said only "size 412 became 431"
// answers nothing an operator reviewing the change wants to know, which is
// the one job a diff has, and the whole text is what Ansible's own
// lineinfile diff carries. The price is that a run over a large file writes
// that file into the record twice, and a third time in the emitted inverse.
func lineRecordDiff(rc sdk.RunbookContext, before lineFile, afterInfo remotefile.Info, afterContent string) error {
	return sdk.RecordDiff(rc, sdk.Diff{
		Before: lineState(before.info, before.content),
		After:  lineState(afterInfo, afterContent),
	})
}

// lineRecordInverse writes the instruction that undoes this run: a
// file.copy putting the whole prior text back, with the mode, owner and
// group the file had.
//
// # Why the whole file, rather than the opposite line edit
//
// The tempting inverse of file.line.set is a file.line.remove, and it is
// wrong every way it can be. A set may have REPLACED a line, and removing
// the new one does not bring the old one back. A set may have added a line
// that was already present elsewhere, and "remove the line" then removes
// both. An insert placed by insertbefore cannot be found again by a remove
// that has no idea where it went. file.line.remove has the mirror problem:
// it can take several lines out of several positions, and putting them back
// with file.line.set would pile them all at the end.
//
// So the inverse is the file. It is the only thing that reliably restores
// what was there, and it is honest about its cost rather than clever about
// avoiding it.
//
// # Why it carries the attributes too
//
// Restoring the text means replacing the file, and replacing a file is
// exactly what loses its mode and owner (see lineWrite). An inverse that
// restored only the text would itself be the thing that changed the
// permissions, which is a rollback causing the damage it was run to repair.
//
// # What it does not restore
//
// The modification time, which nothing in this platform can set. A file
// returned to its prior bytes with a later mtime is what a record of an
// observation can honestly promise.
func lineRecordInverse(rc sdk.RunbookContext, path string, before lineFile) error {
	return sdk.RecordInverse(rc, sdk.Inverse{
		FQCN: lineInverseFQCN,
		Params: map[string]any{
			lineCopyParamDest:    path,
			lineCopyParamContent: before.content,
			lineCopyParamMode:    before.info.Mode,
			lineCopyParamOwner:   before.info.Owner,
			lineCopyParamGroup:   before.info.Group,
		},
		Description: fmt.Sprintf("Write %s back to the %d bytes it held before this task ran, owned by %s:%s with mode %s.",
			path, len(before.content), before.info.Owner, before.info.Group, before.info.Mode),
	})
}

// lineRecordStats writes the stats a method returns.
//
// One helper for both of them so the two cannot drift into recording the
// same idea under two spellings, which is the failure a later task's
// condition inherits silently.
func lineRecordStats(rc sdk.RunbookContext, stats map[string]any) error {
	for key, value := range stats {
		if err := rc.SetStat(key, value); err != nil {
			return err
		}
	}
	return nil
}
