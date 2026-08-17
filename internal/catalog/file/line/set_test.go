package line_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/file/line"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// TestSet_Registered proves the method registered itself as implemented and
// answered the one question a manifest can answer about undoing it.
//
// Reversible is pinned rather than merely read. Flipping it to false later
// would tell a rollback engine there is nothing to restore while this
// method's run is still quietly capturing the prior text, and the Notes are
// pinned as non-empty because registration only enforces them for a method
// answering false, so nothing else would notice them disappearing.
func TestSet_Registered(t *testing.T) {
	d, ok := collection.Lookup("file.line.set")
	if !ok {
		t.Fatalf("collection.Lookup(%q) found nothing; did this package's init() run?", "file.line.set")
	}
	if d.Manifest.Status != collection.StatusImplemented {
		t.Errorf("Manifest.Status = %v, want %v", d.Manifest.Status, collection.StatusImplemented)
	}
	if d.Invoke == nil {
		t.Error("Invoke is nil, so the dispatcher has nothing to call")
	}
	if !d.Manifest.Reversibility.Reversible {
		t.Error("Reversibility.Reversible = false, but a run that changes this file emits an inverse")
	}
	if d.Manifest.Reversibility.Notes == "" {
		t.Error("Reversibility.Notes is empty, so nothing says what the inverse does not restore")
	}
}

// TestSet_RefusesAMissingPath proves the required parameter is really
// required, and that the refusal happens before any connection.
//
// A nil device would fail in connect, so reaching the expected message with
// one is itself the proof that nothing tried to dial.
func TestSet_RefusesAMissingPath(t *testing.T) {
	for _, params := range []map[string]any{
		nil,
		{"line": "x"},
		{"path": "", "line": "x"},
		{"path": nil, "line": "x"},
	} {
		_, err := line.Set(context.Background(), nil, nil, params)
		if err == nil {
			t.Fatalf("params %v: expected a refusal, got nil", params)
		}
		if !strings.Contains(err.Error(), "path is required") {
			t.Errorf("params %v: error = %q, want it to name the missing parameter", params, err)
		}
	}
}

// TestSet_RefusesAMissingLine proves this method will not guess what to put
// in the file.
//
// The refusal has to say what the parameter is for. "line is required" alone
// reads as a schema complaint next to a runbook that plainly names a path
// and a pattern.
func TestSet_RefusesAMissingLine(t *testing.T) {
	for _, params := range []map[string]any{
		{"path": "/etc/hosts"},
		{"path": "/etc/hosts", "line": ""},
		{"path": "/etc/hosts", "regexp": "^x"},
	} {
		_, err := line.Set(context.Background(), nil, nil, params)
		if err == nil {
			t.Fatalf("params %v: expected a refusal, got nil", params)
		}
		if !strings.Contains(err.Error(), "line is required") {
			t.Errorf("params %v: error = %q, want it to name the missing parameter", params, err)
		}
	}
}

// TestSet_RefusesALineWithANewline proves a multi-line value is refused
// rather than written.
//
// Writing it would be the worst available outcome: the file gets several
// lines, and the single line search that decides convergence never finds
// them again, so every later run appends the whole thing once more and the
// file grows without limit. The refusal names the method that does place
// several lines.
func TestSet_RefusesALineWithANewline(t *testing.T) {
	_, err := line.Set(context.Background(), nil, nil, map[string]any{
		"path": "/etc/hosts",
		"line": "first\nsecond",
	})
	if err == nil {
		t.Fatal("a multi-line value was accepted, so the file would grow on every run")
	}
	if !strings.Contains(err.Error(), "contains a newline") {
		t.Errorf("error = %q, want it to say what is wrong with the value", err)
	}
	if !strings.Contains(err.Error(), "file.block.set") {
		t.Errorf("error = %q, want it to name the method that places several lines", err)
	}
}

