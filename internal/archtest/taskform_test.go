// Nothing this repository ships writes a task as fqcn: and params:.
package archtest

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/validate"
)

// fqcnKey is a YAML line that writes a task's method under fqcn:, in a
// runbook file or a fenced example. Prose naming the key in backticks does
// not start a line with it.
var fqcnKey = regexp.MustCompile(`^\s*-?\s*fqcn:\s*\S`)

// TestShippedRunbooksNameTheirMethodAsKey: a runbook is written with its
// method as the task's key. The engine still reads fqcn: and params: for
// older runbooks, and that kept the form alive: the converter emitted it,
// pleiades init's sample used it, and the docs taught it as one of two
// equal ways, so every runbook copied from them carried it on. This holds
// every shipped document and example, and init's sample, to the one form;
// the converter's own output is held by internal/forge/playbook's
// TestEmit_ModuleAsKey, and each method's documented examples by
// TestEveryDocumentedExampleValidates.
func TestShippedRunbooksNameTheirMethodAsKey(t *testing.T) {
	root := repoRoot(t)
	var files []string
	for _, top := range []string{"docs", "examples"} {
		_ = filepath.WalkDir(filepath.Join(root, top), func(path string, d os.DirEntry, err error) error {
			if ext := filepath.Ext(path); err == nil && !d.IsDir() && (ext == ".md" || ext == ".yaml" || ext == ".yml") {
				files = append(files, path)
			}
			return nil
		})
	}
	for _, name := range []string{"README.md", "CONTRIBUTING.md", "CLAUDE.md"} {
		files = append(files, filepath.Join(root, name))
	}
	if len(files) < 50 {
		t.Fatalf("found %d files, so the walk is not reaching docs and examples", len(files))
	}
	for _, path := range files {
		f, err := os.Open(path) // #nosec G304 -- a file under this repository's own docs or examples
		if err != nil {
			continue
		}
		scan := bufio.NewScanner(f)
		for n := 1; scan.Scan(); n++ {
			if fqcnKey.MatchString(scan.Text()) {
				rel, _ := filepath.Rel(root, path)
				t.Errorf("%s:%d writes a task as fqcn:; write its method as the task's key: %s", rel, n, strings.TrimSpace(scan.Text()))
			}
		}
		_ = f.Close()
	}

	dir := t.TempDir()
	if _, err := inventory.Scaffold(dir); err != nil {
		t.Fatal(err)
	}
	sample, err := os.ReadFile(filepath.Join(dir, inventory.DefaultRunbookDir, inventory.DefaultSampleRunbook))
	if err != nil {
		t.Fatal(err)
	}
	if w := validate.TaskFormWarnings(sample); len(w) != 0 {
		t.Errorf("pleiades init's sample writes a task as fqcn: %s", w[0].Message)
	}
}
