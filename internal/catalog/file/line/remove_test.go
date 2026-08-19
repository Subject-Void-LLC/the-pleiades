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

// TestRemove_Registered proves the method registered itself as implemented
// and answered the one question a manifest can answer about undoing it.
//
// Reversible matters more here than for its sibling: the lines this method
// takes out exist nowhere else once it has run, so a manifest that later
// said false would be telling a rollback engine to stop looking for the only
// copy of them.
func TestRemove_Registered(t *testing.T) {
	d, ok := collection.Lookup("file.line.remove")
	if !ok {
		t.Fatalf("collection.Lookup(%q) found nothing; did this package's init() run?", "file.line.remove")
	}
	if d.Manifest.Status != collection.StatusImplemented {
		t.Errorf("Manifest.Status = %v, want %v", d.Manifest.Status, collection.StatusImplemented)
	}
	if d.Invoke == nil {
		t.Error("Invoke is nil, so the dispatcher has nothing to call")
	}
	if !d.Manifest.Reversibility.Reversible {
		t.Error("Reversibility.Reversible = false, but a run that removes a line emits an inverse holding it")
	}
	if d.Manifest.Reversibility.Notes == "" {
		t.Error("Reversibility.Notes is empty, so nothing says what the inverse does not restore")
	}
}

// TestRemove_RefusesAMissingPath proves the required parameter is really
// required, and that the refusal happens before any connection.
func TestRemove_RefusesAMissingPath(t *testing.T) {
	for _, params := range []map[string]any{
		nil,
		{"line": "x"},
		{"path": "", "regexp": "^x"},
		{"path": nil, "regexp": "^x"},
	} {
		_, err := line.Remove(context.Background(), nil, nil, params)
		if err == nil {
			t.Fatalf("params %v: expected a refusal, got nil", params)
		}
		if !strings.Contains(err.Error(), "path is required") {
			t.Errorf("params %v: error = %q, want it to name the missing parameter", params, err)
		}
	}
}

// TestRemove_RefusesATaskThatNamesNothing proves this method will not guess
// which lines to take out.
//
// The alternative is the worst bug this method could have. A request with
// neither parameter set, treated as "match anything", empties the file and
// reports success.
func TestRemove_RefusesATaskThatNamesNothing(t *testing.T) {
	for _, params := range []map[string]any{
		{"path": "/etc/hosts"},
		{"path": "/etc/hosts", "line": "", "regexp": ""},
		{"path": "/etc/hosts", "line": nil, "regexp": nil},
	} {
		_, err := line.Remove(context.Background(), nil, nil, params)
		if err == nil {
			t.Fatalf("params %v: expected a refusal, got nil", params)
		}
		if !strings.Contains(err.Error(), "one of regexp or line is required") {
			t.Errorf("params %v: error = %q, want it to say what to add", params, err)
		}
	}
}

// TestRemove_RefusesBothWaysOfNamingALine proves a task that says it two
// ways is refused rather than resolved.
//
// Ansible accepts both and quietly uses regexp. A task naming two different
// things to remove has one of them wrong, and choosing on the author's
// behalf hides which one, on a method whose mistakes delete text.
func TestRemove_RefusesBothWaysOfNamingALine(t *testing.T) {
	_, err := line.Remove(context.Background(), nil, nil, map[string]any{
		"path":   "/etc/hosts",
		"line":   "10.0.4.9 registry.internal",
		"regexp": "^10\\.0\\.4\\.",
	})
	if err == nil {
		t.Fatal("both parameters were accepted, so one of them was silently ignored")
	}
	if !strings.Contains(err.Error(), "both name what to remove") {
		t.Errorf("error = %q, want it to say the two conflict", err)
	}
}

