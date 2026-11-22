// This file tests the scan against the real gosec: from a checkout deep
// enough that a package's file list is over go/packages' limit, gosec alone
// cannot type-check the package, and gosec-check's scan reads every file,
// reports each finding under the checkout's own path, and leaves nothing
// behind.
package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport/need"
)

// requireGosec returns the gosec on PATH, or stops t when there is none.
func requireGosec(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("gosec")
	need.Require(t, need.Gosec, err == nil, "gosec is not on PATH; make tools installs the pinned one")
	return path
}

// TestScan_RescuesAPackageSplitByALongPath reproduces FAILURE_PATTERNS 1337
// and its fix. Run alone from the deep checkout, gosec loads package big in
// pieces and reports files it could not type-check. The scan reads big from
// the short path instead: no file it could not type-check, a finding in each
// package, each under the deep checkout, and every file counted once.
func TestScan_RescuesAPackageSplitByALongPath(t *testing.T) {
	gosec := requireGosec(t)
	requireSymlinks(t)
	deep := deepDir(t)
	scanned := writeModule(t, deep, 60)

	// gosec alone, as gosec-check ran it before.
	raw := exec.Command(gosec, gosecArgs(excludeDirs, "./...")...)
	raw.Dir = deep
	var stdout, stderr bytes.Buffer
	raw.Stdout, raw.Stderr = &stdout, &stderr
	_ = raw.Run() // gosec exits non-zero on a finding
	alone, err := decodeReport(stdout.Bytes())
	if err != nil {
		t.Fatalf("gosec alone: %v\n%s", err, stderr.String())
	}
	if len(alone.GolangErrors) == 0 {
		t.Fatal("gosec alone type-checked big from the deep path: the split this test reproduces no longer happens, so the rescue may be removable")
	}

	base := filepath.Join(t.TempDir(), shortLinkDir)
	var log strings.Builder
	s := scanner{gosec: gosec, shortBase: base, deadline: time.Now().Add(5 * time.Minute), log: &log}
	report, err := s.scan(deep)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.GolangErrors) != 0 {
		t.Errorf("the scan could not type-check: %v", report.GolangErrors)
	}
	if !strings.Contains(log.String(), "big has 60 files") {
		t.Errorf("log = %q, want it to name big as scanned from the short path", log.String())
	}
	rules := map[string]string{}
	for _, issue := range report.Issues {
		if !strings.HasPrefix(issue.File, deep+string(filepath.Separator)) {
			t.Errorf("finding %s is not under the checkout %s", issue.File, deep)
		}
		rules[filepath.Base(filepath.Dir(issue.File))] += issue.RuleID + " "
	}
	if rules["big"] == "" || rules["small"] == "" {
		t.Errorf("findings by package = %v, want one in big, scanned from the short path, and one in small, scanned from the checkout", rules)
	}
	if report.Stats.Files != len(scanned) {
		t.Errorf("gosec scanned %d files, want %d", report.Stats.Files, len(scanned))
	}
	// The one link is all the scan leaves, and it leads to the checkout.
	entries, err := os.ReadDir(base)
	if err != nil || len(entries) != 1 {
		t.Fatalf("%s holds %v (%v), want one link", base, entries, err)
	}
	if target, err := os.Readlink(filepath.Join(base, entries[0].Name())); err != nil || target != deep {
		t.Errorf("the link leads to %q (%v), want %s", target, err, deep)
	}
}

// TestScan_AShortCheckoutRunsGosecOnceAsBefore: with no package over the
// limit, the scan makes no link and gives gosec the arguments it always had.
func TestScan_AShortCheckoutRunsGosecOnceAsBefore(t *testing.T) {
	gosec := requireGosec(t)
	root := t.TempDir()
	scanned := writeModule(t, root, 5)
	base := filepath.Join(t.TempDir(), shortLinkDir)
	report, err := scanner{gosec: gosec, shortBase: base, log: &strings.Builder{}}.scan(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.GolangErrors) != 0 || report.Stats.Files != len(scanned) {
		t.Errorf("errors %v, %d files; want none and %d", report.GolangErrors, report.Stats.Files, len(scanned))
	}
	if _, err := os.Stat(base); !os.IsNotExist(err) {
		t.Errorf("a short checkout made %s (%v), want nothing made", base, err)
	}
}

// TestScan_RefusesWhatTheShortPathCannotRescue: a package over the limit even
// from the short path stops the scan with an error naming it and the fix,
// before gosec runs and before any link is made.
func TestScan_RefusesWhatTheShortPathCannotRescue(t *testing.T) {
	requireSymlinks(t)
	deep := deepDir(t)
	writeModule(t, deep, 60)
	// A base as deep as the checkout rescues nothing.
	base := filepath.Join(deepDir(t), shortLinkDir)
	_, err := scanner{gosec: "gosec-that-must-not-run", shortBase: base, log: &strings.Builder{}}.scan(deep)
	if err == nil || !strings.Contains(err.Error(), "big (") || !strings.Contains(err.Error(), "shorter path") {
		t.Fatalf("err = %v, want a refusal naming big and the fix", err)
	}
	if _, err := os.Stat(base); !os.IsNotExist(err) {
		t.Errorf("a link that could not help was made in %s (%v)", base, err)
	}
}

// TestRun_AShortCheckoutNeedsNoCacheDirectory: with neither HOME nor
// XDG_CACHE_HOME set, so there is no user cache directory to put a link in,
// a checkout with no package over the limit is still scanned and passes. The
// directory used to be looked up before every scan, and failed this one.
func TestRun_AShortCheckoutNeedsNoCacheDirectory(t *testing.T) {
	requireGosec(t)
	// The Go toolchain finds its own caches from HOME too, so name them
	// first.
	out, err := exec.Command("go", "env", "GOCACHE", "GOPATH", "GOMODCACHE").Output()
	if err != nil {
		t.Fatal(err)
	}
	dirs := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(dirs) != 3 {
		t.Fatalf("go env printed %q", out)
	}
	for i, name := range []string{"GOCACHE", "GOPATH", "GOMODCACHE"} {
		t.Setenv(name, dirs[i])
	}
	// Every variable a platform reads for a home or a cache directory, so
	// the check below holds on Windows (USERPROFILE, LocalAppData) too.
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	t.Setenv("LocalAppData", "")
	t.Setenv("XDG_CACHE_HOME", "")
	if _, err := os.UserCacheDir(); err == nil {
		t.Fatal("a user cache directory was found with HOME and XDG_CACHE_HOME unset")
	}

	root := t.TempDir()
	for name, body := range map[string]string{
		"go.mod":             "module example.com/clean\n\ngo 1.22\n",
		"clean/clean.go":     "// Package clean has nothing for gosec to find.\npackage clean\n\n// Two is two.\nfunc Two() int { return 2 }\n",
		"gosec-waivers.json": `{"waivers": [], "skips": []}` + "\n",
	} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := run(root, time.Minute, ""); err != nil {
		t.Fatalf("run from a short checkout with no cache directory: %v", err)
	}
}