// TestSet_RefusesAParameterThatIsNotText covers the mistake YAML makes on an
// author's behalf: an unquoted value is decoded as a number or a boolean
// long before this method sees it.
//
// The refusal has to name the parameter and say to quote it. Treating a
// non-string as absent, which is what sdk.StringParam does, would answer
// "line is required" or would silently ignore a pattern, and either sends
// the author hunting for a parameter they did write.
func TestSet_RefusesAParameterThatIsNotText(t *testing.T) {
	tests := []struct {
		name   string
		params map[string]any
	}{
		{name: "path", params: map[string]any{"path": 42, "line": "x"}},
		{name: "line", params: map[string]any{"path": "/etc/hosts", "line": 42}},
		{name: "regexp", params: map[string]any{"path": "/etc/hosts", "line": "x", "regexp": 8080}},
		{name: "insertafter", params: map[string]any{"path": "/etc/hosts", "line": "x", "insertafter": true}},
		{name: "insertbefore", params: map[string]any{"path": "/etc/hosts", "line": "x", "insertbefore": true}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := line.Set(context.Background(), nil, nil, tt.params)
			if err == nil {
				t.Fatal("expected a refusal, got nil")
			}
			if !strings.Contains(err.Error(), tt.name+" is ") || !strings.Contains(err.Error(), ", not text") {
				t.Errorf("error = %q, want it to name %q and its type", err, tt.name)
			}
			if !strings.Contains(err.Error(), "quote it") {
				t.Errorf("error = %q, want it to say to quote the value", err)
			}
		})
	}
}

// TestSet_RefusesAPatternThatWillNotCompile proves a broken pattern is
// caught at the runbook rather than at the device, and that the refusal
// names the dialect.
//
// The dialect matters because the pattern most likely to arrive broken is
// one carried over from a playbook: Python's re has backreferences and
// lookaround and Go's RE2 has neither, so "(?<=x)y" is a valid Ansible
// pattern and is not a valid one here.
func TestSet_RefusesAPatternThatWillNotCompile(t *testing.T) {
	tests := []struct {
		name   string
		params map[string]any
	}{
		{name: "regexp", params: map[string]any{"path": "/etc/hosts", "line": "x", "regexp": "(?<=a)x"}},
		{name: "insertafter", params: map[string]any{"path": "/etc/hosts", "line": "x", "insertafter": "a(b"}},
		{name: "insertbefore", params: map[string]any{"path": "/etc/hosts", "line": "x", "insertbefore": "a(b"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := line.Set(context.Background(), nil, nil, tt.params)
			if err == nil {
				t.Fatal("expected a refusal, got nil")
			}
			if !strings.Contains(err.Error(), tt.name+" ") || !strings.Contains(err.Error(), "will not compile") {
				t.Errorf("error = %q, want it to name the parameter that will not compile", err)
			}
			if !strings.Contains(err.Error(), "RE2") {
				t.Errorf("error = %q, want it to name the dialect, since the pattern probably came from a playbook", err)
			}
		})
	}
}

// TestSet_RefusesAPatternThatCannotConverge is the refusal worth having, and
// it is the classic lineinfile footgun caught for free.
//
// A pattern that does not match the line being placed finds nothing on the
// first run, so the line is appended; on the second run it still finds
// nothing, because the line just added does not match it either, so the line
// is appended again, forever. Ansible accepts this. Catching it costs one
// comparison and no round trip, which is why it happens here rather than
// after a connect.
func TestSet_RefusesAPatternThatCannotConverge(t *testing.T) {
	_, err := line.Set(context.Background(), nil, nil, map[string]any{
		"path":   "/etc/ssh/sshd_config",
		"regexp": `^PermitRootLogin\s+yes`,
		"line":   "PermitRootLogin no",
	})
	if err == nil {
		t.Fatal("a pattern that cannot match the line it places was accepted, so the file would grow on every run")
	}
	if !strings.Contains(err.Error(), "does not match") {
		t.Errorf("error = %q, want it to say the pattern and the line disagree", err)
	}
	if !strings.Contains(err.Error(), "Widen the pattern") {
		t.Errorf("error = %q, want it to say how to fix the task", err)
	}
}

// TestSet_AcceptsAPatternThatMatchesTheLine is the control for the refusal
// above: the identical shape of task, with the pattern widened the way the
// error says to widen it, has to be accepted.
//
// Without this, a check that refused everything would pass the test above
// and nothing would notice.
func TestSet_AcceptsAPatternThatMatchesTheLine(t *testing.T) {
	server := startLineServer(t)
	rc := newLineContext(server)
	path := lineWriteFile(t, "#PermitRootLogin yes\n", 0o600)

	result, err := line.Set(context.Background(), rc, newLineTarget(server), lineParams(map[string]any{
		"path":   path,
		"regexp": "^#?PermitRootLogin",
		"line":   "PermitRootLogin no",
	}))
	if err != nil {
		t.Fatalf("Set: %v", err)
	}
	if !result.Changed {
		t.Error("a run that replaced a line reported no change")
	}
	if got := lineOnDisk(t, path); got != "PermitRootLogin no\n" {
		t.Errorf("the file holds %q, want the replaced line", got)
	}
}

