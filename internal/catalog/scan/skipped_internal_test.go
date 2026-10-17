// Tests for reading what Gitleaks skipped from its standard error.
package scan

import (
	"strings"
	"testing"
)

// gitleaksSkips is what Gitleaks 8.30.0 (the pinned image) wrote to
// standard error at --log-level warn, run as an unprivileged user over a
// tree holding a directory and a file of mode 0000, then the same again
// with a space in each name, copied verbatim. It exited 0 both times. The
// harness's scanners run as their container's root, which reads a file
// of mode 0000, so a method-level test cannot make Gitleaks skip one here;
// an SELinux label or a rootless engine's user mapping does in the field.
const gitleaksSkips = "12:26AM WRN skipping directory error=\"permission denied\" path=/scan/locked\n" +
	"12:26AM WRN skipping file: permission denied path=/scan/secret0.ini\n" +
	"12:26AM WRN leaks found: 1\n" +
	"12:42AM WRN skipping directory error=\"permission denied\" path=\"/scan/dir x\"\n" +
	"12:42AM WRN skipping file: permission denied path=\"/scan/a b/se cret.ini\""

// TestSkipWatch_NamesWhatGitleaksCouldNotRead: every path Gitleaks
// skipped is found, whatever chunks its output arrives in, named on the
// device; a line that skips nothing is not counted.
func TestSkipWatch_NamesWhatGitleaksCouldNotRead(t *testing.T) {
	tree := target{param: paramDir, path: "/srv/app", dir: true}
	for name, chunk := range map[string]int{"whole": len(gitleaksSkips), "a byte at a time": 1, "in sevens": 7} {
		t.Run(name, func(t *testing.T) {
			w := &skipWatch{skips: gitleaksSkip}
			for rest := gitleaksSkips; rest != ""; {
				n := min(chunk, len(rest))
				if _, err := w.Write([]byte(rest[:n])); err != nil {
					t.Fatal(err)
				}
				rest = rest[n:]
			}
			count, paths := w.skipped(tree.onDevice)
			want := "/srv/app/locked|/srv/app/secret0.ini|/srv/app/dir x|/srv/app/a b/se cret.ini"
			if count != 4 || strings.Join(paths, "|") != want {
				t.Errorf("skipped = %d %q, want 4 %q", count, paths, want)
			}
		})
	}
	w := &skipWatch{skips: gitleaksSkip}
	_, _ = w.Write([]byte("12:26AM WRN leaks found: 3\n"))
	if count, _ := w.skipped(tree.onDevice); count != 0 {
		t.Errorf("a line that skips nothing was counted")
	}
}

// TestSkipWatch_IsBounded: past maxListed paths only the count grows,
// and one endless line is held to a bound.
func TestSkipWatch_IsBounded(t *testing.T) {
	tree := target{param: paramDir, path: "/srv/app", dir: true}
	w := &skipWatch{skips: gitleaksSkip}
	for i := 0; i < maxListed*3; i++ {
		_, _ = w.Write([]byte("WRN skipping file: permission denied path=/scan/f\n"))
	}
	_, _ = w.Write([]byte("WRN skipping file: permission denied path=/scan/" + strings.Repeat("x", 1<<20) + "\n"))
	count, paths := w.skipped(tree.onDevice)
	if count != maxListed*3+1 || len(paths) != maxListed {
		t.Errorf("skipped = %d with %d paths", count, len(paths))
	}
	for _, p := range paths {
		if len(p) > 1024 {
			t.Errorf("a path of %d bytes was kept", len(p))
		}
	}
}

// osvExtractionErrors is what OSV-Scanner 2.6.0 (the pinned image) wrote
// to standard error, run as an unprivileged user over a directory holding
// a readable lockfile and one of mode 0000, copied verbatim. It exited 1,
// for the readable lockfile's finding; without one it exits 0.
const osvExtractionErrors = "Warning: `scan` exists as both a subcommand of OSV-Scanner and as a file on the filesystem. `scan` is assumed to be a subcommand here. If you intended for `scan` to be an argument to `scan`, you must specify `scan scan` in your command line.\n" +
	"Scanning dir /scan\n" +
	"Starting filesystem walk for root: /\n" +
	"Scanned /scan/a/package-lock.json file and found 1 package\n" +
	"End status: 3 dirs visited, 5 inodes visited, 1 Extract calls, 131.372\u00b5s elapsed, 131.406\u00b5s wall time\n" +
	"Loaded npm local db from /db/osv-scalibr/npm/all.zip\n" +
	"Error during extraction: (extracting as javascript/packagelockjson) scan/b/package-lock.json: Open(scan/b/package-lock.json): open scan/b/package-lock.json: permission denied\n"

// TestSkipWatch_NamesWhatOSVScannerCouldNotRead: the lockfile OSV-Scanner
// could not read is found in its log and named on the device, for a
// directory and for a lockfile named on its own; its other lines skip
// nothing.
func TestSkipWatch_NamesWhatOSVScannerCouldNotRead(t *testing.T) {
	tree := target{param: paramDir, path: "/srv/app", dir: true}
	w := &skipWatch{skips: osvSkip}
	_, _ = w.Write([]byte(osvExtractionErrors))
	if count, paths := w.skipped(func(p string) string { return sourceOnDevice(tree, nil, p) }); count != 1 || len(paths) != 1 || paths[0] != "/srv/app/b/package-lock.json" {
		t.Errorf("skipped = %d %q", count, paths)
	}
	w = &skipWatch{skips: osvSkip}
	_, _ = w.Write([]byte("Error during extraction: (extracting as javascript/packagelockjson) scan/0/package-lock.json: Open(scan/0/package-lock.json): open scan/0/package-lock.json: permission denied\n"))
	byContainer := map[string]string{"/scan/0/package-lock.json": "/srv/web/package-lock.json"}
	if count, paths := w.skipped(func(p string) string { return sourceOnDevice(target{}, byContainer, p) }); count != 1 || paths[0] != "/srv/web/package-lock.json" {
		t.Errorf("skipped = %d %q", count, paths)
	}
}
