package block_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/file/block"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// TestSet_Registered proves the method registered itself as implemented,
// and that it claims the reversibility its whole design is built on.
//
// The Notes are pinned as well as the flag. A method that answers "yes,
// this can be undone" is making a promise an operator will one day rely
// on, and the part of the promise that cannot be read off the boolean is
// what the undo does NOT cover. The comparison against file.line is
// asserted because it is the one sentence explaining why this pair is
// stronger than its neighbor, and a Notes field that lost it would still
// look complete.
func TestSet_Registered(t *testing.T) {
	d, ok := collection.Lookup("file.block.set")
	if !ok {
		t.Fatalf("collection.Lookup(%q) found nothing; did this package's init() run?", "file.block.set")
	}
	if d.Manifest.Status != collection.StatusImplemented {
		t.Errorf("Manifest.Status = %v, want %v", d.Manifest.Status, collection.StatusImplemented)
	}
	if d.Invoke == nil {
		t.Error("Invoke is nil, so the dispatcher has nothing to call")
	}
	if !d.Manifest.Reversibility.Reversible {
		t.Error("Reversibility.Reversible = false, but this method emits a real inverse")
	}
	if !strings.Contains(d.Manifest.Reversibility.Notes, "file.line") {
		t.Errorf("Reversibility.Notes = %q, want it to say why the markers make this stronger than file.line", d.Manifest.Reversibility.Notes)
	}
}

// TestSet_RefusesAMissingPath proves the one always-required parameter
// is really required, and that the refusal happens before any
// connection.
//
// A nil device would fail in connect, so reaching the end of this test
// with the expected message is itself the proof that nothing tried to
// dial.
func TestSet_RefusesAMissingPath(t *testing.T) {
	for _, params := range []map[string]any{
		nil,
		{"block": "x\n"},
		{"path": "", "block": "x\n"},
		{"path": nil, "block": "x\n"},
	} {
		_, err := block.Set(context.Background(), nil, nil, params)
		if err == nil {
			t.Fatalf("params %v: expected a refusal, got nil", params)
		}
		if !strings.Contains(err.Error(), "path is required") {
			t.Errorf("params %v: error = %q, want it to name the missing parameter", params, err)
		}
	}
}

// TestSet_RefusesAnEmptyBlock covers this method's one deliberate
// divergence from ansible.builtin.blockinfile.
//
// There, an empty block means "remove it", because state is a single
// parameter and that is the only way to say it. Here removal has its own
// FQCN, so an empty block is nearly always a variable that rendered
// empty, and honoring Ansible's reading would delete a region of a
// configuration file because a lookup returned nothing. The refusal has
// to name file.block.remove, or an author who really did want a removal
// is left guessing.
func TestSet_RefusesAnEmptyBlock(t *testing.T) {
	for _, params := range []map[string]any{
		{"path": "/etc/hosts"},
		{"path": "/etc/hosts", "block": ""},
		{"path": "/etc/hosts", "block": nil},
	} {
		_, err := block.Set(context.Background(), nil, nil, params)
		if err == nil {
			t.Fatalf("params %v: expected a refusal, got nil", params)
		}
		if !strings.Contains(err.Error(), "block is required") {
			t.Errorf("params %v: error = %q, want it to name the missing parameter", params, err)
		}
		if !strings.Contains(err.Error(), "file.block.remove") {
			t.Errorf("params %v: error = %q, want it to name the method that removes a block", params, err)
		}
	}
}