// TestSet_RefusesBothAnchors proves the mutually exclusive rule Ansible
// declares in its own argument spec is enforced here too.
//
// There is no answer when both patterns match, because the line cannot go in
// two places, so guessing one would be a task whose behavior depends on
// which branch of an implementation happened to be written first.
func TestSet_RefusesBothAnchors(t *testing.T) {
	_, err := line.Set(context.Background(), nil, nil, map[string]any{
		"path":         "/etc/hosts",
		"line":         "x",
		"insertafter":  "^a",
		"insertbefore": "^b",
	})
	if err == nil {
		t.Fatal("two anchors were accepted, so the line's position depends on the implementation")
	}
	if !strings.Contains(err.Error(), "cannot both be set") {
		t.Errorf("error = %q, want it to say the two conflict", err)
	}
}

// TestSet_RefusesTheCrossAnchors proves the two anchor words are refused on
// the parameter they do not belong to.
//
// Ansible's own code quietly accepts insertafter: BOF and treats it as
// insertbefore: BOF. Silently honoring it here would mean a reader of the
// runbook had to know that quirk to know where the line goes, and treating
// it as a pattern instead would silently match every line containing the
// text BOF. The refusal names the spelling that works.
func TestSet_RefusesTheCrossAnchors(t *testing.T) {
	tests := []struct {
		name   string
		params map[string]any
		want   string
	}{
		{
			name:   "insertafter BOF",
			params: map[string]any{"path": "/etc/hosts", "line": "x", "insertafter": "BOF"},
			want:   "insertbefore: BOF",
		},
		{
			name:   "insertbefore EOF",
			params: map[string]any{"path": "/etc/hosts", "line": "x", "insertbefore": "EOF"},
			want:   "leave both anchors out",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := line.Set(context.Background(), nil, nil, tt.params)
			if err == nil {
				t.Fatal("expected a refusal, got nil")
			}
			if !strings.Contains(err.Error(), "is not an anchor") {
				t.Errorf("error = %q, want it to say the word is not an anchor on that parameter", err)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %q, want it to name the spelling that works (%q)", err, tt.want)
			}
		})
	}
}

// TestSet_RefusesAnUnreachableDevice covers the connect failure path, which
// is what a device with no SSH transport produces.
func TestSet_RefusesAnUnreachableDevice(t *testing.T) {
	_, err := line.Set(context.Background(), newLineContext(lineServer{}), newLineUnreachable(), map[string]any{
		"path": "/etc/hosts",
		"line": "x",
	})
	if err == nil {
		t.Fatal("expected a device with no SSH transport to be refused")
	}
	if !strings.Contains(err.Error(), "not reachable over SSH") {
		t.Errorf("error = %q, want it to say the device cannot be reached", err)
	}
}

// TestSet_AppendsALineThatIsNotThere is the create-equivalent path: the run
// that finds a difference and closes it.
//
// It checks four separate things, because each one has failed independently
// in this codebase before: the bytes on disk, the reported flag, the stats a
// later task reads, and the recorded diff whose before half is the only copy
// of the prior text.
func TestSet_AppendsALineThatIsNotThere(t *testing.T) {
	server := startLineServer(t)
	rc := newLineContext(server)
	path := lineWriteFile(t, "127.0.0.1 localhost\n", 0o600)

	result, err := line.Set(context.Background(), rc, newLineTarget(server), lineParams(map[string]any{
		"path": path,
		"line": "10.0.4.12 registry.internal",
	}))
	if err != nil {
		t.Fatalf("Set: %v", err)
	}
	if !result.Changed {
		t.Error("a run that added a line reported no change")
	}

	want := "127.0.0.1 localhost\n10.0.4.12 registry.internal\n"
	if got := lineOnDisk(t, path); got != want {
		t.Errorf("the file holds %q, want %q", got, want)
	}
	if got := rc.stats["path"]; got != path {
		t.Errorf("path stat = %v, want %q", got, path)
	}
	if got := rc.stats["msg"]; got != "line added" {
		t.Errorf("msg stat = %v, want \"line added\"", got)
	}

	before := lineDiffHalf(t, rc, "before")
	if got := before["content"]; got != "127.0.0.1 localhost\n" {
		t.Errorf("diff before content = %q, want the prior text an inverse would restore", got)
	}
	// The after half has to describe the DEVICE, which is why the method reads
	// it back rather than echoing what it asked for. A method that recorded
	// its own request would pass every other assertion here.
	after := lineDiffHalf(t, rc, "after")
	if got := after["content"]; got != want {
		t.Errorf("diff after content = %q, want the text read back from the device", got)
	}
}

