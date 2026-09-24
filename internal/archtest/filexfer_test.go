// The import rules for the file-transfer packages: the port does no I/O, and
// no remote-path code imports path/filepath.
package archtest

import (
	"sort"
	"testing"
)

// fileTransferPortImports is every package pkg/filexfer may import. It
// is an allowlist of pure standard-library packages, none of which can
// open a file, a socket or a process, so that the resolver a transfer
// runs before anything is dialed provably cannot touch the network: a
// Path is refused or built without any I/O at all, which is what makes
// "refused before any network call" a property of the code rather than
// of each caller's discipline.
var fileTransferPortImports = map[string]bool{
	"context":      true,
	"errors":       true,
	"fmt":          true,
	"io":           true,
	"io/fs":        true,
	"path":         true,
	"strings":      true,
	"time":         true,
	"unicode":      true,
	"unicode/utf8": true,
}

// remotePathPackages are the packages that build or judge a path on a
// remote device. None of them may import path/filepath: a remote path is
// always slash-separated, whatever the controller runs on, and a guard
// written with filepath passes on a Linux controller and fails open on a
// Windows one, where the separator is a backslash.
var remotePathPackages = []string{
	modulePath + "/pkg/filexfer",
	modulePath + "/pkg/sftpxfer",
	modulePath + "/pkg/scpxfer",
}

// disallowedImports returns, sorted, every import not in allowed.
func disallowedImports(imports []string, allowed map[string]bool) []string {
	var bad []string
	for _, imp := range imports {
		if !allowed[imp] {
			bad = append(bad, imp)
		}
	}
	sort.Strings(bad)
	return bad
}

// importsAny returns, sorted, every import that appears in banned.
func importsAny(imports []string, banned ...string) []string {
	var hit []string
	for _, imp := range imports {
		for _, b := range banned {
			if imp == b {
				hit = append(hit, imp)
			}
		}
	}
	sort.Strings(hit)
	return hit
}

// TestFileTransferPortImportsNoIO holds pkg/filexfer to its allowlist.
func TestFileTransferPortImportsNoIO(t *testing.T) {
	pkgs := goList(t, false, modulePath+"/pkg/filexfer")
	if len(pkgs) != 1 {
		t.Fatalf("go list found %d packages for pkg/filexfer, want 1", len(pkgs))
	}
	if bad := disallowedImports(pkgs[0].Imports, fileTransferPortImports); len(bad) != 0 {
		t.Errorf("pkg/filexfer imports %v, outside its no-I/O allowlist: the path resolver must be unable to reach a file, a socket or a process", bad)
	}
}

// TestRemotePathCodeNeverImportsFilepath holds every remote-path
// package to the slash-only rule.
func TestRemotePathCodeNeverImportsFilepath(t *testing.T) {
	for _, path := range remotePathPackages {
		pkgs := goList(t, false, path)
		if len(pkgs) != 1 {
			t.Fatalf("go list found %d packages for %s, want 1", len(pkgs), path)
		}
		if hit := importsAny(pkgs[0].Imports, "path/filepath"); len(hit) != 0 {
			t.Errorf("%s imports path/filepath: a remote path is slash-separated on every controller", path)
		}
	}
}

// TestFileTransferImportRulesDetectViolations is the negative control
// for both rules above, so their passes are results rather than empty
// queries.
func TestFileTransferImportRulesDetectViolations(t *testing.T) {
	bad := disallowedImports([]string{"path", "net", "os", "strings"}, fileTransferPortImports)
	if len(bad) != 2 || bad[0] != "net" || bad[1] != "os" {
		t.Errorf("disallowedImports() = %v, want [net os]", bad)
	}
	if hit := importsAny([]string{"path", "path/filepath"}, "path/filepath"); len(hit) != 1 {
		t.Errorf("importsAny() = %v, want [path/filepath]", hit)
	}
	if hit := importsAny([]string{"path"}, "path/filepath"); len(hit) != 0 {
		t.Errorf("importsAny() = %v on a clean list, want nothing", hit)
	}
}