// TestSet_RefusesAParameterThatIsNotText proves a value that arrived as
// something other than text is refused by name.
//
// Treating a non-string as absent, the way sdk.StringParam does, is the
// wrong answer for a marker in particular: the task would silently fall
// back to the default marker and manage a different region of the file
// than the author named.
func TestSet_RefusesAParameterThatIsNotText(t *testing.T) {
	tests := []struct {
		name   string
		params map[string]any
	}{
		{name: "path", params: map[string]any{"path": 42, "block": "x\n"}},
		{name: "block", params: map[string]any{"path": "/etc/hosts", "block": 42}},
		{name: "marker", params: map[string]any{"path": "/etc/hosts", "block": "x\n", "marker": 42}},
		{name: "marker_begin", params: map[string]any{"path": "/etc/hosts", "block": "x\n", "marker_begin": 42}},
		{name: "marker_end", params: map[string]any{"path": "/etc/hosts", "block": "x\n", "marker_end": 42}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := block.Set(context.Background(), nil, nil, tt.params)
			if err == nil {
				t.Fatal("expected a refusal, got nil")
			}
			if !strings.Contains(err.Error(), tt.name+" is int, not text") {
				t.Errorf("error = %q, want it to name %q and its type", err, tt.name)
			}
			if !strings.Contains(err.Error(), "quote it") {
				t.Errorf("error = %q, want it to say to quote the value", err)
			}
		})
	}
}

// TestSet_RefusesAnEmptyMarkerParameter proves a marker parameter given
// as an empty string is refused rather than quietly defaulted.
//
// Both alternatives are silent: honoring it writes a marker line the
// author did not intend, and replacing it with the default writes a
// different one. Only a refusal says a variable rendered empty.
func TestSet_RefusesAnEmptyMarkerParameter(t *testing.T) {
	for _, key := range []string{"marker", "marker_begin", "marker_end"} {
		params := map[string]any{"path": "/etc/hosts", "block": "x\n", key: ""}

		_, err := block.Set(context.Background(), nil, nil, params)
		if err == nil {
			t.Fatalf("%s: expected a refusal, got nil", key)
		}
		if !strings.Contains(err.Error(), key+" is empty") {
			t.Errorf("%s: error = %q, want it to name the empty parameter", key, err)
		}
	}
}

// TestSet_RefusesAMarkerWithoutThePlaceholder proves a marker that
// cannot tell its two ends apart is refused up front.
//
// Without {mark} the begin and end lines are the same text, so the block
// has no findable end, and the next run would read the region as empty
// or as running to the wrong line.
func TestSet_RefusesAMarkerWithoutThePlaceholder(t *testing.T) {
	params := map[string]any{"path": "/etc/hosts", "block": "x\n", "marker": "# PLEIADES BLOCK"}

	_, err := block.Set(context.Background(), nil, nil, params)
	if err == nil {
		t.Fatal("expected a refusal, got nil")
	}
	if !strings.Contains(err.Error(), "{mark}") {
		t.Errorf("error = %q, want it to name the missing placeholder", err)
	}
}

// TestSet_RefusesAMarkerLineThatIsNotOneLine proves a marker carrying a
// line break is refused.
//
// A marker has to compare equal to one line of the file. A newline
// inside it would write two lines and match neither, and a carriage
// return is worse: it is invisible, so the marker would look right in
// the runbook and never match.
func TestSet_RefusesAMarkerLineThatIsNotOneLine(t *testing.T) {
	for _, marker := range []string{"# {mark} FIRST\n# SECOND", "# {mark} BLOCK\r"} {
		params := map[string]any{"path": "/etc/hosts", "block": "x\n", "marker": marker}

		_, err := block.Set(context.Background(), nil, nil, params)
		if err == nil {
			t.Fatalf("marker %q: expected a refusal, got nil", marker)
		}
		if !strings.Contains(err.Error(), "contains a line break") {
			t.Errorf("marker %q: error = %q, want it to say a marker is one line", marker, err)
		}
	}
}

// TestSet_RefusesABlankMarkerLine proves a marker that renders to
// whitespace is refused, at either end.
//
// A blank marker line matches every empty line in the file, and
// configuration files are full of them, so the block would be found and
// edited somewhere arbitrary. Both ends are checked because each is its
// own comparison and a copied check can be wired to the wrong value.
func TestSet_RefusesABlankMarkerLine(t *testing.T) {
	tests := []struct {
		name   string
		params map[string]any
	}{
		{name: "marker_begin", params: map[string]any{"marker": "{mark}", "marker_begin": " "}},
		{name: "marker_end", params: map[string]any{"marker": "{mark}", "marker_end": "  "}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := map[string]any{"path": "/etc/hosts", "block": "x\n"}
			for key, value := range tt.params {
				params[key] = value
			}

			_, err := block.Set(context.Background(), nil, nil, params)
			if err == nil {
				t.Fatal("expected a refusal, got nil")
			}
			if !strings.Contains(err.Error(), "the "+tt.name+" marker line is blank") {
				t.Errorf("error = %q, want it to name the blank end of the block", err)
			}
		})
	}
}