// TestSet_EmitsAnInverseCarryingThePriorFile proves the run recorded a
// runnable instruction rather than a description of one.
//
// The whole prior text is the point. A line edit is not reliably undone by
// another line edit, since a replacement has already thrown the old line
// away, so an inverse naming file.line.remove would silently fail to restore
// anything. The attributes are checked too, because restoring the text means
// replacing the file, and a restore that dropped the mode would be the
// rollback causing the damage.
func TestSet_EmitsAnInverseCarryingThePriorFile(t *testing.T) {
	server := startLineServer(t)
	rc := newLineContext(server)
	path := lineWriteFile(t, "alpha\nbeta\n", 0o640)

	if _, err := line.Set(context.Background(), rc, newLineTarget(server), lineParams(map[string]any{
		"path": path,
		"line": "gamma",
	})); err != nil {
		t.Fatalf("Set: %v", err)
	}

	inverse := lineInverse(t, rc)
	if got := inverse["fqcn"]; got != "file.copy" {
		t.Errorf("inverse fqcn = %v, want file.copy: only a whole-file restore undoes a line edit", got)
	}
	if got, _ := inverse["description"].(string); !strings.Contains(got, path) {
		t.Errorf("inverse description = %q, want it to name the file an operator would be approving a write to", got)
	}

	params := lineInverseParams(t, rc)
	if got := params["dest"]; got != path {
		t.Errorf("inverse dest = %v, want %q", got, path)
	}
	if got := params["content"]; got != "alpha\nbeta\n" {
		t.Errorf("inverse content = %q, want the whole prior text", got)
	}
	if got := params["mode"]; got != "0640" {
		t.Errorf("inverse mode = %v, want the 0640 the file carried, not the mode a write would leave", got)
	}
	// The owner and group have to be the ones the file really had, and the
	// diff's before half is where those were read from, so comparing the two
	// catches an inverse wired to the wrong source.
	before := lineDiffHalf(t, rc, "before")
	for _, key := range []string{"owner", "group"} {
		if params[key] != before[key] {
			t.Errorf("inverse %s = %v, want %v, the value read off the device", key, params[key], before[key])
		}
	}
}

// TestSet_ConvergedRunReportsNoChange is the single most important property
// this method has.
//
// The second run must report no change, must leave the bytes exactly as the
// first run left them, and must emit NO inverse: a run that changed nothing
// has nothing to undo, and recording one anyway would have a rollback
// rewrite a file this task never touched.
func TestSet_ConvergedRunReportsNoChange(t *testing.T) {
	server := startLineServer(t)
	path := lineWriteFile(t, "alpha\n", 0o600)
	device := newLineTarget(server)
	params := lineParams(map[string]any{"path": path, "line": "beta"})

	first, err := line.Set(context.Background(), newLineContext(server), device, params)
	if err != nil {
		t.Fatalf("first Set: %v", err)
	}
	if !first.Changed {
		t.Fatal("the first run reported no change, so this test could prove nothing about the second")
	}
	written := lineOnDisk(t, path)

	rc := newLineContext(server)
	second, err := line.Set(context.Background(), rc, device, params)
	if err != nil {
		t.Fatalf("second Set: %v", err)
	}
	if second.Changed {
		t.Error("a converged run reported a change, so this method never settles")
	}
	if got := lineOnDisk(t, path); got != written {
		t.Errorf("the file holds %q after the second run, want the untouched %q", got, written)
	}
	if got := rc.stats["msg"]; got != "" {
		t.Errorf("msg stat = %v, want it empty on a run that did nothing", got)
	}
	if _, emitted := rc.stats["inverse"]; emitted {
		t.Error("a converged run emitted an inverse, so a rollback would rewrite a file this task never touched")
	}

	// A converged run still records a diff, and its two halves have to be
	// identical: that is how a journal says "this task changed nothing, so
	// undoing it means doing nothing", which an absent diff cannot say.
	if lineDiffHalf(t, rc, "before")["content"] != lineDiffHalf(t, rc, "after")["content"] {
		t.Error("a converged run recorded two different halves, so a reader cannot tell it changed nothing")
	}
}

