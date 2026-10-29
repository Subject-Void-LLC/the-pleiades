// Tests for a machine with a data directory: every script puts the trees,
// the build and module caches and the temporary directory under it, reads
// the space free there, and clears the run's own leftovers there.
package oscheck

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// onData is a FreeBSD lab machine whose variable data goes under /data.
var onData = POSIX{Tool: "bsdcheck", Machine: "the FreeBSD machine", Dir: ".pleiades-bsdcheck", Path: "/usr/local/bin", Files: ".bsdcheck", Data: "/data", Dedicated: true}

// TestDataDirectory_EveryScriptUsesIt: with a Data directory the trees'
// directory, the build cache, the module cache and the temporary directory
// are under it, and the space free is read there; without one, everything is
// where it was, under the login's home.
func TestDataDirectory_EveryScriptUsesIt(t *testing.T) {
	test := onData.TestScript("abc", 1, []string{"a/b"})
	for _, w := range []string{
		`export GOCACHE="/data/.pleiades-bsdcheck/.gocache"`,
		`mkdir -p "/data/tmp" "/data/gomod" || exit 2`,
		`export TMPDIR="/data/tmp" GOMODCACHE="/data/gomod"`,
		`cd "/data/.pleiades-bsdcheck/abc" || exit 2`,
	} {
		if !strings.Contains(test, w) {
			t.Errorf("the test script does not hold %q:\n%s", w, test)
		}
	}
	if strings.Contains(test, "$HOME") {
		t.Errorf("the test script still reads the login's home:\n%s", test)
	}
	ready := onData.readyScript(nil)
	for _, w := range []string{
		`free_gb() { df -Pk "/data" |`,
		`base="/data/.pleiades-bsdcheck"`,
		`for d in /tmp "${TMPDIR:-/tmp}" "/data/tmp"; do`,
		`if [ "$(free_gb)" -lt 4 ]; then rm -rf "/data/gomod"; fi`,
	} {
		if !strings.Contains(ready, w) {
			t.Errorf("the ready script does not hold %q:\n%s", w, ready)
		}
	}
	for name, script := range map[string]string{
		"prepare": onData.prepareScript("abc"),
		"records": onData.RecordsScript("abc"),
		"stop":    onData.StopScript("abc"),
	} {
		if !strings.Contains(script, "/data/.pleiades-bsdcheck/") && !strings.Contains(script, `base="/data/.pleiades-bsdcheck"`) {
			t.Errorf("the %s script does not use the data directory:\n%s", name, script)
		}
		if strings.Contains(script, "$HOME") {
			t.Errorf("the %s script still reads the login's home:\n%s", name, script)
		}
	}
	if !strings.HasSuffix(onData.prepareScript("abc"), `printf '%s\n' "/data"`) {
		t.Errorf("the prepare script does not answer the data directory:\n%s", onData.prepareScript("abc"))
	}
	home := posix
	home.Dedicated = true
	if s := home.TestScript("abc", 1, []string{"a/b"}); strings.Contains(s, "GOMODCACHE") || !strings.Contains(s, `cd "$HOME/.pleiades-maccheck/abc"`) {
		t.Errorf("without a data directory the test script moved the caches:\n%s", s)
	}
	if s := home.readyScript(nil); !strings.Contains(s, `df -Pk "$HOME"`) || strings.Contains(s, "gomod") {
		t.Errorf("without a data directory the ready script reads elsewhere:\n%s", s)
	}
	if home.dropCache(CacheDir) != `rm -rf "$HOME/.pleiades-maccheck/.gocache"; ` && home.DropCache {
		t.Errorf("dropCache = %q", home.dropCache(CacheDir))
	}
}

// TestDataDirectory_ReadyScriptClearsItsOwn: run under sh with a stand-in
// data directory, the ready script empties the run's temporary directory
// there of go-build and Test* folders, keeps anything else, and removes the
// module cache there only while short.
func TestDataDirectory_ReadyScriptClearsItsOwn(t *testing.T) {
	needsSh(t)
	data := t.TempDir()
	bin := t.TempDir()
	for _, dir := range []string{"tmp/go-build1", "tmp/TestX2", "tmp/keep", "gomod/cache", ".pleiades-bsdcheck/old", ".pleiades-bsdcheck/.gocache"} {
		if err := os.MkdirAll(filepath.Join(data, dir), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	// df says 100 GB free: nothing is short.
	df := "#!/bin/sh\necho 'Filesystem 1024-blocks Used Available Capacity Mounted'\necho '/dev/x 999999999 1 104857600 1% /data'\n"
	stub := "#!/bin/sh\nexit 0\n"
	for name, body := range map[string]string{"df": df, "go": stub} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o700); err != nil { // #nosec G306 -- a test's own stand-in program
			t.Fatal(err)
		}
	}
	x := onData
	x.Data, x.Path = data, bin
	script := strings.ReplaceAll(x.readyScript(nil), `for d in /tmp "${TMPDIR:-/tmp}"`, "for d in")
	cmd := exec.Command("/bin/sh", "-c", script)
	cmd.Env = append(os.Environ(), "HOME="+t.TempDir(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	left, _ := os.ReadDir(filepath.Join(data, "tmp"))
	var names []string
	for _, e := range left {
		names = append(names, e.Name())
	}
	if strings.Join(names, " ") != "keep" {
		t.Errorf("the data directory's tmp holds %v, want only keep", names)
	}
	if _, err := os.Stat(filepath.Join(data, "gomod", "cache")); err != nil {
		t.Error("the module cache was removed with space to spare")
	}
	if lines := strings.Fields(string(out)); len(lines) != 3 || lines[1] != "100" {
		t.Errorf("the script printed %q", out)
	}
}