// TestSet_RefusesMarkersThatMatchEachOther proves two ends that render
// to the same line are refused.
//
// It is the same hazard the missing placeholder produces, reached the
// other way, and the message has to name the two parameters rather than
// the marker: the marker is fine and the two words fed into it are not.
func TestSet_RefusesMarkersThatMatchEachOther(t *testing.T) {
	params := map[string]any{
		"path":         "/etc/hosts",
		"block":        "x\n",
		"marker_begin": "HERE",
		"marker_end":   "HERE",
	}

	_, err := block.Set(context.Background(), nil, nil, params)
	if err == nil {
		t.Fatal("expected a refusal, got nil")
	}
	if !strings.Contains(err.Error(), "have to differ") {
		t.Errorf("error = %q, want it to say the two ends must differ", err)
	}
}

// TestSet_RefusesABlockContainingAMarkerLine proves a body carrying a
// copy of either marker is refused.
//
// Writing it would put a third marker line in the file, and the NEXT run
// would then read the block as ending early or as being duplicated. The
// refusal happens while the runbook can still be fixed rather than on
// the run after next, which is when the damage would otherwise show up.
func TestSet_RefusesABlockContainingAMarkerLine(t *testing.T) {
	for _, body := range []string{
		"first\n" + blockTestBegin + "\nsecond\n",
		"first\n" + blockTestEnd + "\n",
	} {
		params := map[string]any{"path": "/etc/hosts", "block": body}

		_, err := block.Set(context.Background(), nil, nil, params)
		if err == nil {
			t.Fatalf("body %q: expected a refusal, got nil", body)
		}
		if !strings.Contains(err.Error(), "contains the marker line") {
			t.Errorf("body %q: error = %q, want it to say the body carries a marker", body, err)
		}
	}
}

// TestSet_AppendsTheBlockWhenTheFileHasNone is the create-equivalent
// path: the run that finds no managed region and adds one.
//
// It asserts the whole file byte for byte, because "the block is in
// there somewhere" is not the property that matters. The lines that were
// already in the file have to be untouched and in order, the markers
// have to surround exactly the requested body, and the file has to end
// the way a text file ends.
func TestSet_AppendsTheBlockWhenTheFileHasNone(t *testing.T) {
	server := startBlockServer(t)
	rc := newBlockContext(server)
	path := blockFile(t, "alpha\nomega\n")

	result, err := block.Set(context.Background(), rc, newBlockTarget(server), blockParams(path, map[string]any{
		"block": "one\ntwo\n",
	}))
	if err != nil {
		t.Fatalf("Set: %v", err)
	}
	if !result.Changed {
		t.Error("a run that added a block reported no change")
	}

	want := "alpha\nomega\n" + blockTestBegin + "\none\ntwo\n" + blockTestEnd + "\n"
	if got := blockContents(t, path); got != want {
		t.Errorf("the file holds\n%q\nwant\n%q", got, want)
	}

	if got := rc.stats["present"]; got != true {
		t.Errorf("present stat = %v, want true", got)
	}
	if got := rc.stats["block"]; got != "one\ntwo" {
		t.Errorf("block stat = %v, want the body with no trailing newline", got)
	}
	if got := rc.stats["path"]; got != path {
		t.Errorf("path stat = %v, want %q", got, path)
	}

	before := blockDiffHalf(t, rc, "before")
	if got := before["present"]; got != false {
		t.Errorf("diff before present = %v, want false: the file carried no block", got)
	}
	after := blockDiffHalf(t, rc, "after")
	if got := after["block"]; got != "one\ntwo" {
		t.Errorf("diff after block = %v, want the body read back from the device", got)
	}

	// A run that added a block undoes to a REMOVAL, not to a set with an
	// empty body: there was nothing there before, so restoring means
	// leaving nothing.
	fqcn, params, description := blockInverse(t, rc)
	if fqcn != "file.block.remove" {
		t.Errorf("inverse fqcn = %q, want file.block.remove", fqcn)
	}
	if params["path"] != path {
		t.Errorf("inverse path = %v, want %q", params["path"], path)
	}
	if _, ok := params["block"]; ok {
		t.Errorf("the inverse carries a block parameter %v, but a removal takes none", params["block"])
	}
	if !strings.Contains(description, "carried no such block before") {
		t.Errorf("inverse description = %q, want it to say the file had no block", description)
	}
}