// TestSet_ReplacesTheLastMatch proves the pattern picks the LAST matching
// line, and that it leaves the others alone.
//
// Last is Ansible's default and the useful answer for a configuration file,
// where a setting written twice is effectively whichever came last. The
// first line staying untouched is what separates "replace the last match"
// from "replace every match", which is a different method.
func TestSet_ReplacesTheLastMatch(t *testing.T) {
	server := startLineServer(t)
	rc := newLineContext(server)
	path := lineWriteFile(t, "Port 22\nListenAddress 0.0.0.0\nPort 2200\n", 0o600)

	result, err := line.Set(context.Background(), rc, newLineTarget(server), lineParams(map[string]any{
		"path":   path,
		"regexp": "^Port ",
		"line":   "Port 2222",
	}))
	if err != nil {
		t.Fatalf("Set: %v", err)
	}
	if !result.Changed {
		t.Error("a run that replaced a line reported no change")
	}

	want := "Port 22\nListenAddress 0.0.0.0\nPort 2222\n"
	if got := lineOnDisk(t, path); got != want {
		t.Errorf("the file holds %q, want %q: the last match is the one that is replaced", got, want)
	}
	if got := rc.stats["msg"]; got != "line replaced" {
		t.Errorf("msg stat = %v, want \"line replaced\" rather than the wording for an add", got)
	}
}

// TestSet_InsertsAgainstAnAnchor covers every placement the two anchors can
// produce, in one table, against one starting file.
//
// They are together because the interesting property is relative: the same
// line lands in four different places depending only on which anchor the
// task named, and a table makes a placement wired to the wrong index
// obvious in a way four separate tests would not.
func TestSet_InsertsAgainstAnAnchor(t *testing.T) {
	const start = "one\nmiddle\ntwo\nmiddle\nthree\n"

	tests := []struct {
		name   string
		anchor map[string]any
		want   string
	}{
		{
			name:   "after the last match",
			anchor: map[string]any{"insertafter": "^middle$"},
			want:   "one\nmiddle\ntwo\nmiddle\nadded\nthree\n",
		},
		{
			name:   "before the last match",
			anchor: map[string]any{"insertbefore": "^middle$"},
			want:   "one\nmiddle\ntwo\nadded\nmiddle\nthree\n",
		},
		{
			name:   "at the start of the file",
			anchor: map[string]any{"insertbefore": "BOF"},
			want:   "added\none\nmiddle\ntwo\nmiddle\nthree\n",
		},
		{
			name:   "at the end of the file, named",
			anchor: map[string]any{"insertafter": "EOF"},
			want:   "one\nmiddle\ntwo\nmiddle\nthree\nadded\n",
		},
		{
			name:   "at the end of the file, by default",
			anchor: nil,
			want:   "one\nmiddle\ntwo\nmiddle\nthree\nadded\n",
		},
		{
			// An anchor that matches nothing is a weaker statement than "do not
			// add this line", so the line still goes in, at the end. That is
			// Ansible's documented fallback for both anchors.
			name:   "an anchor that matches nothing falls back to the end",
			anchor: map[string]any{"insertafter": "^nowhere$"},
			want:   "one\nmiddle\ntwo\nmiddle\nthree\nadded\n",
		},
		{
			name:   "an insertbefore that matches nothing falls back to the end",
			anchor: map[string]any{"insertbefore": "^nowhere$"},
			want:   "one\nmiddle\ntwo\nmiddle\nthree\nadded\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := startLineServer(t)
			path := lineWriteFile(t, start, 0o600)

			params := map[string]any{"path": path, "line": "added"}
			for key, value := range tt.anchor {
				params[key] = value
			}

			result, err := line.Set(context.Background(), newLineContext(server), newLineTarget(server), lineParams(params))
			if err != nil {
				t.Fatalf("Set: %v", err)
			}
			if !result.Changed {
				t.Error("a run that added a line reported no change")
			}
			if got := lineOnDisk(t, path); got != tt.want {
				t.Errorf("the file holds %q, want %q", got, tt.want)
			}
		})
	}
}

