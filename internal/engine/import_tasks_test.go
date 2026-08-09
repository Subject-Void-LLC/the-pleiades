package engine_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
)

// writeRunbookFile writes content to name under dir and returns the full
// path, failing the test on any I/O error.
func writeRunbookFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
	return path
}

func newTestBuilder(t *testing.T) *engine.Builder {
	t.Helper()
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatalf("NewCELEvaluator: %v", err)
	}
	return engine.NewBuilder(eval)
}

// TestBuildFromYAMLFile_ImportTasksSplicesInline proves import_tasks
// produces the identical *DAG as writing the same tasks inline as a
// block: this is the direct regression test the Release Gate names, not
// just a build check.
func TestBuildFromYAMLFile_ImportTasksSplicesInline(t *testing.T) {
	dir := t.TempDir()
	writeRunbookFile(t, dir, "common.yaml", `
- name: step one
  fqcn: noop
- name: step two
  fqcn: noop
`)
	viaImport := writeRunbookFile(t, dir, "via_import.yaml", `
id: via-import
tasks:
  - name: shared setup
    fqcn: import_tasks
    params:
      file: common.yaml
`)
	viaInline := writeRunbookFile(t, dir, "via_inline.yaml", `
id: via-inline
tasks:
  - name: shared setup
    block:
      - name: step one
        fqcn: noop
      - name: step two
        fqcn: noop
`)

	builder := newTestBuilder(t)

	imported, err := builder.BuildFromYAMLFile(viaImport)
	if err != nil {
		t.Fatalf("BuildFromYAMLFile(via_import.yaml): %v", err)
	}
	inline, err := builder.BuildFromYAMLFile(viaInline)
	if err != nil {
		t.Fatalf("BuildFromYAMLFile(via_inline.yaml): %v", err)
	}

	if len(imported.Nodes) != len(inline.Nodes) {
		t.Fatalf("node count mismatch: imported=%d inline=%d", len(imported.Nodes), len(inline.Nodes))
	}
	for id, task := range inline.Nodes {
		got, ok := imported.Nodes[id]
		if !ok {
			t.Fatalf("imported DAG missing node %q present in the inline DAG", id)
		}
		if got.Name != task.Name || got.FQCN != task.FQCN {
			t.Errorf("node %q mismatch: imported={%q,%q} inline={%q,%q}", id, got.Name, got.FQCN, task.Name, task.FQCN)
		}
	}
	if imported.EntryPoint != inline.EntryPoint {
		t.Errorf("EntryPoint mismatch: imported=%q inline=%q", imported.EntryPoint, inline.EntryPoint)
	}
}

// TestBuildFromYAMLFile_ImportTasksModuleAsKeySugar proves module-as-key
// sugar syntax (task_syntax.go) is rewritten inside an import_tasks file
// too, not just the top-level runbook: resolveOneImport parses the
// imported file's bare task list through its own decode path
// (normalizeWorkflowYAMLTaskList), separate from parseWorkflowYAML's, and
// both must apply the same normalization.
func TestBuildFromYAMLFile_ImportTasksModuleAsKeySugar(t *testing.T) {
	dir := t.TempDir()
	writeRunbookFile(t, dir, "common.yaml", `
- name: show version
  net.cli.command:
    command: "show version"
`)
	main := writeRunbookFile(t, dir, "main.yaml", `
id: import-sugar
tasks:
  - name: shared setup
    fqcn: import_tasks
    params:
      file: common.yaml
`)

	builder := newTestBuilder(t)
	dag, err := builder.BuildFromYAMLFile(main)
	if err != nil {
		t.Fatalf("BuildFromYAMLFile(main.yaml): %v", err)
	}

	task, ok := dag.Nodes["tasks[0].block[0]"]
	if !ok {
		t.Fatalf("expected spliced-in node tasks[0].block[0], got nodes: %v", dag.Nodes)
	}
	if task.FQCN != "net.cli.command" {
		t.Errorf("expected fqcn %q, got %q", "net.cli.command", task.FQCN)
	}
	if task.Params["command"] != "show version" {
		t.Errorf("expected params.command %q, got %v", "show version", task.Params)
	}
}