// TestSet_PutsTheFilesModeBack proves the method repairs the side effect
// of how a file is written here.
//
// remotefile.Write creates a temporary with mktemp, which is readable
// only by its creator, and renames it over the target, so the file that
// lands is mode 0600 whatever it was before. Without the repair, adding
// a line to a file every service reads would make it unreadable to all
// of them, and the task would report success.
func TestSet_PutsTheFilesModeBack(t *testing.T) {
	server := startBlockServer(t)
	path := blockFile(t, "alpha\n")

	if _, err := block.Set(context.Background(), newBlockContext(server), newBlockTarget(server), blockParams(path, map[string]any{
		"block": "one\n",
	})); err != nil {
		t.Fatalf("Set: %v", err)
	}

	if got := blockMode(t, path); got != "0644" {
		t.Errorf("the file is %s on disk, want the 0644 it started as: the rewrite left it wearing a temporary's permissions", got)
	}
}

// TestSet_AddsTheMissingFinalNewline proves a file whose last line has
// no newline is handled rather than corrupted, and that the emitted
// inverse admits what it cannot put back.
//
// Appending straight onto such a file would glue the begin marker to the
// end of that last line. Adding the newline is the only sane answer, and
// it is a change to a line the task was never asked to touch, so an
// operator reading a rollback plan is told the undo will not remove it.
func TestSet_AddsTheMissingFinalNewline(t *testing.T) {
	server := startBlockServer(t)
	rc := newBlockContext(server)
	path := blockFile(t, "alpha")

	if _, err := block.Set(context.Background(), rc, newBlockTarget(server), blockParams(path, map[string]any{
		"block": "one\n",
	})); err != nil {
		t.Fatalf("Set: %v", err)
	}

	want := "alpha\n" + blockTestBegin + "\none\n" + blockTestEnd + "\n"
	if got := blockContents(t, path); got != want {
		t.Errorf("the file holds\n%q\nwant\n%q", got, want)
	}

	_, _, description := blockInverse(t, rc)
	if !strings.Contains(description, "did not end with a newline") {
		t.Errorf("inverse description = %q, want it to say the undo leaves the added newline behind", description)
	}
}

// TestSet_AppendsToAnEmptyFile proves an empty file gets the block and
// nothing else.
//
// The trap it guards is treating an empty file as one empty line, which
// would leave a blank line above the begin marker on every file this
// method ever creates a block in.
func TestSet_AppendsToAnEmptyFile(t *testing.T) {
	server := startBlockServer(t)
	rc := newBlockContext(server)
	path := blockFile(t, "")

	if _, err := block.Set(context.Background(), rc, newBlockTarget(server), blockParams(path, map[string]any{
		"block": "one\n",
	})); err != nil {
		t.Fatalf("Set: %v", err)
	}

	want := blockTestBegin + "\none\n" + blockTestEnd + "\n"
	if got := blockContents(t, path); got != want {
		t.Errorf("the file holds\n%q\nwant\n%q", got, want)
	}

	// An empty file already ended the way a text file ends, so nothing
	// was added and the inverse has nothing to admit.
	if _, _, description := blockInverse(t, rc); strings.Contains(description, "did not end with a newline") {
		t.Errorf("inverse description = %q, want no newline caveat: an empty file needed none", description)
	}
}