// TestSet_KeepsTheFileEndingItFound is the trailing newline contract, and
// the reason it is a contract rather than a detail.
//
// remotefile.Write replaces the whole file, so whatever this method renders
// is exactly what the file becomes. A render that always finished with a
// newline would rewrite a file that had none for no other reason, and a
// render that always dropped one would do the same to every normal file. The
// converged half of this table is the one that catches it: asking for a line
// that is already present in a file with no final newline has to send no
// write at all.
func TestSet_KeepsTheFileEndingItFound(t *testing.T) {
	tests := []struct {
		name    string
		start   string
		line    string
		want    string
		changed bool
	}{
		{
			name:    "a file with no final newline keeps none",
			start:   "alpha\nbeta",
			line:    "gamma",
			want:    "alpha\nbeta\ngamma",
			changed: true,
		},
		{
			name:    "a file with no final newline is not rewritten for one",
			start:   "alpha\nbeta",
			line:    "beta",
			want:    "alpha\nbeta",
			changed: false,
		},
		{
			name:    "a file with a final newline keeps it",
			start:   "alpha\nbeta\n",
			line:    "gamma",
			want:    "alpha\nbeta\ngamma\n",
			changed: true,
		},
		{
			// An empty file has no ending to preserve, so the line it receives
			// gets the newline a text file is expected to end with.
			name:    "an empty file gets a normal text file back",
			start:   "",
			line:    "alpha",
			want:    "alpha\n",
			changed: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := startLineServer(t)
			path := lineWriteFile(t, tt.start, 0o600)

			result, err := line.Set(context.Background(), newLineContext(server), newLineTarget(server), lineParams(map[string]any{
				"path": path,
				"line": tt.line,
			}))
			if err != nil {
				t.Fatalf("Set: %v", err)
			}
			if result.Changed != tt.changed {
				t.Errorf("Changed = %v, want %v", result.Changed, tt.changed)
			}
			if got := lineOnDisk(t, path); got != tt.want {
				t.Errorf("the file holds %q, want %q", got, tt.want)
			}
		})
	}
}

// TestSet_KeepsTheFilesPermissions is the assertion that catches the one
// thing an atomic write silently destroys.
//
// remotefile.Write creates a temporary with mktemp, which makes it 0600, and
// renames it over the target, and a rename replaces the inode. Without the
// restore, editing one line of a file that everybody could read would leave
// it readable only by the account that ran the task, and the task would
// report success. 0640 is deliberately neither mktemp's 0600 nor anything a
// umask hands out, so this cannot pass by accident.
func TestSet_KeepsTheFilesPermissions(t *testing.T) {
	server := startLineServer(t)
	path := lineWriteFile(t, "alpha\n", 0o640)

	if _, err := line.Set(context.Background(), newLineContext(server), newLineTarget(server), lineParams(map[string]any{
		"path": path,
		"line": "beta",
	})); err != nil {
		t.Fatalf("Set: %v", err)
	}

	if got := lineModeOnDisk(t, path); got != "0640" {
		t.Errorf("the file is %s on disk, want the 0640 it had: the atomic write left the temporary's permissions behind", got)
	}
	if got := lineOnDisk(t, path); got != "alpha\nbeta\n" {
		t.Errorf("the file holds %q, want the edited text", got)
	}
}

// TestSet_KeepsTheFilesGroup is the other half of what an atomic write
// throws away, and it needs a real second group to show it.
//
// mktemp creates the temporary owned by the connecting account and its
// primary group, and the rename hands those to the file. A file that had been
// handed to a service group would come back owned by whoever ran the task,
// which is a permissions change nobody asked for on a file whose whole point
// is who may read it. The mode test cannot show this: it passes even with the
// owner and group left out of the repair entirely.
//
// It skips rather than pretending where there is no second group to use.
func TestSet_KeepsTheFilesGroup(t *testing.T) {
	server := startLineServer(t)
	path := lineWriteFile(t, "alpha\n", 0o640)

	group := lineHandToAnotherGroup(t, path)
	if group == "" {
		t.Skip("this process may use no second group, so a group that survives a write cannot be proven here")
	}

	if _, err := line.Set(context.Background(), newLineContext(server), newLineTarget(server), lineParams(map[string]any{
		"path": path,
		"line": "beta",
	})); err != nil {
		t.Fatalf("Set: %v", err)
	}

	if got := lineGroupOnDisk(t, path); got != group {
		t.Errorf("the file belongs to group %q on disk, want the %q it had: the atomic write handed it to the temporary's group", got, group)
	}
	if got := lineOnDisk(t, path); got != "alpha\nbeta\n" {
		t.Errorf("the file holds %q, want the edited text", got)
	}
}

