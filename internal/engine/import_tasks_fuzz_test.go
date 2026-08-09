package engine_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"go.yaml.in/yaml/v3"
)

// FuzzImportTasksPath proves resolveImportPath's file-path boundary
// (import_tasks.go) never panics and, more importantly, never lets a
// resolved path escape the runbook's own directory: a sentinel file
// planted just outside that directory must never appear in the built
// DAG, no matter what params.file value the fuzzer tries.
func FuzzImportTasksPath(f *testing.F) {
	seeds := []string{
		"common.yaml",
		"../../../etc/passwd",
		"/etc/passwd",
		"..\\..\\windows\\system.ini",
		"",
		".",
		"..",
		"sub/../../escape.yaml",
		"a.yaml",
		"a.yaml\x00.yaml",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	dir := f.TempDir()
	outsideDir := f.TempDir()
	if err := os.WriteFile(filepath.Join(outsideDir, "secret.yaml"), []byte("- name: SENTINEL_ESCAPED\n  fqcn: noop\n"), 0o600); err != nil {
		f.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.yaml"), []byte("- name: a task\n  fqcn: noop\n"), 0o600); err != nil {
		f.Fatal(err)
	}
	// A same-named sentinel file also planted one level up from dir, so a
	// "../secret.yaml"-shaped seed has something real to (wrongly) find if
	// the boundary check has a bug rather than merely erroring on a
	// missing file either way.
	if err := os.WriteFile(filepath.Join(filepath.Dir(dir), "secret.yaml"), []byte("- name: SENTINEL_ESCAPED\n  fqcn: noop\n"), 0o600); err != nil {
		f.Fatal(err)
	}

	eval, err := engine.NewCELEvaluator()
	if err != nil {
		f.Fatal(err)
	}
	builder := engine.NewBuilder(eval)

	f.Fuzz(func(t *testing.T, file string) {
		doc := map[string]any{
			"id": "fuzz-import-tasks",
			"tasks": []map[string]any{
				{
					"name": "import",
					"fqcn": "import_tasks",
					"params": map[string]any{
						"file": file,
					},
				},
			},
		}
		data, err := yaml.Marshal(doc)
		if err != nil {
			t.Fatalf("marshaling fuzz runbook: %v", err)
		}
		runbookPath := filepath.Join(dir, "fuzz_runbook.yaml")
		if err := os.WriteFile(runbookPath, data, 0o600); err != nil {
			t.Fatalf("writing fuzz runbook: %v", err)
		}

		dag, err := builder.BuildFromYAMLFile(runbookPath)
		if err != nil {
			return // a rejection is always an acceptable outcome
		}
		for _, task := range dag.Nodes {
			if task.Name == "SENTINEL_ESCAPED" {
				t.Fatalf("import_tasks escaped the runbook directory with params.file=%q", file)
			}
		}
	})
}
