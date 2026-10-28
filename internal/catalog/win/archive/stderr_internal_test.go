// Tests of what win.archive.extract puts into an error when tar.exe or the
// host's script fails: what each wrote to stderr, on one line, bounded and
// quoted by pkg/farside (Phase 131, R1), never its raw lines.
package archive

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/farside"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/winrmexec"
)

// tarCorrupt is what Windows' bsdtar-based tar.exe writes for a zip whose
// central directory is damaged: its own lines, then its exit summary.
const tarCorrupt = "tar.exe: Truncated input file (needed 1048576 bytes, only 3113 available)\r\n" +
	"tar.exe: Error exit delayed from previous errors.\r\n"

// psFolderDenied is the error record Windows PowerShell 5.1 writes when the
// script may not make the destination folder.
const psFolderDenied = "New-Item : Access to the path 'C:\\Program Files\\go' is denied.\r\n" +
	"At line:9 char:3\r\n" +
	"+   New-Item -ItemType Directory -Path $dest -Force | Out-Null\r\n" +
	"+   ~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~\r\n" +
	"    + CategoryInfo          : PermissionDenied: (C:\\Program Files\\go:String) [New-Item], UnauthorizedAccessException\r\n" +
	"    + FullyQualifiedErrorId : CreateDirectoryUnauthorizedAccessError,Microsoft.PowerShell.Commands.NewItemCommand\r\n"

// TestExtract_AFailureSaysWhatTheHostSaid: tar.exe's refusal and the
// script's own error record each reach the error as farside.Output quotes
// them, on one line with no raw line break.
func TestExtract_AFailureSaysWhatTheHostSaid(t *testing.T) {
	h := &model{files: map[string]bool{`C:\a.zip`: true}, folders: map[string]bool{}}
	h.install(t)
	p := map[string]any{"src": `C:\a.zip`, "dest": `C:\x`}
	for name, tc := range map[string]struct {
		res  winrmexec.Result
		out  string
		says string
	}{
		"tar.exe fails":    {winrmexec.Result{ExitCode: exitTar, Stdout: "dest_existed=False\r\ntar=1\r\n", Stderr: tarCorrupt}, tarCorrupt, `(exit 1): "tar.exe: Truncated input file (needed 1048576 bytes, only 3113 available) tar.exe: Error exit delayed from previous errors."`},
		"the script fails": {winrmexec.Result{ExitCode: 1, Stderr: psFolderDenied}, psFolderDenied + "\n", `exited 1: "New-Item : Access to the path 'C:\\Program Files\\go' is denied. At line:9`},
	} {
		r := tc.res
		h.answer = &r
		_, _, err := extractRun(t, collection.ModeExecute, p)
		if err == nil || !strings.Contains(err.Error(), farside.Output(tc.out)) || !strings.Contains(err.Error(), tc.says) || strings.ContainsAny(err.Error(), "\r\n") {
			t.Errorf("%s: err = %v\nwant it to hold %s", name, err, tc.says)
		}
	}
}
