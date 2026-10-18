// Tests for win.file.copy's parameters, branches and records, against a
// model host that reads the scripts' values and decodes the standard input
// the write sends, over a modeled filesystem. The scripts themselves run on
// a real host in the lab.
package file

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/winrmexec"
)

const copyDest = `C:\ops\tools.zip`

// copyHost is a host's files by path, its folders, and what each call sent.
type copyHost struct {
	files   map[string][]byte
	folders map[string]bool
	reads   int
	writes  []string
	corrupt bool
	fail    error
}

// read plays out the read script.
func (h *copyHost) read(_ context.Context, _ winrmexec.Target, _ winrmexec.Auth, _ winrmexec.Shell, script string, _ winrmexec.Options) (winrmexec.Result, error) {
	h.reads++
	if h.fail != nil {
		return winrmexec.Result{}, h.fail
	}
	data, ok := h.files[assignment(script, "path")]
	if !ok {
		return winrmexec.Result{Stdout: "before=\r\n"}, nil
	}
	return winrmexec.Result{Stdout: fmt.Sprintf("before=%s\r\nsize=%d\r\n", digest(data), len(data))}, nil
}

// write plays out the write script: the folder, the stdin decoded line by
// line, the digest compared, the move.
func (h *copyHost) write(_ context.Context, _ winrmexec.Target, _ winrmexec.Auth, _ winrmexec.Shell, script, stdin string, _ winrmexec.Options) (winrmexec.Result, error) {
	h.writes = append(h.writes, stdin)
	path, want := assignment(script, "path"), assignment(script, "want")
	if !h.folders[path[:strings.LastIndex(path, `\`)]] {
		return winrmexec.Result{ExitCode: exitNoFolder}, nil
	}
	var data []byte
	for _, line := range strings.Split(stdin, "\n") {
		if line == "" {
			continue
		}
		b, err := base64.StdEncoding.DecodeString(line)
		if err != nil {
			return winrmexec.Result{Stderr: err.Error(), ExitCode: 1}, nil
		}
		data = append(data, b...)
	}
	if h.corrupt {
		data = append(data, 'x')
	}
	if got := digest(data); got != want {
		return winrmexec.Result{Stdout: "got=" + got + "\r\n", ExitCode: exitMismatch}, nil
	}
	h.files[path] = data
	return winrmexec.Result{Stdout: fmt.Sprintf("after=%s\r\nsize=%d\r\n", want, len(data))}, nil
}

func digest(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// onCopyHost makes the method reach h.
func onCopyHost(t *testing.T, h *copyHost) {
	t.Helper()
	oldRun, oldWrite := run, runWithStdin
	run, runWithStdin = h.read, h.write
	t.Cleanup(func() { run, runWithStdin = oldRun, oldWrite })
}

// projectRecorder is a recorder that reads project files from files.
type projectRecorder struct {
	recorder
	files map[string][]byte
}

func (p *projectRecorder) ReadProjectFile(name string, max int64) ([]byte, error) {
	data, ok := p.files[name]
	if !ok {
		return nil, errors.New("no such project file")
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("over %d bytes", max)
	}
	return data, nil
}

func newProject(files map[string][]byte) *projectRecorder {
	return &projectRecorder{recorder: recorder{stats: map[string]any{}}, files: files}
}

func callCopy(t *testing.T, check bool, rc sdk.RunbookContext, params map[string]any) (collection.Result, error) {
	t.Helper()
	d, ok := collection.Lookup("win.file.copy")
	if !ok {
		t.Fatal("win.file.copy is not registered")
	}
	if check {
		return d.Check(context.Background(), rc, host, params)
	}
	return d.Invoke(context.Background(), rc, host, params)
}

// TestCopy: a check sends nothing; the run sends the file and records its
// digest and the diff; again sends nothing and is no change; a file of
// another digest is replaced; a file over many stdin lines arrives whole.
func TestCopy(t *testing.T) {
	big := make([]byte, 3*copyLine+17)
	for i := range big {
		big[i] = byte(i * 7)
	}
	h := &copyHost{files: map[string][]byte{}, folders: map[string]bool{`C:\ops`: true}}
	onCopyHost(t, h)
	params := map[string]any{"src": "dist/tools.zip", "path": copyDest}

	check := newProject(map[string][]byte{"dist/tools.zip": big})
	if res, err := callCopy(t, true, check, params); err != nil || !res.Changed || len(h.writes) != 0 {
		t.Fatalf("check = %+v, %v, %d writes", res, err, len(h.writes))
	}
	rc := newProject(map[string][]byte{"dist/tools.zip": big})
	if res, err := callCopy(t, false, rc, params); err != nil || !res.Changed {
		t.Fatalf("copy = %+v, %v", res, err)
	}
	if string(h.files[copyDest]) != string(big) || strings.Count(h.writes[0], "\n") != 4 {
		t.Errorf("the host holds %d bytes from %d lines", len(h.files[copyDest]), strings.Count(h.writes[0], "\n"))
	}
	if rc.stats[statSHA256] != digest(big) || rc.stats[statSize] != int64(len(big)) {
		t.Errorf("stats %v", rc.stats)
	}
	if d, _ := rc.stats[sdk.StatDiff].(map[string]any); d[sdk.DiffBefore].(map[string]any)[statSHA256] != "" {
		t.Errorf("diff %v", d)
	}
	again := newProject(map[string][]byte{"dist/tools.zip": big})
	if res, err := callCopy(t, false, again, params); err != nil || res.Changed || len(h.writes) != 1 {
		t.Errorf("again = %+v, %v, %d writes", res, err, len(h.writes))
	}
	newer := newProject(map[string][]byte{"dist/tools.zip": []byte("v2")})
	if res, err := callCopy(t, false, newer, params); err != nil || !res.Changed || string(h.files[copyDest]) != "v2" {
		t.Errorf("a new version = %+v, %v, host %q", res, err, h.files[copyDest])
	}
}

// TestCopy_Failures: a missing folder, a copy whose digest differs on the
// host, a host that cannot be reached, a script that fails, and stats that
// cannot be kept each fail the task.
func TestCopy_Failures(t *testing.T) {
	params := map[string]any{"src": "a.bin", "path": copyDest}
	files := map[string][]byte{"a.bin": []byte("payload")}

	h := &copyHost{files: map[string][]byte{}, folders: map[string]bool{}}
	onCopyHost(t, h)
	if _, err := callCopy(t, false, newProject(files), params); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("no folder: %v", err)
	}
	h.folders[`C:\ops`], h.corrupt = true, true
	if _, err := callCopy(t, false, newProject(files), params); err == nil || !strings.Contains(err.Error(), "nothing was written") || h.files[copyDest] != nil {
		t.Errorf("a mismatch: %v", err)
	}
	h.corrupt, h.fail = false, errors.New("connection refused")
	if _, err := callCopy(t, false, newProject(files), params); err == nil || !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("unreachable: %v", err)
	}
	h.fail = nil
	rc := newProject(files)
	rc.failOn = statSHA256
	if _, err := callCopy(t, false, rc, params); err == nil {
		t.Error("a refused stat passed")
	}
	rc = newProject(files)
	rc.failOn = sdk.StatDiff
	if _, err := callCopy(t, true, rc, params); err == nil {
		t.Error("a refused diff passed")
	}
	old := runWithStdin
	runWithStdin = func(context.Context, winrmexec.Target, winrmexec.Auth, winrmexec.Shell, string, string, winrmexec.Options) (winrmexec.Result, error) {
		return winrmexec.Result{ExitCode: 9, Stderr: "Access is denied"}, nil
	}
	if _, err := callCopy(t, false, newProject(map[string][]byte{"a.bin": []byte("other")}), params); err == nil || !strings.Contains(err.Error(), "exited 9") {
		t.Errorf("a script that fails: %v", err)
	}
	runWithStdin = func(context.Context, winrmexec.Target, winrmexec.Auth, winrmexec.Shell, string, string, winrmexec.Options) (winrmexec.Result, error) {
		return winrmexec.Result{}, errors.New("the shell closed")
	}
	if _, err := callCopy(t, false, newProject(map[string][]byte{"a.bin": []byte("third")}), params); err == nil || !strings.Contains(err.Error(), "shell closed") {
		t.Errorf("a write that cannot run: %v", err)
	}
	runWithStdin = func(context.Context, winrmexec.Target, winrmexec.Auth, winrmexec.Shell, string, string, winrmexec.Options) (winrmexec.Result, error) {
		return winrmexec.Result{Stdout: "after=deadbeef\r\n"}, nil
	}
	if _, err := callCopy(t, false, newProject(map[string][]byte{"a.bin": []byte("fourth")}), params); err == nil || !strings.Contains(err.Error(), "after the copy") {
		t.Errorf("a host that reports another digest: %v", err)
	}
	runWithStdin = old
	run = func(context.Context, winrmexec.Target, winrmexec.Auth, winrmexec.Shell, string, winrmexec.Options) (winrmexec.Result, error) {
		return winrmexec.Result{ExitCode: 1, Stderr: "denied"}, nil
	}
	if _, err := callCopy(t, false, newProject(files), params); err == nil || !strings.Contains(err.Error(), "exited 1") {
		t.Errorf("a read that fails: %v", err)
	}
	run = func(context.Context, winrmexec.Target, winrmexec.Auth, winrmexec.Shell, string, winrmexec.Options) (winrmexec.Result, error) {
		return winrmexec.Result{Stdout: "nothing useful"}, nil
	}
	if _, err := callCopy(t, false, newProject(files), params); err == nil || !strings.Contains(err.Error(), "did not report") {
		t.Errorf("a read with no digest: %v", err)
	}
}

// TestCopy_RefusedBeforeTheHost: what a task may not ask is refused with
// nothing sent.
func TestCopy_RefusedBeforeTheHost(t *testing.T) {
	h := &copyHost{files: map[string][]byte{}, folders: map[string]bool{`C:\ops`: true}}
	onCopyHost(t, h)
	files := map[string][]byte{"a.bin": []byte("x")}
	for name, params := range map[string]map[string]any{
		"no src":          {"path": copyDest},
		"an absolute src": {"src": "/etc/passwd", "path": copyDest},
		"a src leaving":   {"src": "../secret", "path": copyDest},
		"an unclean src":  {"src": "a/../a.bin", "path": copyDest},
		"no path":         {"src": "a.bin"},
		"a relative path": {"src": "a.bin", "path": `ops\tools.zip`},
		"a quote in path": {"src": "a.bin", "path": `C:\ops\it's.zip`},
		"a .. in path":    {"src": "a.bin", "path": `C:\ops\..\tools.zip`},
		"a zero timeout":  {"src": "a.bin", "path": copyDest, "timeout": 0},
		"a word timeout":  {"src": "a.bin", "path": copyDest, "timeout": "soon"},
		"a missing src":   {"src": "nope.bin", "path": copyDest},
	} {
		if _, err := callCopy(t, false, newProject(files), params); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if _, err := callCopy(t, false, &recorder{stats: map[string]any{}}, map[string]any{"src": "a.bin", "path": copyDest}); !errors.Is(err, sdk.ErrNoProjectFiles) {
		t.Errorf("a run with no project files: %v", err)
	}
	d, _ := collection.Lookup("win.file.copy")
	if _, err := d.Invoke(context.Background(), newProject(files), nil, map[string]any{"src": "a.bin", "path": copyDest}); err == nil {
		t.Error("no device was accepted")
	}
	if h.reads != 0 || len(h.writes) != 0 {
		t.Errorf("a refused task reached the host: %d reads, %d writes", h.reads, len(h.writes))
	}
}

// TestCopy_Scripts: the read changes nothing; the write streams line by
// line and moves the file into place only after its digest is checked.
func TestCopy_Scripts(t *testing.T) {
	c := copyRequest{path: copyDest, sha256: digest([]byte("x"))}
	if r := c.readScript(); strings.Contains(r, "Move-Item") || strings.Contains(r, "Remove-Item") || assignment(r, "path") != copyDest {
		t.Errorf("the read script changes something or names another path:\n%s", r)
	}
	w := c.writeScript()
	for _, want := range []string{"$path = '" + copyDest + "'", "$want = '" + c.sha256 + "'", "[Console]::In.ReadLine()", "[Convert]::FromBase64String($line)"} {
		if !strings.Contains(w, want) {
			t.Errorf("the write script lacks %q", want)
		}
	}
	if strings.Contains(w, "ReadToEnd") {
		t.Error("the write script reads its input whole")
	}
	if strings.Index(w, "if ($got -ne $want)") > strings.Index(w, "Move-Item") {
		t.Error("the file is moved into place before its digest is checked")
	}
}