// TestSet_ReportsTheRestoredPermissions proves the recorded after state
// describes the file as it really is once the permissions have been put
// back, not as it was in the moment between the rename and the repair.
//
// Recording the intermediate state would tell an operator the file is 0600
// when it is 0640, which is worse than recording nothing.
func TestSet_ReportsTheRestoredPermissions(t *testing.T) {
	server := startLineServer(t)
	rc := newLineContext(server)
	path := lineWriteFile(t, "alpha\n", 0o640)

	if _, err := line.Set(context.Background(), rc, newLineTarget(server), lineParams(map[string]any{
		"path": path,
		"line": "beta",
	})); err != nil {
		t.Fatalf("Set: %v", err)
	}

	if got := lineDiffHalf(t, rc, "after")["mode"]; got != "0640" {
		t.Errorf("diff after mode = %v, want 0640, which is what the file really carries", got)
	}
}

// TestSet_RefusesAnAbsentPath proves this method edits a file and never
// creates one, which is lineinfile's own default.
//
// The refusal has to name what does create, because "does not exist" on its
// own reads as a mistake in the runbook's ordering rather than as a
// deliberate boundary between methods.
func TestSet_RefusesAnAbsentPath(t *testing.T) {
	server := startLineServer(t)
	path := filepath.Join(t.TempDir(), "not-there.conf")

	_, err := line.Set(context.Background(), newLineContext(server), newLineTarget(server), lineParams(map[string]any{
		"path": path,
		"line": "alpha",
	}))
	if err == nil {
		t.Fatal("an absent path was accepted, so this method created something")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("error = %q, want it to say the path is not there", err)
	}
	if !strings.Contains(err.Error(), "file.touch") {
		t.Errorf("error = %q, want it to name what does create a path", err)
	}
	if _, statErr := os.Lstat(path); !os.IsNotExist(statErr) {
		t.Errorf("os.Lstat(%s) = %v, want the path still absent", path, statErr)
	}
}

// TestSet_RefusesSomethingThatIsNotARegularFile proves a link and a
// directory are both refused, and that nothing was written either way.
//
// The link case is the one that would do real damage. remotefile.Write
// renames a temporary over the path, so writing "through" a link would leave
// a regular file where the link was, and everything else resolving through
// that link would stop seeing later edits with nothing reporting an error.
func TestSet_RefusesSomethingThatIsNotARegularFile(t *testing.T) {
	server := startLineServer(t)
	target := lineWriteFile(t, "alpha\n", 0o600)
	dir := filepath.Dir(target)

	link := filepath.Join(dir, "link.conf")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("creating the symlink: %v", err)
	}

	tests := []struct {
		name string
		path string
		want string
	}{
		{name: "a symbolic link", path: link, want: "symbolic link"},
		{name: "a directory", path: dir, want: "rather than a regular file"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := line.Set(context.Background(), newLineContext(server), newLineTarget(server), lineParams(map[string]any{
				"path": tt.path,
				"line": "beta",
			}))
			if err == nil {
				t.Fatal("expected a refusal, got nil")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %q, want it to say what is at the path", err)
			}
			if got := lineOnDisk(t, target); got != "alpha\n" {
				t.Errorf("the real file holds %q, want the untouched %q: something was written anyway", got, "alpha\n")
			}
		})
	}

	// The link's own refusal has to name the target, because "point the task
	// somewhere else" is not actionable without saying where.
	_, err := line.Set(context.Background(), newLineContext(server), newLineTarget(server), lineParams(map[string]any{
		"path": link,
		"line": "beta",
	}))
	if err == nil || !strings.Contains(err.Error(), target) {
		t.Errorf("error = %v, want it to name the target to point at instead", err)
	}
}

