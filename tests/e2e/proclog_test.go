//go:build integration

// What a harness subprocess printed, kept two ways: the recent lines a
// failure message quotes, without the request lines a polling test floods
// them with, and, when PLEIADES_TEST_ARTIFACTS names a directory, every
// line, masked, in a file kept only for a test that failed, which CI
// uploads. A job log quotes only a failure's last forty lines, and a Runner
// that stopped answering is explained by what it printed minutes earlier.
package e2e

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// artifactsEnv names the directory a failed test's process logs are kept in.
const artifactsEnv = "PLEIADES_TEST_ARTIFACTS"

// routine reports whether line is one of the Controller's per-request lines
// for a read it allowed and answered. A test polling a job every 25
// milliseconds writes two of them each time, so the 60 recent lines a stuck
// job's failure quoted were all polling, and the dispatch and outcome lines
// that would say why it was stuck had been pushed out long before.
func routine(line string) bool {
	switch {
	case strings.Contains(line, `"msg":"admission decision"`):
		return strings.Contains(line, `"allowed":true`)
	case strings.Contains(line, `"msg":"request completed"`):
		return strings.Contains(line, `"method":"GET"`) && strings.Contains(line, `"status":200`)
	}
	return false
}

// openProcLog opens a file for everything process name prints, under the
// directory artifactsEnv names, one folder per test; nil when it names none
// or the file cannot be made, which costs the record and never the test.
// A second process of the same name gets its own numbered file. The file is
// removed when the test passes, so the folder holds only what a failure
// left. Call it before the process's own cleanup is registered, so this one
// runs after the process has stopped and its output is drained.
func openProcLog(tb testing.TB, name string) *os.File {
	tb.Helper()
	root := os.Getenv(artifactsEnv)
	if root == "" {
		return nil
	}
	dir := filepath.Join(root, strings.NewReplacer("/", "__", " ", "_").Replace(tb.Name()))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		tb.Logf("not keeping %s's output: %v", name, err)
		return nil
	}
	var f *os.File
	for i := 1; f == nil; i++ {
		file := name + ".log"
		if i > 1 {
			file = fmt.Sprintf("%s-%d.log", name, i)
		}
		opened, err := os.OpenFile(filepath.Join(dir, file), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) // #nosec G304 -- a file named for this test under the directory CI hands it
		switch {
		case errors.Is(err, fs.ErrExist):
			continue
		case err != nil:
			tb.Logf("not keeping %s's output: %v", name, err)
			return nil
		}
		f = opened
	}
	tb.Cleanup(func() {
		_ = f.Close()
		if !tb.Failed() {
			_ = os.Remove(f.Name())
			_ = os.Remove(dir) // only once it is empty
		}
	})
	return f
}