// TestSet_ReplacesTheBlockInPlace is the property the markers exist for.
//
// The replacement has to land between the markers, where the block
// already was, leaving the lines above and below it exactly as they
// were. The failure this rules out is the one every append-if-missing
// implementation has: the old copy stays and the new one lands at the
// end of the file.
func TestSet_ReplacesTheBlockInPlace(t *testing.T) {
	server := startBlockServer(t)
	rc := newBlockContext(server)
	path := blockFile(t, "alpha\n"+blockTestBegin+"\nold one\nold two\n"+blockTestEnd+"\nomega\n")

	result, err := block.Set(context.Background(), rc, newBlockTarget(server), blockParams(path, map[string]any{
		"block": "new one\nnew two\nnew three\n",
	}))
	if err != nil {
		t.Fatalf("Set: %v", err)
	}
	if !result.Changed {
		t.Error("a run that replaced the block reported no change")
	}

	want := "alpha\n" + blockTestBegin + "\nnew one\nnew two\nnew three\n" + blockTestEnd + "\nomega\n"
	if got := blockContents(t, path); got != want {
		t.Errorf("the file holds\n%q\nwant\n%q", got, want)
	}

	if got := blockDiffHalf(t, rc, "before")["block"]; got != "old one\nold two" {
		t.Errorf("diff before block = %v, want the text that was replaced: it is what an undo restores", got)
	}

	// Replacing a block undoes to a SET carrying the old body, not to a
	// removal: the file had a block before this run and must have one
	// after the undo.
	fqcn, params, description := blockInverse(t, rc)
	if fqcn != "file.block.set" {
		t.Errorf("inverse fqcn = %q, want file.block.set", fqcn)
	}
	if params["block"] != "old one\nold two" {
		t.Errorf("inverse block = %v, want the body this run replaced", params["block"])
	}
	if !strings.Contains(description, "exactly as this run found it") {
		t.Errorf("inverse description = %q, want it to say the previous block comes back verbatim", description)
	}
}

// TestSet_KeepsAMissingFinalNewlineWhenItReplacesInPlace proves a
// replacement leaves the end of the file exactly as it found it.
//
// The file ends with the end marker and no newline. Adding one would be
// a change to a line the task was not asked about, and it is the sort of
// change that shows up as a whole-file diff in whatever reviews the
// device afterwards. Appending a block is the only case that has to add
// a newline, and this proves that case is not the general one.
func TestSet_KeepsAMissingFinalNewlineWhenItReplacesInPlace(t *testing.T) {
	server := startBlockServer(t)
	rc := newBlockContext(server)
	path := blockFile(t, "alpha\n"+blockTestBegin+"\nold\n"+blockTestEnd)

	if _, err := block.Set(context.Background(), rc, newBlockTarget(server), blockParams(path, map[string]any{
		"block": "new\n",
	})); err != nil {
		t.Fatalf("Set: %v", err)
	}

	want := "alpha\n" + blockTestBegin + "\nnew\n" + blockTestEnd
	if got := blockContents(t, path); got != want {
		t.Errorf("the file holds\n%q\nwant\n%q", got, want)
	}
	if _, _, description := blockInverse(t, rc); strings.Contains(description, "did not end with a newline") {
		t.Errorf("inverse description = %q, want no newline caveat: replacing in place added none", description)
	}
}

// TestSet_ConvergedRunReportsNoChangeAndWritesNothing is the single most
// important property this method has.
//
// The session budget is the assertion, not a decoration. A converged run
// needs exactly two sessions, the stat and the read; a run that went on
// to write the file would ask for a third and be refused, so this test
// fails with an error rather than passing while the device is rewritten
// on every run forever.
//
// The second run also writes the body in the OTHER spelling, without the
// trailing newline a YAML block scalar leaves. Those two mean the same
// region, and comparing them as strings rather than as lines is the
// classic way a module reports changed forever after somebody reformats
// a runbook.
func TestSet_ConvergedRunReportsNoChangeAndWritesNothing(t *testing.T) {
	first := startBlockServer(t)
	path := blockFile(t, "alpha\n")

	result, err := block.Set(context.Background(), newBlockContext(first), newBlockTarget(first), blockParams(path, map[string]any{
		"block": "one\ntwo\n",
	}))
	if err != nil {
		t.Fatalf("first Set: %v", err)
	}
	if !result.Changed {
		t.Fatal("the first run reported no change, so this test could prove nothing about the second")
	}
	written := blockContents(t, path)

	second := startBlockServerWithSessionBudget(t, 2)
	rc := newBlockContext(second)
	again, err := block.Set(context.Background(), rc, newBlockTarget(second), blockParams(path, map[string]any{
		"block": "one\ntwo",
	}))
	if err != nil {
		t.Fatalf("second Set: %v", err)
	}
	if again.Changed {
		t.Error("a converged run reported a change")
	}
	if got := blockContents(t, path); got != written {
		t.Errorf("the file holds\n%q\nwant it untouched at\n%q", got, written)
	}

	// A converged run still records a diff, and its halves have to match:
	// that is what tells a rollback engine this task changed nothing,
	// which an absent diff cannot express. The inverse, by contrast, must
	// be absent, because undoing nothing means doing nothing.
	before := blockDiffHalf(t, rc, "before")
	after := blockDiffHalf(t, rc, "after")
	if before["block"] != after["block"] || before["present"] != after["present"] {
		t.Errorf("a converged run recorded before %v and after %v, want them identical", before, after)
	}
	blockNoInverse(t, rc)
}

