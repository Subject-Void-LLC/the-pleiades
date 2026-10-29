// Tests of what win.file.copy and win.file.download put into an error when
// the host's script fails: PowerShell's own error record, as a Windows host
// writes it to stderr over WinRM, on one line, bounded and quoted by
// pkg/farside (Phase 131, R1), never its raw lines.
package file

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/farside"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/winrmexec"
)

// psDenied is the error record Windows PowerShell 5.1 writes for a write
// the account may not make: CRLF lines, a caret line and the category.
const psDenied = "Set-Content : Access to the path 'C:\\ops\\tools.zip' is denied.\r\n" +
	"At line:14 char:5\r\n" +
	"+     [IO.File]::WriteAllBytes($tmp, $bytes)\r\n" +
	"+     ~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~\r\n" +
	"    + CategoryInfo          : PermissionDenied: (C:\\ops\\tools.zip:String) [Set-Content], UnauthorizedAccessException\r\n" +
	"    + FullyQualifiedErrorId : GetContentWriterUnauthorizedAccessError,Microsoft.PowerShell.Commands.SetContentCommand\r\n"

// curl404 is what curl.exe writes for a URL its server refuses, as the
// download script's fetch step runs it.
const curl404 = "curl: (22) The requested URL returned error: 404\r\n"

// holdsFarSide fails t unless err carries out exactly as farside.Output
// quotes it, on one line and with no control character left to act on.
func holdsFarSide(t *testing.T, err error, out string) {
	t.Helper()
	if err == nil {
		t.Fatal("the failure was not an error")
	}
	want := farside.Output(out)
	if !strings.Contains(err.Error(), want) {
		t.Errorf("err = %v\nwant it to hold %s", err, want)
	}
	if strings.ContainsAny(err.Error(), "\r\n\x1b") {
		t.Errorf("err holds a raw line break or escape: %q", err.Error())
	}
}

// TestCopy_AFailedScriptSaysWhatPowerShellSaid: the read and the write
// each fail with PowerShell's error record, and the error holds it as one
// quoted line; a record past farside.MaxBytes is cut and marked.
func TestCopy_AFailedScriptSaysWhatPowerShellSaid(t *testing.T) {
	params := map[string]any{"src": "a.bin", "path": copyDest}
	files := map[string][]byte{"a.bin": []byte("payload")}
	oldRun, oldWrite := run, runWithStdin
	t.Cleanup(func() { run, runWithStdin = oldRun, oldWrite })

	run = func(context.Context, winrmexec.Target, winrmexec.Auth, winrmexec.Shell, string, winrmexec.Options) (winrmexec.Result, error) {
		return winrmexec.Result{ExitCode: 1, Stderr: psDenied}, nil
	}
	_, err := callCopy(t, false, newProject(files), params)
	holdsFarSide(t, err, psDenied)
	if err != nil && !strings.Contains(err.Error(), `exited 1: "Set-Content : Access to the path 'C:\\ops\\tools.zip' is denied. At line:14`) {
		t.Errorf("the read's error = %v", err)
	}

	run = (&copyHost{files: map[string][]byte{}, folders: map[string]bool{`C:\ops`: true}}).read
	long := strings.Repeat(psDenied, 4)
	runWithStdin = func(context.Context, winrmexec.Target, winrmexec.Auth, winrmexec.Shell, string, string, winrmexec.Options) (winrmexec.Result, error) {
		return winrmexec.Result{ExitCode: 9, Stderr: long}, nil
	}
	_, err = callCopy(t, false, newProject(files), params)
	holdsFarSide(t, err, long)
	if err != nil && (!strings.Contains(err.Error(), `exited 9: "Set-Content`) || !strings.HasSuffix(err.Error(), `..."`)) {
		t.Errorf("a long record is not bounded and marked: %v", err)
	}
}

// TestDownload_AFailedFetchSaysWhatCurlSaid: curl's refusal and a script
// that fails another way each reach the error as one quoted line.
func TestDownload_AFailedFetchSaysWhatCurlSaid(t *testing.T) {
	h := newHost()
	onModel(t, h)
	h.serves = nil
	_, err := call(t, false, &recorder{stats: map[string]any{}}, params())
	holdsFarSide(t, err, curl404)
	if err != nil && !strings.Contains(err.Error(), `(exit 22): "curl: (22) The requested URL returned error: 404"`) {
		t.Errorf("a refused fetch = %v", err)
	}

	run = func(context.Context, winrmexec.Target, winrmexec.Auth, winrmexec.Shell, string, winrmexec.Options) (winrmexec.Result, error) {
		return winrmexec.Result{ExitCode: 1, Stderr: psDenied, Stdout: "before=\r\n"}, nil
	}
	_, err = call(t, false, &recorder{stats: map[string]any{}}, params())
	holdsFarSide(t, err, psDenied+"\n"+"before=\r\n")
	if err != nil && !strings.Contains(err.Error(), `exited 1: "Set-Content : Access`) {
		t.Errorf("a failed script = %v", err)
	}
}