// TestSet_TransportFailuresAreReported walks every command this method sends
// and cuts the connection off at each one in turn.
//
// The session budget is a count of commands the harness lets through before
// refusing, and this method sends them in a fixed order: stat, read, write,
// stat, chmod, stat, read. Every one of those is a branch that has to report
// what failed rather than carrying on with a zero value, and the two the
// budget alone cannot separate are told apart by the file's mode: at 0600 the
// write leaves nothing to repair, so no chmod is sent at all.
//
// The message assertions matter as much as the failures. A read that failed
// and was swallowed leaves empty text, and every later comparison then finds
// a difference, so the task would carry on and write an empty file over a
// real one while reporting an error about something else entirely.
func TestSet_TransportFailuresAreReported(t *testing.T) {
	tests := []struct {
		name    string
		budget  int
		mode    os.FileMode
		want    []string
		written bool
	}{
		{
			name:   "the first read of the path",
			budget: 0,
			mode:   0o600,
			want:   []string{"open session", "stat "},
		},
		{
			name:   "reading the contents",
			budget: 1,
			mode:   0o600,
			want:   []string{"open session", "read "},
		},
		{
			name:   "the write itself",
			budget: 2,
			mode:   0o600,
			want:   []string{"open session", "write "},
		},
		{
			name:    "reading the path back after the write",
			budget:  3,
			mode:    0o600,
			want:    []string{"open session", "stat ", "already in place"},
			written: true,
		},
		{
			name:    "putting the permissions back",
			budget:  4,
			mode:    0o640,
			want:    []string{"open session", "chmod ", "temporary it was written through"},
			written: true,
		},
		{
			name:    "confirming the permissions went back",
			budget:  5,
			mode:    0o640,
			want:    []string{"open session", "stat ", "original permissions"},
			written: true,
		},
		{
			name:    "reading the new contents back",
			budget:  6,
			mode:    0o640,
			want:    []string{"open session", "read ", "failure to confirm"},
			written: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := startLineServerWithSessionBudget(t, tt.budget)
			path := lineWriteFile(t, "alpha\n", tt.mode)

			_, err := line.Set(context.Background(), newLineContext(server), newLineTarget(server), lineParams(map[string]any{
				"path": path,
				"line": "beta",
			}))
			if err == nil {
				t.Fatal("a failed command was reported as success")
			}
			for _, want := range tt.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error = %q, want it to contain %q so the operator knows which step failed", err, want)
				}
			}

			// Whatever the error claims about the file has to be true, which is
			// the half a fixed phrase cannot fake.
			content := lineOnDisk(t, path)
			if tt.written && content != "alpha\nbeta\n" {
				t.Errorf("the file holds %q, want the edited text the error says is already in place", content)
			}
			if !tt.written && content != "alpha\n" {
				t.Errorf("the file holds %q, want the untouched text", content)
			}
		})
	}
}

// TestSet_RecordFailuresAreReported covers the three separate recording call
// sites, which no single one of them can reach on its own.
//
// Swallowing any of them would leave a rollback engine with no record of a
// change that really happened, which is worse than a failed task: the change
// is on the device either way, and only one of the two outcomes says so.
func TestSet_RecordFailuresAreReported(t *testing.T) {
	tests := []struct {
		name   string
		failOn string
		line   string
		after  []string
	}{
		{name: "the diff", failOn: "diff", line: "beta"},
		{name: "the inverse", failOn: "inverse", line: "beta", after: []string{"diff"}},
		{name: "the returned stats", failOn: "path", line: "beta", after: []string{"diff", "inverse"}},
		{name: "the diff of a converged run", failOn: "diff", line: "alpha"},
		{name: "the stats of a converged run", failOn: "msg", line: "alpha", after: []string{"diff"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := startLineServer(t)
			rc := newLineContext(server)
			rc.failOn = tt.failOn

			_, err := line.Set(context.Background(), rc, newLineTarget(server), lineParams(map[string]any{
				"path": lineWriteFile(t, "alpha\n", 0o600),
				"line": tt.line,
			}))
			if err == nil {
				t.Fatal("a failure to record was swallowed")
			}
			if !errors.Is(err, errLineStat) {
				t.Errorf("error = %q, want it to carry what recording returned", err)
			}
			// Everything that should have been recorded before this call site
			// has to already be there, which is what pins the order the three
			// run in.
			for _, key := range tt.after {
				if _, recorded := rc.stats[key]; !recorded {
					t.Errorf("%q was not recorded before %q, so the call sites run in the wrong order", key, tt.failOn)
				}
			}
		})
	}
}