// TestRemove_RefusesAnAnchor proves the two placement parameters are refused
// rather than accepted and ignored.
//
// Ansible accepts them with state: absent and does nothing with them, so a
// task that carried one over from a converted playbook looks like it is
// controlling something it is not. The refusal names the method that does
// place lines, because that is usually what the author meant.
func TestRemove_RefusesAnAnchor(t *testing.T) {
	for _, key := range []string{"insertafter", "insertbefore"} {
		_, err := line.Remove(context.Background(), nil, nil, map[string]any{
			"path":   "/etc/hosts",
			"regexp": "^x",
			key:      "^anchor",
		})
		if err == nil {
			t.Fatalf("%s: expected a refusal, got nil", key)
		}
		if !strings.Contains(err.Error(), key+" says where to insert") {
			t.Errorf("%s: error = %q, want it to name the parameter doing nothing", key, err)
		}
		if !strings.Contains(err.Error(), "file.line.set") {
			t.Errorf("%s: error = %q, want it to name the method that does place lines", key, err)
		}
	}
}

// TestRemove_RefusesALineWithANewline proves a multi-line value is refused
// rather than accepted and never matched.
//
// Accepting it would be quiet in the worst way: lines are compared one at a
// time, so the task would report nothing removed on every run and never fail.
func TestRemove_RefusesALineWithANewline(t *testing.T) {
	_, err := line.Remove(context.Background(), nil, nil, map[string]any{
		"path": "/etc/hosts",
		"line": "first\nsecond",
	})
	if err == nil {
		t.Fatal("a multi-line value was accepted, so the task would silently match nothing forever")
	}
	if !strings.Contains(err.Error(), "contains a newline") {
		t.Errorf("error = %q, want it to say what is wrong with the value", err)
	}
	if !strings.Contains(err.Error(), "file.block.remove") {
		t.Errorf("error = %q, want it to name the method that removes several lines together", err)
	}
}