// TestSet_UsesTheMarkerItIsGiven proves all three marker parameters
// reach the file, and that the emitted inverse carries them.
//
// Every value here differs from its default on purpose: a test using the
// default markers would pass against a method that ignored the
// parameters entirely. The inverse carrying them is what lets a rollback
// find this region rather than the one the defaults describe.
func TestSet_UsesTheMarkerItIsGiven(t *testing.T) {
	server := startBlockServer(t)
	rc := newBlockContext(server)
	path := blockFile(t, "alpha\n")

	if _, err := block.Set(context.Background(), rc, newBlockTarget(server), blockParams(path, map[string]any{
		"block":        "one\n",
		"marker":       "; {mark} PLEIADES PROXY",
		"marker_begin": "OPEN",
		"marker_end":   "SHUT",
	})); err != nil {
		t.Fatalf("Set: %v", err)
	}

	want := "alpha\n; OPEN PLEIADES PROXY\none\n; SHUT PLEIADES PROXY\n"
	if got := blockContents(t, path); got != want {
		t.Errorf("the file holds\n%q\nwant\n%q", got, want)
	}

	_, params, _ := blockInverse(t, rc)
	for key, value := range map[string]string{
		"marker":       "; {mark} PLEIADES PROXY",
		"marker_begin": "OPEN",
		"marker_end":   "SHUT",
	} {
		if params[key] != value {
			t.Errorf("inverse %s = %v, want %q: a rollback that fell back to the defaults would edit a different region", key, params[key], value)
		}
	}
}

// TestSet_RefusesAnAbsentPath proves this method edits a file and never
// creates one.
//
// The refusal has to name what does create, because "does not exist" on
// its own reads as a mistake in the runbook's ordering rather than as a
// deliberate boundary between two methods.
func TestSet_RefusesAnAbsentPath(t *testing.T) {
	server := startBlockServer(t)
	path := filepath.Join(t.TempDir(), "not-there")

	_, err := block.Set(context.Background(), newBlockContext(server), newBlockTarget(server), blockParams(path, map[string]any{
		"block": "one\n",
	}))
	if err == nil {
		t.Fatal("an absent path was accepted, so this method created something")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("error = %q, want it to say the path is not there", err)
	}
	if !strings.Contains(err.Error(), "file.touch") {
		t.Errorf("error = %q, want it to name what does create a file", err)
	}
	if _, statErr := os.Lstat(path); !os.IsNotExist(statErr) {
		t.Errorf("os.Lstat(%s) = %v, want the path still absent", path, statErr)
	}
}

