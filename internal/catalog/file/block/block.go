// Package block implements the two "file.block.*" Collection methods:
// file.block.set, which keeps a marked, multi-line region of a text file
// exactly as a runbook declares it, and file.block.remove, which takes
// that region away again.
//
// # The markers are the design, not decoration
//
// Every other way to manage a chunk of a configuration file has to guess
// where that chunk ends. A pattern match finds one line and knows
// nothing about the lines around it. An "append it if the text is
// missing" check finds the text and cannot tell an old copy of it from a
// new one. Both fail the same way the first time the managed text
// changes: the old copy stays where it is, the new one lands underneath
// it, and the file grows a stale duplicate on every edit.
//
// A marked block cannot do that, because the region carries its own
// boundaries on the device. The run that wrote it left a begin line
// above it and an end line below it, so a later run finds the exact
// bytes it is responsible for, replaces what is between them, and leaves
// every other line in the file alone. That is the whole method: read the
// file, find the two marker lines, edit the slice between them in Go,
// write the file back.
//
// It is also what gives this pair the completest undo in the file
// namespace. Because the markers say which text a run replaced, the run
// can hand back that exact text, and a run that added a block can hand
// back an instruction to remove it. See each method's Reversibility
// Notes for what that still does not cover.
//
// # The default marker says ANSIBLE, deliberately
//
// The default marker is "# {mark} ANSIBLE MANAGED BLOCK", the word
// ANSIBLE included. It reads oddly in a file this platform writes, and
// changing it would be worse than odd. A playbook converted to a runbook
// has to find the block its ansible.builtin.blockinfile task already
// wrote on the device. A different default would not find it, would
// append a second block below the first, and would leave the device
// carrying two managed regions that disagree, with a service reading
// whichever one came last. Ansible's vocabulary is this platform's
// vocabulary, and here that goes as far as a string literal.
//
// # What state means here, since Ansible spells it differently
//
// ansible.builtin.blockinfile is one module with a state parameter that
// is present or absent. This is two namespaced methods instead, which is
// the platform's rule (a method name says what it does), and it costs a
// converted playbook one rename per task rather than a rewrite.
package block