// TestRemove_RefusesAParameterThatIsNotText covers the mistake YAML makes on
// an author's behalf: an unquoted value is a number or a boolean by the time
// this method sees it.
func TestRemove_RefusesAParameterThatIsNotText(t *testing.T) {
	tests := []struct {
		name   string
		params map[string]any
	}{
		{name: "path", params: map[string]any{"path": 42, "regexp": "^x"}},
		{name: "line", params: map[string]any{"path": "/etc/hosts", "line": 8080}},
		{name: "regexp", params: map[string]any{"path": "/etc/hosts", "regexp": 8080}},
		{name: "insertafter", params: map[string]any{"path": "/etc/hosts", "regexp": "^x", "insertafter": true}},
		{name: "insertbefore", params: map[string]any{"path": "/etc/hosts", "regexp": "^x", "insertbefore": true}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := line.Remove(context.Background(), nil, nil, tt.params)
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

// TestRemove_RefusesAPatternThatWillNotCompile proves a broken pattern is
// caught at the runbook rather than at the device, and that the refusal
// names the dialect the pattern probably came from.
func TestRemove_RefusesAPatternThatWillNotCompile(t *testing.T) {
	_, err := line.Remove(context.Background(), nil, nil, map[string]any{
		"path":   "/etc/hosts",
		"regexp": "(?<=a)x",
	})
	if err == nil {
		t.Fatal("expected a refusal, got nil")
	}
	if !strings.Contains(err.Error(), "regexp ") || !strings.Contains(err.Error(), "will not compile") {
		t.Errorf("error = %q, want it to name the parameter that will not compile", err)
	}
	if !strings.Contains(err.Error(), "RE2") {
		t.Errorf("error = %q, want it to name the dialect, since the pattern probably came from a playbook", err)
	}
}

// TestRemove_RefusesAnUnreachableDevice covers the connect failure path,
// which is what a device with no SSH transport produces.
func TestRemove_RefusesAnUnreachableDevice(t *testing.T) {
	_, err := line.Remove(context.Background(), newLineContext(lineServer{}), newLineUnreachable(), map[string]any{
		"path":   "/etc/hosts",
		"regexp": "^x",
	})
	if err == nil {
		t.Fatal("expected a device with no SSH transport to be refused")
	}
	if !strings.Contains(err.Error(), "not reachable over SSH") {
		t.Errorf("error = %q, want it to say the device cannot be reached", err)
	}
}

// TestRemove_RemovesEveryMatchingLine is the working path, and every match
// going is the property that makes it converge.
//
// Leaving the second copy behind would mean the next run removes that one
// and the run after finds a third, so a file with duplicates would need as
// many runs as it had copies. The untouched lines between the matches are
// what separates "remove every match" from "empty the file".
func TestRemove_RemovesEveryMatchingLine(t *testing.T) {
	server := startLineServer(t)
	rc := newLineContext(server)
	path := lineWriteFile(t, "deb http://keep.example.com main\ndeb http://old.example.com main\nkeep me\ndeb http://old.example.com extra\n", 0o600)

	result, err := line.Remove(context.Background(), rc, newLineTarget(server), lineParams(map[string]any{
		"path":   path,
		"regexp": `^deb .*old\.example\.com`,
	}))
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if !result.Changed {
		t.Error("a run that removed two lines reported no change")
	}

	want := "deb http://keep.example.com main\nkeep me\n"
	if got := lineOnDisk(t, path); got != want {
		t.Errorf("the file holds %q, want %q", got, want)
	}
	if got := rc.stats["found"]; got != 2 {
		t.Errorf("found stat = %v, want 2", got)
	}
	if got := rc.stats["msg"]; got != "2 line(s) removed" {
		t.Errorf("msg stat = %v, want lineinfile's own wording", got)
	}
	if got := rc.stats["path"]; got != path {
		t.Errorf("path stat = %v, want %q", got, path)
	}
	// The after half has to describe the DEVICE, which is why the method reads
	// it back rather than echoing what it wrote.
	if got := lineDiffHalf(t, rc, "after")["content"]; got != want {
		t.Errorf("diff after content = %q, want the text read back from the device", got)
	}
}

// TestRemove_RemovesAnExactLine proves the other way of naming a line works
// and is exact rather than a substring.
//
// The near miss in the file is the assertion that matters: a comparison that
// had been written as strings.Contains would take that line too, and a
// runbook removing "10.0.4.9 registry.internal" would silently also remove
// the entry for a different host whose line happened to hold that text.
func TestRemove_RemovesAnExactLine(t *testing.T) {
	server := startLineServer(t)
	rc := newLineContext(server)
	path := lineWriteFile(t, "10.0.4.9 registry.internal\n10.0.4.9 registry.internal spare\n", 0o600)

	result, err := line.Remove(context.Background(), rc, newLineTarget(server), lineParams(map[string]any{
		"path": path,
		"line": "10.0.4.9 registry.internal",
	}))
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if !result.Changed {
		t.Error("a run that removed a line reported no change")
	}
	if got := lineOnDisk(t, path); got != "10.0.4.9 registry.internal spare\n" {
		t.Errorf("the file holds %q, want only the line that was not an exact match", got)
	}
	if got := rc.stats["found"]; got != 1 {
		t.Errorf("found stat = %v, want 1", got)
	}
}

// TestRemove_EmitsAnInverseCarryingThePriorFile proves the run recorded a
// runnable instruction holding the only remaining copy of the removed text.
//
// The whole prior file is the point. This method can take several lines from
// several positions, and putting them back with file.line.set would pile
// them all at the end, so an inverse describing the removal would restore
// the wrong file.
func TestRemove_EmitsAnInverseCarryingThePriorFile(t *testing.T) {
	server := startLineServer(t)
	rc := newLineContext(server)
	const start = "alpha\ndrop me\nbeta\n"
	path := lineWriteFile(t, start, 0o640)

	if _, err := line.Remove(context.Background(), rc, newLineTarget(server), lineParams(map[string]any{
		"path": path,
		"line": "drop me",
	})); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	inverse := lineInverse(t, rc)
	if got := inverse["fqcn"]; got != "file.copy" {
		t.Errorf("inverse fqcn = %v, want file.copy: only a whole-file restore puts removed lines back where they were", got)
	}
	params := lineInverseParams(t, rc)
	if got := params["dest"]; got != path {
		t.Errorf("inverse dest = %v, want %q", got, path)
	}
	if got := params["content"]; got != start {
		t.Errorf("inverse content = %q, want the whole prior text including the removed line", got)
	}
	if got := params["mode"]; got != "0640" {
		t.Errorf("inverse mode = %v, want the 0640 the file carried, not the mode a write would leave", got)
	}
	before := lineDiffHalf(t, rc, "before")
	for _, key := range []string{"owner", "group"} {
		if params[key] != before[key] {
			t.Errorf("inverse %s = %v, want %v, the value read off the device", key, params[key], before[key])
		}
	}
}

// TestRemove_ConvergedRunReportsNoChange proves a file holding nothing to
// remove is left alone, sends no write, and emits no inverse.
//
// The mtime assertion is what proves no write was sent. A method that
// rendered and wrote unconditionally would leave identical bytes and would
// still be wrong: it would move the modification time on every run, which
// anything watching the file for changes would act on.
func TestRemove_ConvergedRunReportsNoChange(t *testing.T) {
	server := startLineServer(t)
	rc := newLineContext(server)
	path := lineWriteFile(t, "alpha\nbeta\n", 0o600)

	stamp, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}

	result, err := line.Remove(context.Background(), rc, newLineTarget(server), lineParams(map[string]any{
		"path":   path,
		"regexp": "^nothing-matches-this",
	}))
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if result.Changed {
		t.Error("a run that removed nothing reported a change")
	}
	if got := lineOnDisk(t, path); got != "alpha\nbeta\n" {
		t.Errorf("the file holds %q, want it untouched", got)
	}

	again, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if !again.ModTime().Equal(stamp.ModTime()) {
		t.Error("the modification time moved, so the file was rewritten with identical bytes")
	}
	if got := rc.stats["found"]; got != 0 {
		t.Errorf("found stat = %v, want 0", got)
	}
	if got := rc.stats["msg"]; got != "" {
		t.Errorf("msg stat = %v, want it empty on a run that removed nothing", got)
	}
	if _, emitted := rc.stats["inverse"]; emitted {
		t.Error("a run that removed nothing emitted an inverse, so a rollback would rewrite a file this task never touched")
	}
	if lineDiffHalf(t, rc, "before")["content"] != lineDiffHalf(t, rc, "after")["content"] {
		t.Error("a converged run recorded two different halves, so a reader cannot tell it changed nothing")
	}
}

// TestRemove_AnAbsentFileIsAlreadyDone proves a file that is not there is
// reported as no change rather than as an error.
//
// This is lineinfile's own answer for state: absent and it is the right one:
// a file that does not exist holds no lines to remove, and refusing would
// make a runbook that strips a setting fail on every host that never had the
// file, which is exactly the mixed fleet the task is written for. It is also
// the one place this method and file.line.set deliberately disagree.
func TestRemove_AnAbsentFileIsAlreadyDone(t *testing.T) {
	server := startLineServer(t)
	rc := newLineContext(server)
	path := filepath.Join(t.TempDir(), "not-there.conf")

	result, err := line.Remove(context.Background(), rc, newLineTarget(server), lineParams(map[string]any{
		"path":   path,
		"regexp": "^anything",
	}))
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if result.Changed {
		t.Error("a file that was never there reported a change")
	}
	if _, statErr := os.Lstat(path); !os.IsNotExist(statErr) {
		t.Errorf("os.Lstat(%s) = %v, want the path still absent: this method created something", path, statErr)
	}
	if got := rc.stats["msg"]; got != "file not present" {
		t.Errorf("msg stat = %v, want lineinfile's own wording for an absent file", got)
	}
	if got := rc.stats["found"]; got != 0 {
		t.Errorf("found stat = %v, want 0", got)
	}
	if _, emitted := rc.stats["inverse"]; emitted {
		t.Error("a file that was never there emitted an inverse, so a rollback would create a file nothing removed")
	}
	// The diff still has to be there, and its before half has to say the file
	// was ABSENT rather than empty. Those are different states and they undo
	// differently, which is why the text is left out of the record entirely
	// rather than written as an empty string.
	before := lineDiffHalf(t, rc, "before")
	if got := before["exists"]; got != false {
		t.Errorf("diff before exists = %v, want false", got)
	}
	if _, present := before["content"]; present {
		t.Error("the diff recorded content for a file that was not there, so a reader cannot tell absent from empty")
	}
}

// TestRemove_EmptiesAFileWhenEverythingMatches proves the boundary case
// where nothing survives, which is the one an off by one in the render would
// turn into a stray newline.
func TestRemove_EmptiesAFileWhenEverythingMatches(t *testing.T) {
	server := startLineServer(t)
	rc := newLineContext(server)
	path := lineWriteFile(t, "drop\ndrop\n", 0o600)

	result, err := line.Remove(context.Background(), rc, newLineTarget(server), lineParams(map[string]any{
		"path": path,
		"line": "drop",
	}))
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if !result.Changed {
		t.Error("a run that emptied the file reported no change")
	}
	if got := lineOnDisk(t, path); got != "" {
		t.Errorf("the file holds %q, want it empty rather than holding a leftover newline", got)
	}
	if got := rc.stats["found"]; got != 2 {
		t.Errorf("found stat = %v, want 2", got)
	}
}

// TestRemove_KeepsTheFileEndingItFound is the trailing newline contract for
// this method, and the converged row is the one that catches a render that
// normalizes.
//
// A file with no final newline that has nothing removed from it must not be
// rewritten just to gain one, because that would be a change to the device
// that nobody asked for and it would report as this task's work.
func TestRemove_KeepsTheFileEndingItFound(t *testing.T) {
	tests := []struct {
		name    string
		start   string
		remove  string
		want    string
		changed bool
	}{
		{
			name:    "a file with no final newline keeps none",
			start:   "alpha\ndrop\nbeta",
			remove:  "drop",
			want:    "alpha\nbeta",
			changed: true,
		},
		{
			name:    "a file with no final newline is not rewritten for one",
			start:   "alpha\nbeta",
			remove:  "nothing here",
			want:    "alpha\nbeta",
			changed: false,
		},
		{
			name:    "a file with a final newline keeps it",
			start:   "alpha\ndrop\nbeta\n",
			remove:  "drop",
			want:    "alpha\nbeta\n",
			changed: true,
		},
		{
			// An empty file holds no lines, so there is nothing to match and
			// nothing to write.
			name:    "an empty file holds nothing to remove",
			start:   "",
			remove:  "drop",
			want:    "",
			changed: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := startLineServer(t)
			path := lineWriteFile(t, tt.start, 0o600)

			result, err := line.Remove(context.Background(), newLineContext(server), newLineTarget(server), lineParams(map[string]any{
				"path": path,
				"line": tt.remove,
			}))
			if err != nil {
				t.Fatalf("Remove: %v", err)
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

// TestRemove_KeepsTheFilesPermissions is the assertion that catches the one
// thing an atomic write silently destroys.
//
// remotefile.Write creates its temporary with mktemp, which makes it 0600,
// and renames it over the target. Without the restore, removing one line
// from a world readable file would leave it readable only by the account
// that ran the task, and the task would report success. 0640 is deliberately
// neither mktemp's 0600 nor anything a umask hands out.
func TestRemove_KeepsTheFilesPermissions(t *testing.T) {
	server := startLineServer(t)
	path := lineWriteFile(t, "alpha\ndrop\n", 0o640)

	if _, err := line.Remove(context.Background(), newLineContext(server), newLineTarget(server), lineParams(map[string]any{
		"path": path,
		"line": "drop",
	})); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	if got := lineModeOnDisk(t, path); got != "0640" {
		t.Errorf("the file is %s on disk, want the 0640 it had: the atomic write left the temporary's permissions behind", got)
	}
	if got := lineOnDisk(t, path); got != "alpha\n" {
		t.Errorf("the file holds %q, want the edited text", got)
	}
}

// TestRemove_RefusesSomethingThatIsNotARegularFile proves a link and a
// directory are both refused, and that nothing was written either way.
func TestRemove_RefusesSomethingThatIsNotARegularFile(t *testing.T) {
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
			_, err := line.Remove(context.Background(), newLineContext(server), newLineTarget(server), lineParams(map[string]any{
				"path": tt.path,
				"line": "alpha",
			}))
			if err == nil {
				t.Fatal("expected a refusal, got nil")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %q, want it to say what is at the path", err)
			}
			if got := lineOnDisk(t, target); got != "alpha\n" {
				t.Errorf("the real file holds %q, want it untouched: something was written anyway", got)
			}
		})
	}
}

// TestRemove_TransportFailuresAreReported walks every command this method
// sends and cuts the connection off at each one in turn.
//
// The commands are the same ones file.line.set sends, in the same order, and
// they are checked separately here because each method wires its own error
// handling around the shared helpers: a missing wrap in one of them would
// leave the other's tests entirely green.
func TestRemove_TransportFailuresAreReported(t *testing.T) {
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
			name:    "putting the permissions back",
			budget:  4,
			mode:    0o640,
			want:    []string{"open session", "chmod ", "temporary it was written through"},
			written: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := startLineServerWithSessionBudget(t, tt.budget)
			path := lineWriteFile(t, "alpha\ndrop\n", tt.mode)

			_, err := line.Remove(context.Background(), newLineContext(server), newLineTarget(server), lineParams(map[string]any{
				"path": path,
				"line": "drop",
			}))
			if err == nil {
				t.Fatal("a failed command was reported as success")
			}
			for _, want := range tt.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error = %q, want it to contain %q so the operator knows which step failed", err, want)
				}
			}

			content := lineOnDisk(t, path)
			if tt.written && content != "alpha\n" {
				t.Errorf("the file holds %q, want the edited text the error says is already in place", content)
			}
			if !tt.written && content != "alpha\ndrop\n" {
				t.Errorf("the file holds %q, want the untouched text", content)
			}
		})
	}
}

