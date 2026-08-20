package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// TestRun_Idempotent runs the full generator twice into a scratch working
// directory and asserts byte-for-byte identical output, the same property
// this generator's own doc comments claim ("regenerating must produce no
// diff") and which was previously only checked by hand with sha256sum
// during development. Runs in a temp $PWD (rather than passing outDir
// through, which run() does not accept) because writeSchema's wellKnownDir
// is a fixed, repo-root-relative constant; chdir'ing is the only way to
// exercise the real run() entry point without writing into this package's
// own tools/gendocs/ directory.
func TestRun_Idempotent(t *testing.T) {
	origWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd(): %v", err)
	}
	tmp := t.TempDir()
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("os.Chdir(%s): %v", tmp, err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(origWD); err != nil {
			t.Fatalf("os.Chdir(%s) (restore): %v", origWD, err)
		}
	})

	if err := run(); err != nil {
		t.Fatalf("run() (first pass): %v", err)
	}
	first := snapshotTree(t, ".")

	if err := run(); err != nil {
		t.Fatalf("run() (second pass): %v", err)
	}
	second := snapshotTree(t, ".")

	if len(first) != len(second) {
		t.Fatalf("file count changed across reruns: %d then %d", len(first), len(second))
	}
	for path, content := range first {
		other, ok := second[path]
		if !ok {
			t.Errorf("%s present after first run, missing after second", path)
			continue
		}
		if content != other {
			t.Errorf("%s changed content across an identical rerun (not idempotent)", path)
		}
	}

	assertExists := func(rel string) {
		if _, ok := first[rel]; !ok {
			t.Errorf("expected generated file %s, not found", rel)
		}
	}
	assertExists(filepath.Join(outputDir, "index.md"))
	assertExists(filepath.Join(outputDir, "modules", "index.md"))
	assertExists(filepath.Join(outputDir, "modules", "net", "catalyst", "device_facts.md"))
	assertExists(filepath.Join(outputDir, "capabilities.md"))
	assertExists(filepath.Join(outputDir, "devices.md"))
	assertExists(filepath.Join(outputDir, "plugins.md"))
	assertExists(filepath.Join(outputDir, "filters", "index.md"))
	assertExists(filepath.Join(outputDir, "task-keys.md"))
	assertExists(filepath.Join(outputDir, "implementation-status.md"))
	assertExists(filepath.Join(outputDir, "schemas", "runbook.schema.json"))
	assertExists(filepath.Join(outputDir, "schemas", "inventory.schema.json"))
	assertExists(filepath.Join(outputDir, "schemas", "module-catalog.json"))
	assertExists(filepath.Join(wellKnownDir, "runbook.schema.json"))
	assertExists(filepath.Join(wellKnownDir, "inventory.schema.json"))
	assertExists(filepath.Join(wellKnownDir, "module-catalog.json"))

	for _, name := range []string{"runbook.schema.json", "inventory.schema.json", "module-catalog.json"} {
		browsable := first[filepath.Join(outputDir, "schemas", name)]
		embedded := first[filepath.Join(wellKnownDir, name)]
		if browsable != embedded {
			t.Errorf("docs/reference/schemas/%s and internal/api/wellknown/%s diverge; go:embed and the browsable copy must be byte-identical", name, name)
		}
	}
}

func snapshotTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		out[rel] = string(data)
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	return out
}