// TestSet_RefusesASymbolicLink proves a link is refused rather than
// followed, and that nothing was written.
//
// This is the divergence from Ansible worth being loudest about.
// Ansible follows the link and edits the target. A file here is written
// by renaming a new one over the path, which would REPLACE THE LINK with
// a regular file and quietly detach it from whatever it pointed at. The
// target keeping its exact contents is what proves nothing happened.
func TestSet_RefusesASymbolicLink(t *testing.T) {
	server := startBlockServer(t)
	target := blockFile(t, "alpha\n")
	link := filepath.Join(filepath.Dir(target), "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("creating the symlink: %v", err)
	}

	_, err := block.Set(context.Background(), newBlockContext(server), newBlockTarget(server), blockParams(link, map[string]any{
		"block": "one\n",
	}))
	if err == nil {
		t.Fatal("a symbolic link was accepted, so the task would have replaced the link with a file")
	}
	if !strings.Contains(err.Error(), "symbolic link") {
		t.Errorf("error = %q, want it to say the path is a link", err)
	}
	if !strings.Contains(err.Error(), target) {
		t.Errorf("error = %q, want it to name the target to point at instead", err)
	}
	if got := blockContents(t, target); got != "alpha\n" {
		t.Errorf("the link's target holds %q, want it untouched", got)
	}
	if info, statErr := os.Lstat(link); statErr != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("os.Lstat(%s) = %v, want the path still a symbolic link", link, info)
	}
}

// TestSet_RefusesSomethingThatIsNotAFile covers the remaining kinds a
// path can be. A directory is the one anybody actually hits, by pointing
// a task at a config directory instead of the file in it.
func TestSet_RefusesSomethingThatIsNotAFile(t *testing.T) {
	server := startBlockServer(t)
	dir := t.TempDir()

	_, err := block.Set(context.Background(), newBlockContext(server), newBlockTarget(server), blockParams(dir, map[string]any{
		"block": "one\n",
	}))
	if err == nil {
		t.Fatal("a directory was accepted")
	}
	if !strings.Contains(err.Error(), "is a directory, not a regular file") {
		t.Errorf("error = %q, want it to say what is really at the path", err)
	}
}

// TestSet_RefusesMarkersThatDoNotPairUp proves every ambiguous file is
// an error rather than a guess, and that the file is left alone.
//
// Ansible guesses at each of these: it takes the last marker of each
// kind it sees, and treats a begin with no end as "there is no block",
// which makes it append a second one below the orphan. Both guesses
// leave a file somebody has to untangle by hand, and the message here is
// what tells them where to look.
func TestSet_RefusesMarkersThatDoNotPairUp(t *testing.T) {
	tests := []struct {
		name     string
		contents string
		wants    string
	}{
		{
			name:     "two begin markers",
			contents: blockTestBegin + "\none\n" + blockTestBegin + "\ntwo\n" + blockTestEnd + "\n",
			wants:    "found 2 copies of the begin marker",
		},
		{
			name:     "two end markers",
			contents: blockTestBegin + "\none\n" + blockTestEnd + "\ntwo\n" + blockTestEnd + "\n",
			wants:    "found 2 copies of the end marker",
		},
		{
			name:     "begin with no end",
			contents: "alpha\n" + blockTestBegin + "\none\n",
			wants:    "with no matching end marker",
		},
		{
			name:     "end with no begin",
			contents: "alpha\n" + blockTestEnd + "\n",
			wants:    "with no matching begin marker",
		},
		{
			name:     "closed before it opens",
			contents: blockTestEnd + "\none\n" + blockTestBegin + "\n",
			wants:    "closes before it opens",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := startBlockServer(t)
			path := blockFile(t, tt.contents)

			_, err := block.Set(context.Background(), newBlockContext(server), newBlockTarget(server), blockParams(path, map[string]any{
				"block": "one\n",
			}))
			if err == nil {
				t.Fatal("an ambiguous file was accepted, so the method guessed")
			}
			if !strings.Contains(err.Error(), tt.wants) {
				t.Errorf("error = %q, want it to contain %q", err, tt.wants)
			}
			if got := blockContents(t, path); got != tt.contents {
				t.Errorf("the file holds\n%q\nwant it untouched at\n%q", got, tt.contents)
			}
		})
	}
}