import (
	"context"
	"fmt"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remotefile"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// The fully qualified names of the two methods in this package.
//
// They are constants because each method NAMES THE OTHER at run time:
// setting a block where there was none undoes to a removal, and removing
// one undoes to a set. A typo in either of those strings would produce a
// journal entry a rollback engine could only fail on, and it would not
// be noticed until somebody tried to roll back.
const (
	blockFQCNSet    = "file.block.set"
	blockFQCNRemove = "file.block.remove"
)

// Parameter names, which are ansible.builtin.blockinfile's own names, so
// a person converting a playbook that writes a marked block is renaming
// nothing but the module itself.
//
// What is deliberately absent is worth listing, because a missing
// parameter reads as an oversight otherwise. insertafter, insertbefore,
// create, backup and validate are not implemented here: a block always
// lands at the end of a file that must already exist, and file.touch or
// file.copy is what makes a file exist. The file attribute parameters
// (mode, owner, group) are absent for a stronger reason. This method
// never changes them and takes real trouble to put them back after a
// rewrite (see blockRestoreAttributes), so accepting them would mean
// owning a second job that file.permissions already does properly.
const (
	blockParamPath        = "path"
	blockParamBlock       = "block"
	blockParamMarker      = "marker"
	blockParamMarkerBegin = "marker_begin"
	blockParamMarkerEnd   = "marker_end"
)

// Marker defaults, taken from ansible.builtin.blockinfile unchanged. See
// this package's own doc comment for why the word ANSIBLE stays in the
// default text.
const (
	blockDefaultMarker      = "# {mark} ANSIBLE MANAGED BLOCK"
	blockDefaultMarkerBegin = "BEGIN"
	blockDefaultMarkerEnd   = "END"

	// blockMark is the placeholder inside a marker that each end of the
	// block substitutes its own word into. One marker parameter carrying
	// a placeholder, rather than two whole marker parameters, is what
	// keeps the two lines looking like a matched pair on the device.
	blockMark = "{mark}"
)

// Stat keys this package reports under. path and block are also
// parameter names, deliberately: a task is asked for a path and a block
// and reports the path and the block that are there now, so one
// vocabulary covers the request, the report and the diff.
const (
	blockStatPath    = blockParamPath
	blockStatBlock   = blockParamBlock
	blockStatPresent = "present"
)

// blockMarkers is the pair of literal lines that delimit a managed block
// on a device, together with the three parameters they were built from.
//
// The three raw values are kept rather than thrown away after the two
// lines are computed, because an emitted inverse has to name them. A
// rollback that left them out and relied on these defaults would find a
// different region, or no region at all, if a later release ever changed
// one of them.
type blockMarkers struct {
	marker      string
	markerBegin string
	markerEnd   string

	// beginLine and endLine are the two lines exactly as they appear in
	// the file, with the placeholder already substituted.
	beginLine string
	endLine   string
}

// params returns the three marker parameters as an inverse's params map
// carries them, so a rollback engine finds the same region this run did
// without depending on any default.
func (m blockMarkers) params() map[string]any {
	return map[string]any{
		blockParamMarker:      m.marker,
		blockParamMarkerBegin: m.markerBegin,
		blockParamMarkerEnd:   m.markerEnd,
	}
}

// blockMarkerRequest resolves the marker, marker_begin and marker_end
// parameters into the two literal lines this package searches for and
// writes.
//
// It runs before any connection is opened. Every refusal below is a
// mistake in the runbook rather than a condition on the device, and one
// that costs a TCP connect, a key exchange and an authentication round
// before reporting itself is a slower answer to the same question.
func blockMarkerRequest(params map[string]any) (blockMarkers, error) {
	var none blockMarkers

	m := blockMarkers{
		marker:      blockDefaultMarker,
		markerBegin: blockDefaultMarkerBegin,
		markerEnd:   blockDefaultMarkerEnd,
	}

	// A slice rather than a map, so that a task getting two of them wrong
	// is refused for the same one every time. Map iteration order is
	// random, and an error message that moves between runs is one nobody
	// can write a test or a runbook fix against.
	for _, field := range []struct {
		key  string
		into *string
	}{
		{blockParamMarker, &m.marker},
		{blockParamMarkerBegin, &m.markerBegin},
		{blockParamMarkerEnd, &m.markerEnd},
	} {
		text, present, err := blockTextParam(params, field.key)
		if err != nil {
			return none, err
		}
		if !present {
			continue
		}
		// An explicitly empty marker piece is refused rather than quietly
		// replaced by the default. It is almost always a variable that
		// rendered empty, and either alternative is silent: honoring it
		// changes which lines delimit the block, and defaulting it writes
		// a marker the author did not ask for. A refusal is the only
		// answer that says what happened.
		if text == "" {
			return none, fmt.Errorf("%s is empty: give the text of the marker, or leave the parameter out to use the default", field.key)
		}
		*field.into = text
	}

	// Without the placeholder the two ends of the block are the same
	// line, and a region with no distinguishable end is a region nothing
	// can find twice. Checked before the lines are built so the message
	// names the real mistake rather than reporting that the two lines
	// happen to match.
	if !strings.Contains(m.marker, blockMark) {
		return none, fmt.Errorf("%s %q does not contain %s: the placeholder is what tells the two ends of the block apart, as in %q",
			blockParamMarker, m.marker, blockMark, blockDefaultMarker)
	}

	m.beginLine = strings.ReplaceAll(m.marker, blockMark, m.markerBegin)
	m.endLine = strings.ReplaceAll(m.marker, blockMark, m.markerEnd)

	if err := m.check(); err != nil {
		return none, err
	}
	return m, nil
}

// check refuses a marker pair this package could not find again.
//
// Each refusal is a line the file could never match, or could match far
// too much of.
func (m blockMarkers) check() error {
	for _, line := range []struct {
		key  string
		text string
	}{
		{blockParamMarkerBegin, m.beginLine},
		{blockParamMarkerEnd, m.endLine},
	} {
		// A marker is one line. A newline inside it would write two lines
		// and then match neither of them, and a carriage return is worse
		// because it is invisible: the marker would look identical to the
		// one an editor wrote and compare unequal forever.
		if strings.ContainsAny(line.text, "\n\r") {
			return fmt.Errorf("the %s marker line %q contains a line break: a marker is a single line, since it has to compare equal to one line of the file", line.key, line.text)
		}
		// A blank marker line matches every blank line in the file, and
		// blank lines are what configuration files are full of. The block
		// would be found somewhere arbitrary and edited there.
		if strings.TrimSpace(line.text) == "" {
			return fmt.Errorf("the %s marker line is blank: a blank line matches every empty line in the file, so the block could be found anywhere", line.key)
		}
	}

	// Identical ends mean the first marker is also the last, so there is
	// no region between them to read or replace.
	if m.beginLine == m.endLine {
		return fmt.Errorf("%s and %s both produce the marker line %q: the two ends of the block have to differ, or nothing can tell where it stops",
			blockParamMarkerBegin, blockParamMarkerEnd, m.beginLine)
	}
	return nil
}

// blockPathRequest reads the one parameter both methods require.
func blockPathRequest(params map[string]any) (string, error) {
	path, _, err := blockTextParam(params, blockParamPath)
	if err != nil {
		return "", err
	}
	if path == "" {
		return "", fmt.Errorf("%s is required", blockParamPath)
	}
	return path, nil
}

// blockTextParam reads one string parameter, reporting whether it was
// there at all and refusing a value that arrived as something other than
// text.
//
// sdk.StringParam is the usual reader and it treats a non-string as
// absent, which is the wrong answer here. A marker written unquoted in
// YAML can arrive as something other than a string, and a reader that
// shrugged at it would fall back to the default marker and edit a
// different region of the file than the author named. The presence flag
// is what lets an explicitly empty value be refused rather than silently
// defaulted.
func blockTextParam(params map[string]any, key string) (string, bool, error) {
	raw, present := params[key]
	if !present || raw == nil {
		return "", false, nil
	}
	text, ok := raw.(string)
	if !ok {
		return "", true, fmt.Errorf("%s is %T, not text: quote it in the runbook", key, raw)
	}
	return text, true, nil
}

// blockRequirePlainFile refuses anything at path that this method must
// not rewrite, and is the read that happens before any change.
//
// A symbolic link is the refusal that earns its place. remotefile.Write
// replaces a file by renaming a new one over it, so writing through a
// link would REPLACE THE LINK with a regular file and quietly detach the
// path from whatever it pointed at. Ansible follows the link instead;
// this platform refuses, because silently converting a link into a file
// is the kind of change nobody reviews a runbook for.
func blockRequirePlainFile(ctx context.Context, conn *remoteexec.Conn, path, fqcn string) (remotefile.Info, error) {
	info, err := remotefile.Stat(ctx, conn, path)
	if err != nil {
		return remotefile.Info{}, fmt.Errorf("%s: %w", fqcn, err)
	}

	switch info.Kind {
	case remotefile.KindFile:
		return info, nil
	case remotefile.KindAbsent:
		return info, fmt.Errorf("%s: %s does not exist: this method edits a file rather than creating one, and file.touch or file.copy is what creates", fqcn, path)
	case remotefile.KindSymlink:
		return info, fmt.Errorf("%s: %s is a symbolic link to %s: point the task at the target, since writing the file back would replace the link itself", fqcn, path, info.Target)
	default:
		return info, fmt.Errorf("%s: %s is a %s, not a regular file", fqcn, path, info.Kind)
	}
}

// blockRestoreAttributes puts back the mode, owner and group the file
// carried before it was rewritten.
//
// THIS IS NOT HOUSEKEEPING, it repairs a real side effect of how a file
// is written here. remotefile.Write creates a temporary with mktemp,
// which makes it readable only by its creator, and renames it over the
// target. The rename carries the temporary's inode, so the file that
// lands is owned by the account this task connected as and is mode 0600,
// whatever it was a moment earlier. Left alone, adding a line to
// /etc/hosts would make it unreadable to every service that reads it,
// and the task would report success.
//
// Ansible's atomic_move solves the same problem the same way, by copying
// the destination's attributes onto the replacement.
func blockRestoreAttributes(ctx context.Context, conn *remoteexec.Conn, path string, before remotefile.Info) error {
	// Read what the write left, rather than assuming mktemp's 0600. The
	// assumption is probably right and it is not worth being probably
	// right about the permissions of a file this platform just rewrote.
	now, err := remotefile.Stat(ctx, conn, path)
	if err != nil {
		return err
	}

	want := remotefile.Attributes{Mode: before.Mode, Owner: before.Owner, Group: before.Group}
	// The changed flag is discarded on purpose. It answers "did the
	// restore have to do anything", and the caller already knows the
	// answer to the only question a task reports on, which is whether the
	// CONTENT changed.
	_, err = remotefile.Apply(ctx, conn, path, want, now)
	return err
}

// blockRecordStats writes the stats both methods return.
//
// They describe the file as it is NOW rather than what the task asked
// for, which is the same thing on a successful run and is the difference
// that matters to a later task reading them: file.block.remove reports
// present false and an empty block, and a converged file.block.set
// reports the body that was already there.
func blockRecordStats(rc sdk.RunbookContext, path string, region blockRegion) error {
	for key, value := range map[string]any{
		blockStatPath:    path,
		blockStatPresent: region.found,
		blockStatBlock:   region.text(),
	} {
		if err := rc.SetStat(key, value); err != nil {
			return err
		}
	}
	return nil
}

// blockNewlineCaveat is the sentence an emitted inverse adds when the
// run gave the file a final newline it did not have before.
//
// It exists because of the rule that an inverse which cannot restore
// everything still says what it left out. Appending a block to a file
// whose last line had no newline has to add one, or the begin marker
// would be glued onto that last line. Undoing the run takes the block
// away again and leaves the newline, since a file's final newline is not
// something the markers delimit and no marker recorded that it was
// missing. One byte, and an operator comparing checksums after a
// rollback deserves to know which byte it is.
func blockNewlineCaveat(added bool) string {
	if !added {
		return ""
	}
	return " The file did not end with a newline before this run and does now; undoing the block does not take that newline away."
}