// TestRemove_RecordFailuresAreReported covers the recording call sites, on
// both the run that changed something and the two that did not.
//
// Swallowing any of them would leave a rollback engine with no record of a
// deletion that really happened, and this method's deletions are the ones
// nothing else holds a copy of.
func TestRemove_RecordFailuresAreReported(t *testing.T) {
	tests := []struct {
		name    string
		failOn  string
		remove  string
		absent  bool
		already []string
	}{
		{name: "the diff", failOn: "diff", remove: "drop"},
		{name: "the inverse", failOn: "inverse", remove: "drop", already: []string{"diff"}},
		{name: "the returned stats", failOn: "found", remove: "drop", already: []string{"diff", "inverse"}},
		{name: "the diff of a converged run", failOn: "diff", remove: "nothing here"},
		{name: "the stats of a converged run", failOn: "found", remove: "nothing here", already: []string{"diff"}},
		{name: "the diff of an absent file", failOn: "diff", remove: "drop", absent: true},
		{name: "the stats of an absent file", failOn: "found", remove: "drop", absent: true, already: []string{"diff"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := startLineServer(t)
			rc := newLineContext(server)
			rc.failOn = tt.failOn

			path := lineWriteFile(t, "alpha\ndrop\n", 0o600)
			if tt.absent {
				path = filepath.Join(t.TempDir(), "not-there.conf")
			}

			_, err := line.Remove(context.Background(), rc, newLineTarget(server), lineParams(map[string]any{
				"path": path,
				"line": tt.remove,
			}))
			if err == nil {
				t.Fatal("a failure to record was swallowed")
			}
			if !errors.Is(err, errLineStat) {
				t.Errorf("error = %q, want it to carry what recording returned", err)
			}
			for _, key := range tt.already {
				if _, recorded := rc.stats[key]; !recorded {
					t.Errorf("%q was not recorded before %q, so the call sites run in the wrong order", key, tt.failOn)
				}
			}
		})
	}
}
