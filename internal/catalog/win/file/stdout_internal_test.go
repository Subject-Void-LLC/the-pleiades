// Tests of what win.file.copy and win.file.download carry from the host's
// stdout: a script's answer that is not what the script prints (a huge or
// control-laden line, a digest that is not one) reaches an error bounded and
// quoted by pkg/farside, and never a stat or a diff.
package file

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/winrmexec"
)

// hugeLine is far-side output many times farside.MaxBytes, with an escape
// and line breaks a terminal would act on.
var hugeLine = "\x1b[2J" + strings.Repeat("PS banner text ", 2000) + "\r\nmore\r\n"

// bounded fails t unless err exists, is no longer than a sentence and a
// bounded quote can make it, and holds no raw control character.
func bounded(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("the bad answer was not refused")
	}
	if len(err.Error()) > 600 {
		t.Errorf("the error is %d bytes, not bounded: %.120q...", len(err.Error()), err.Error())
	}
	if strings.ContainsAny(err.Error(), "\r\n\x1b") {
		t.Errorf("the error holds a raw line break or escape: %.200q", err.Error())
	}
}

// answering makes every read script answer stdout, exit 0.
func answering(t *testing.T, stdout string) {
	t.Helper()
	oldRun := run
	run = func(context.Context, winrmexec.Target, winrmexec.Auth, winrmexec.Shell, string, winrmexec.Options) (winrmexec.Result, error) {
		return winrmexec.Result{Stdout: stdout}, nil
	}
	t.Cleanup(func() { run = oldRun })
}

// TestCopy_StdoutReachesAnErrorBounded: no digest reported, a digest that
// is not one, and a bad got or after from the write each fail bounded, and
// none is recorded.
func TestCopy_StdoutReachesAnErrorBounded(t *testing.T) {
	params := map[string]any{"src": "a.bin", "path": copyDest}
	files := map[string][]byte{"a.bin": []byte("payload")}
	for _, stdout := range []string{hugeLine, "before=" + hugeLine} {
		answering(t, stdout)
		rc := newProject(files)
		_, err := callCopy(t, false, rc, params)
		bounded(t, err)
		if len(rc.stats) != 0 {
			t.Errorf("a refused answer was recorded: %v", rc.stats)
		}
	}
	if _, err := callCopy(t, true, newProject(files), params); err == nil {
		t.Error("a check took a digest that is not one")
	}

	oldRun, oldWrite := run, runWithStdin
	t.Cleanup(func() { run, runWithStdin = oldRun, oldWrite })
	run = (&copyHost{files: map[string][]byte{}, folders: map[string]bool{`C:\ops`: true}}).read
	for _, w := range []struct {
		key string
		res winrmexec.Result
	}{
		{"got", winrmexec.Result{ExitCode: exitMismatch, Stdout: "got=" + hugeLine}},
		{"after", winrmexec.Result{Stdout: "after=" + hugeLine}},
	} {
		runWithStdin = func(context.Context, winrmexec.Target, winrmexec.Auth, winrmexec.Shell, string, string, winrmexec.Options) (winrmexec.Result, error) {
			return w.res, nil
		}
		_, err := callCopy(t, false, newProject(files), params)
		bounded(t, err)
		if err != nil && !strings.Contains(err.Error(), shown(answers(w.res.Stdout)[w.key], digestPattern)) {
			t.Errorf("%s: the error does not quote the answer as shown does: %.200q", w.key, err.Error())
		}
	}
}

// TestDownload_StdoutReachesAnErrorBounded: the same for win.file.download,
// whose after also reaches the diff, in a run and in a check.
func TestDownload_StdoutReachesAnErrorBounded(t *testing.T) {
	for _, stdout := range []string{
		hugeLine,
		"before=" + hugeLine,
		"before=\nafter=" + hugeLine,
		"before=\ncurl=" + hugeLine,
	} {
		exit := 0
		if strings.Contains(stdout, "curl=") {
			exit = exitFetch
		}
		oldRun := run
		run = func(context.Context, winrmexec.Target, winrmexec.Auth, winrmexec.Shell, string, winrmexec.Options) (winrmexec.Result, error) {
			return winrmexec.Result{Stdout: stdout, ExitCode: exit}, nil
		}
		for _, check := range []bool{false, true} {
			rc := &recorder{stats: map[string]any{}}
			_, err := call(t, check, rc, params())
			bounded(t, err)
			if len(rc.stats) != 0 {
				t.Errorf("check %v: a refused answer was recorded: %v", check, rc.stats)
			}
		}
		run = oldRun
	}
	// A mismatch quotes the digest it got, bounded.
	h := newHost()
	onModel(t, h)
	h.serves[imageURL] = hugeLine
	_, err := call(t, false, &recorder{stats: map[string]any{}}, params())
	bounded(t, err)
}