// TestBuildFromYAMLFile_ImportTasksNestedChain proves an imported file can
// itself use import_tasks, resolved against the same base directory as
// the top-level runbook.
func TestBuildFromYAMLFile_ImportTasksNestedChain(t *testing.T) {
	dir := t.TempDir()
	writeRunbookFile(t, dir, "leaf.yaml", `
- name: leaf task
  fqcn: noop
`)
	writeRunbookFile(t, dir, "middle.yaml", `
- name: middle import
  fqcn: import_tasks
  params:
    file: leaf.yaml
`)
	top := writeRunbookFile(t, dir, "top.yaml", `
id: nested-chain
tasks:
  - name: top import
    fqcn: import_tasks
    params:
      file: middle.yaml
`)

	dag, err := newTestBuilder(t).BuildFromYAMLFile(top)
	if err != nil {
		t.Fatalf("BuildFromYAMLFile: %v", err)
	}

	foundLeaf := false
	for _, task := range dag.Nodes {
		if task.Name == "leaf task" {
			foundLeaf = true
		}
	}
	if !foundLeaf {
		t.Errorf("expected the nested import chain to reach leaf.yaml's task, nodes: %v", dag.Nodes)
	}
}

// TestBuildFromYAML_ImportTasksRequiresBaseDir proves BuildFromYAML (no
// file context) fails clearly on import_tasks rather than silently
// misresolving a relative path.
func TestBuildFromYAML_ImportTasksRequiresBaseDir(t *testing.T) {
	payload := []byte(`{
		"id": "no-basedir",
		"tasks": [{"name": "import", "fqcn": "import_tasks", "params": {"file": "common.yaml"}}]
	}`)

	_, err := newTestBuilder(t).BuildFromYAML(payload)
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if !strings.Contains(err.Error(), "no base directory") {
		t.Errorf("expected a no-base-directory error, got: %v", err)
	}
}

// TestBuildFromYAMLFile_ImportTasksRejectsEscape proves the path resolver
// fails closed against every escape shape it must reject, never reading
// the file even if one happens to exist at the resolved location.
func TestBuildFromYAMLFile_ImportTasksRejectsEscape(t *testing.T) {
	dir := t.TempDir()

	outsideDir := t.TempDir()
	writeRunbookFile(t, outsideDir, "secret.yaml", `
- name: SENTINEL_ESCAPED
  fqcn: noop
`)

	cases := map[string]string{
		"absolute path":    filepath.Join(outsideDir, "secret.yaml"),
		"parent traversal": "../" + filepath.Base(outsideDir) + "/secret.yaml",
		"empty path":       "",
	}

	for name, file := range cases {
		t.Run(name, func(t *testing.T) {
			runbook := writeRunbookFile(t, dir, "runbook_"+strings.ReplaceAll(name, " ", "_")+".yaml", `
id: escape-test
tasks:
  - name: import
    fqcn: import_tasks
    params:
      file: `+quoteYAMLString(file)+`
`)
			_, err := newTestBuilder(t).BuildFromYAMLFile(runbook)
			if err == nil {
				t.Fatalf("expected %s to be rejected, got no error", name)
			}
		})
	}
}

// TestBuildFromYAMLFile_ImportTasksRejectsCycle proves a file that
// transitively imports itself fails with a clear cycle error instead of
// recursing forever.
func TestBuildFromYAMLFile_ImportTasksRejectsCycle(t *testing.T) {
	dir := t.TempDir()
	writeRunbookFile(t, dir, "a.yaml", `
- name: from a
  fqcn: import_tasks
  params:
    file: b.yaml
`)
	writeRunbookFile(t, dir, "b.yaml", `
- name: from b
  fqcn: import_tasks
  params:
    file: a.yaml
`)
	top := writeRunbookFile(t, dir, "top.yaml", `
id: cycle-test
tasks:
  - name: start
    fqcn: import_tasks
    params:
      file: a.yaml
`)

	_, err := newTestBuilder(t).BuildFromYAMLFile(top)
	if err == nil {
		t.Fatal("expected a cycle error, got nil")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Errorf("expected a cycle error, got: %v", err)
	}
}

// quoteYAMLString renders s as a double-quoted YAML scalar so an
// adversarial test value (including an empty string or one containing
// slashes) never breaks the surrounding hand-written YAML document.
func quoteYAMLString(s string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + replacer.Replace(s) + `"`
}
