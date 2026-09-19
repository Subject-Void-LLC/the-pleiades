package block

import (
	"context"
	"fmt"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remotefile"
)

// Finding the marked region in a file, and putting one back.
//
// Everything here works on a []string of lines rather than on the file's
// text. That is the point of the whole design: once the file is a list of
// lines, replacing a block is a slice operation that cannot corrupt the
// lines around it, and it happens in Go on this side rather than in a
// sed expression on the device where a metacharacter in the block would
// mean something.

// Diff keys, which are the two things worth recording about a marked
// region: whether it is there and what is between the markers.
//
// They are constants because the emitted inverse is built from the same
// two values, and a method that wrote "was_present" into a diff while an
// inverse read "present" would produce a record that looks complete and
// answers nothing.
const (
	blockDiffPresent = "present"
	blockDiffBlock   = "block"
)

// blockRegion is where a managed block sits in a file, and what is
// inside it.
//
// The zero value means "there is no such block here", which is a real
// answer rather than a missing one: it is what tells file.block.set to
// append and what tells file.block.remove that it has nothing to do.
type blockRegion struct {
	// found reports whether both marker lines were there.
	found bool

	// begin and end are the indexes of the two marker lines themselves,
	// so the body is everything strictly between them.
	begin int
	end   int

	// body is the block's own lines, without the markers.
	body []string
}

// text renders the body as one string, with no trailing newline.
//
// This is the canonical form the diff, the stats and an emitted inverse
// all carry, and it round-trips: feeding it back as the block parameter
// produces the same lines it came from, because blockBodyLines drops a
// single trailing newline and this adds none.
func (r blockRegion) text() string { return strings.Join(r.body, "\n") }

// state renders the region as one half of a diff.
func (r blockRegion) state() map[string]any {
	return map[string]any{
		blockDiffPresent: r.found,
		blockDiffBlock:   r.text(),
	}
}

// blockFind locates the one managed region in a file's lines.
//
// # Why every ambiguity here is an error rather than a guess
//
// Ansible takes the LAST begin marker and the LAST end marker it sees,
// and treats a begin marker with no end as "there is no block", which
// makes it append a second one below the orphan. Both behaviors are
// guesses about a file somebody has to live with afterwards, and both
// leave the file worse: one edits an arbitrary one of two regions and
// leaves the other stale forever, the other duplicates the block and
// leaves a dangling marker above it.
//
// A file with markers that do not pair up got that way somehow, by a
// hand edit or a run that was interrupted, and the person who has to fix
// it is much better served by a task that names what it found than by
// one that quietly picks an interpretation.
func blockFind(lines []string, m blockMarkers) (blockRegion, error) {
	var begins, ends []int
	for i, line := range lines {
		// A plain switch is safe because blockMarkers.check has already
		// refused a pair whose two lines are equal, so no line can count
		// as both ends of the block.
		switch line {
		case m.beginLine:
			begins = append(begins, i)
		case m.endLine:
			ends = append(ends, i)
		}
	}

	// Neither marker is the common case on a first run, and it is the one
	// answer here that is not a problem: the file simply has no managed
	// block yet.
	if len(begins) == 0 && len(ends) == 0 {
		return blockRegion{}, nil
	}

	if len(begins) > 1 {
		return blockRegion{}, fmt.Errorf("found %d copies of the begin marker %q (lines %s): only one managed block per marker can be kept correct, so remove the extra copies by hand",
			len(begins), m.beginLine, blockLineNumbers(begins))
	}
	if len(ends) > 1 {
		return blockRegion{}, fmt.Errorf("found %d copies of the end marker %q (lines %s): only one managed block per marker can be kept correct, so remove the extra copies by hand",
			len(ends), m.endLine, blockLineNumbers(ends))
	}
	if len(ends) == 0 {
		return blockRegion{}, fmt.Errorf("found the begin marker %q on line %d with no matching end marker %q: where the block stops is a guess, and appending a second block below an unclosed one is how a file ends up with two",
			m.beginLine, begins[0]+1, m.endLine)
	}
	if len(begins) == 0 {
		return blockRegion{}, fmt.Errorf("found the end marker %q on line %d with no matching begin marker %q: where the block starts is a guess",
			m.endLine, ends[0]+1, m.beginLine)
	}
	if ends[0] < begins[0] {
		return blockRegion{}, fmt.Errorf("the end marker is on line %d and the begin marker on line %d, so the block closes before it opens: the two are swapped or the file was edited by hand",
			ends[0]+1, begins[0]+1)
	}

	return blockRegion{found: true, begin: begins[0], end: ends[0], body: lines[begins[0]+1 : ends[0]]}, nil
}

// blockLineNumbers renders line indexes as the 1-based numbers an editor
// shows, so an operator can jump straight to them.
func blockLineNumbers(indexes []int) string {
	numbers := make([]string, 0, len(indexes))
	for _, i := range indexes {
		numbers = append(numbers, fmt.Sprint(i+1))
	}
	return strings.Join(numbers, ", ")
}

