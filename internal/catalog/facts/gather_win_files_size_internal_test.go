// Package facts: the Windows files script, with its Win32 helper, reaches the host
// for as many paths as one task may name: on standard input, behind a loader
// that fits the command line.
package facts

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/winrmexec"
)

func TestWindowsFilesScript_FitsTheCommandLineAtItsLargest(t *testing.T) {
	paths := make([]string, gatherMaxFiles)
	for i := range paths {
		paths[i] = `C:\` + strings.Repeat("d", 150) + fmt.Sprintf(`\%03d.txt`, i)
	}
	script := windowsFileProbe().commandFor(paths)
	line, stdin := windowsScriptAndStdin(script, winrmexec.Options{})
	if err := winrmexec.CheckScript(winrmexec.ShellPowerShell, line, winrmexec.Options{}); err != nil {
		t.Fatalf("%d paths: the line sent does not fit: %v", len(paths), err)
	}
	if line != windowsStdinLoader || stdin != script {
		t.Fatalf("a script past the command line was sent on it (%d bytes)", len(script))
	}
	if err := winrmexec.CheckText("the files script", stdin); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(script, "\\"+"u") || strings.Contains(script, "`") {
		t.Fatal("the files script holds a backslash-u or a backtick")
	}
}