// TestSet_NamesTheLinesOfADuplicateMarker proves the ambiguity error
// points at the lines to fix rather than merely saying there are too
// many.
//
// The numbers are 1-based, because they are for a person opening the
// file in an editor, and an off-by-one there sends them to the wrong
// line of a file they are already confused about.
func TestSet_NamesTheLinesOfADuplicateMarker(t *testing.T) {
	server := startBlockServer(t)
	path := blockFile(t, "alpha\n"+blockTestBegin+"\none\n"+blockTestBegin+"\ntwo\n"+blockTestEnd+"\n")

	_, err := block.Set(context.Background(), newBlockContext(server), newBlockTarget(server), blockParams(path, map[string]any{
		"block": "one\n",
	}))
	if err == nil {
		t.Fatal("a file with two begin markers was accepted")
	}
	if !strings.Contains(err.Error(), "lines 2, 4") {
		t.Errorf("error = %q, want it to name lines 2 and 4, where the markers really are", err)
	}
}

// TestSet_RefusesAnUnreachableDevice covers the connect failure path,
// which is what a device with no SSH transport produces.
func TestSet_RefusesAnUnreachableDevice(t *testing.T) {
	_, err := block.Set(context.Background(), newBlockContext(blockServer{}), newBlockUnreachable(), map[string]any{
		"path":  "/etc/hosts",
		"block": "one\n",
	})
	if err == nil {
		t.Fatal("expected a device with no SSH transport to be refused")
	}
	if !strings.Contains(err.Error(), "not reachable over SSH") {
		t.Errorf("error = %q, want it to say the device cannot be reached", err)
	}
}

// TestSet_ReportsAFailureAtEveryStep walks the session budget along the
// six commands a changing run sends, so each failure branch is reached
// with a real protocol-level refusal.
//
// What each case is really asserting is that the failure is reported as
// itself. A read that failed must never be mistaken for a file with no
// block, which would make the method append a second one; and a write
// that landed before the mode could be restored must say so, because the
// file is then sitting there readable only by the account this task
// connected as, and nothing else will ever mention it.
func TestSet_ReportsAFailureAtEveryStep(t *testing.T) {
	tests := []struct {
		name   string
		budget int
		wants  string
	}{
		{name: "the stat that reads what is at the path", budget: 0, wants: "stat "},
		{name: "the read of the file", budget: 1, wants: "read "},
		{name: "the write", budget: 2, wants: "write "},
		{name: "the stat that reads back what the write left", budget: 3, wants: "could not be given back its previous mode"},
		{name: "the chmod that puts the mode back", budget: 4, wants: "could not be given back its previous mode"},
		{name: "the read that observes the result", budget: 5, wants: "read "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := startBlockServerWithSessionBudget(t, tt.budget)
			path := blockFile(t, "alpha\n")

			_, err := block.Set(context.Background(), newBlockContext(server), newBlockTarget(server), blockParams(path, map[string]any{
				"block": "one\n",
			}))
			if err == nil {
				t.Fatal("a failed command was reported as success")
			}
			if !strings.Contains(err.Error(), "open session") {
				t.Errorf("error = %q, want it to say the session could not be opened", err)
			}
			if !strings.Contains(err.Error(), tt.wants) {
				t.Errorf("error = %q, want it to name the step that failed (%q)", err, tt.wants)
			}
			if strings.Contains(err.Error(), "does not exist") {
				t.Errorf("error = %q, want a transport failure not to be reported as an absent file", err)
			}
		})
	}
}

// TestSet_RecordFailuresAreReported covers the three separate recording
// call sites, each of which a single earlier one could hide.
//
// Swallowing any of them would leave a rollback engine with no record of
// a change that really happened, which is worse than a failed task: the
// device moved and nothing knows how to move it back.
func TestSet_RecordFailuresAreReported(t *testing.T) {
	for _, key := range []string{"diff", "path", "inverse"} {
		t.Run(key, func(t *testing.T) {
			server := startBlockServer(t)
			rc := newBlockContext(server)
			rc.failOn = key

			_, err := block.Set(context.Background(), rc, newBlockTarget(server), blockParams(blockFile(t, "alpha\n"), map[string]any{
				"block": "one\n",
			}))
			if err == nil {
				t.Fatalf("a failure to record %q was swallowed", key)
			}
			if !errors.Is(err, errBlockStat) {
				t.Errorf("error = %q, want it to carry what recording returned", err)
			}
		})
	}
}