// blockSplitLines splits a file's contents into lines, reporting
// separately whether the file ended with a newline.
//
// That flag is why this is not simply strings.Split. A text file
// normally ends with a newline and Split would represent it as a final
// empty line, which would then be written back as a blank line and grow
// the file by one every run. A file that genuinely does NOT end with a
// newline is rarer and matters more: rewriting it with one is a change
// to a line the task was never asked to touch, so the flag is carried
// through and put back exactly as it was found.
func blockSplitLines(content string) ([]string, bool) {
	if content == "" {
		// An empty file has no lines at all, and counts as ending with a
		// newline so that appending a block to it does not have to add a
		// leading blank line first.
		return nil, true
	}
	trailing := strings.HasSuffix(content, "\n")
	if trailing {
		content = content[:len(content)-1]
	}
	return strings.Split(content, "\n"), trailing
}

// blockJoin is blockSplitLines in reverse.
func blockJoin(lines []string, trailing bool) string {
	// No lines is an empty file rather than a file holding one newline,
	// which is what removing a block that was the entire file leaves
	// behind.
	if len(lines) == 0 {
		return ""
	}
	joined := strings.Join(lines, "\n")
	if trailing {
		joined += "\n"
	}
	return joined
}

// blockBodyLines splits a block parameter into the lines that go between
// the markers.
//
// It is blockSplitLines with the trailing-newline flag dropped, which is
// the right call for a value that is not a file: a runbook's block
// written as a YAML block scalar ends with a newline and one written
// inline does not, and those two must produce the same region or a task
// would report changed forever after somebody reformatted the runbook.
func blockBodyLines(content string) []string {
	lines, _ := blockSplitLines(content)
	return lines
}

// blockObservation is a file as one run found it: its lines, whether it
// ended with a newline, and where the managed block is.
type blockObservation struct {
	lines    []string
	trailing bool
	region   blockRegion
}

// blockObserve reads the file and locates the managed block in it.
//
// It is called before acting and again afterwards, which is the point:
// the "after" half of a diff is built by READING THE DEVICE BACK rather
// than by repeating what the task asked for. Those two are the same on a
// run that worked and differ on exactly the runs worth catching, and a
// method that echoed the request back would report a block it had failed
// to write.
func blockObserve(ctx context.Context, conn *remoteexec.Conn, path string, m blockMarkers) (blockObservation, error) {
	// The presence flag remotefile.Read returns is deliberately dropped.
	// The caller has already established that a regular file is at this
	// path, so a false here could only mean the file vanished in the
	// moment since, and for the one question this function answers, where
	// the marked block is, a file that is gone and a file that is empty
	// give the same answer: there is no block. Branching on it would add a
	// path no test could reach, and an unreachable branch is a claim
	// nobody can check.
	content, _, err := remotefile.Read(ctx, conn, path)
	if err != nil {
		return blockObservation{}, err
	}

	lines, trailing := blockSplitLines(content)
	region, err := blockFind(lines, m)
	if err != nil {
		return blockObservation{}, fmt.Errorf("%s: %w", path, err)
	}
	return blockObservation{lines: lines, trailing: trailing, region: region}, nil
}

// blockPredict is what blockObserve would find in a file written as lines
// and trailing: the text is joined and split exactly as a write and a
// read-back would, and the region found by the same blockFind. A check's
// prediction is therefore the observation a real run would make, not a
// second description of it.
func blockPredict(lines []string, trailing bool, m blockMarkers) (blockObservation, error) {
	read, readTrailing := blockSplitLines(blockJoin(lines, trailing))
	region, err := blockFind(read, m)
	if err != nil {
		return blockObservation{}, err
	}
	return blockObservation{lines: read, trailing: readTrailing, region: region}, nil
}

// blockWithout returns the file's lines with the managed region cut out,
// and whether the result still ends with a newline.
//
// The newline answer is the only subtle part. Every line before the
// region was terminated by a newline in the original file, because
// something followed it. So cutting a region that ran to the end of the
// file leaves a file that ends with a newline even when the original did
// not, and the caller is told that happened so an emitted inverse can
// say so.
func blockWithout(before blockObservation) (lines []string, trailing bool, addedNewline bool) {
	kept := make([]string, 0, len(before.lines))
	kept = append(kept, before.lines[:before.region.begin]...)
	kept = append(kept, before.lines[before.region.end+1:]...)

	if before.region.end == len(before.lines)-1 && !before.trailing {
		return kept, true, true
	}
	return kept, before.trailing, false
}

// blockWith returns the file's lines with the managed region replaced by
// body, or with a new region holding body appended at the end.
//
// Appending at the end is where a block goes because insertafter and
// insertbefore are not implemented here; see this package's parameter
// list for what that leaves out.
func blockWith(before blockObservation, m blockMarkers, body []string) (lines []string, trailing bool, addedNewline bool) {
	region := make([]string, 0, len(body)+2)
	region = append(region, m.beginLine)
	region = append(region, body...)
	region = append(region, m.endLine)

	if before.region.found {
		// A fresh slice rather than appending into before.lines, which
		// would write the new region over the lines that follow the old
		// one in the same backing array.
		next := make([]string, 0, len(before.lines)+len(region))
		next = append(next, before.lines[:before.region.begin]...)
		next = append(next, region...)
		next = append(next, before.lines[before.region.end+1:]...)
		// Replacing in place leaves the end of the file exactly as it was,
		// including a last line with no newline when the block is that
		// last line.
		return next, before.trailing, false
	}

	next := make([]string, 0, len(before.lines)+len(region))
	next = append(next, before.lines...)
	next = append(next, region...)
	// A file whose last line has no newline gets one, because the
	// alternative is the begin marker glued onto the end of that line,
	// which is a corrupt file rather than a managed block.
	return next, true, !before.trailing
}
