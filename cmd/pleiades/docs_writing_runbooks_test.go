// Gate for docs/04-writing-runbooks.md: every runbook the page shows, each a
// fenced block opening with the info string "yaml", is written into a project
// beside an inventory that has group_vars and host_vars, and must pass the
// real binary's validate with no issue and no warning. A snippet that is not a
// whole runbook is fenced as "text", which this test leaves alone.
package main_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// docsInventory is the project the page's examples run against.
const docsInventory = `hosts:
  - id: web1
    name: web1
    type: linux_server
    tags: [web]
    properties:
      host: 127.0.0.1
      port: 2201
  - id: web2
    name: web2
    type: linux_server
    tags: [web]
    properties:
      host: 127.0.0.1
      port: 2202
`

// yamlFence matches one fenced block whose info string is exactly "yaml".
var yamlFence = regexp.MustCompile("(?s)\n```yaml\n(.*?)\n```\n")

// TestCLI_WritingRunbooksPageExamplesValidate is the gate.
func TestCLI_WritingRunbooksPageExamplesValidate(t *testing.T) {
	page, err := os.ReadFile(filepath.Join("..", "..", "docs", "04-writing-runbooks.md"))
	if err != nil {
		t.Fatal(err)
	}
	fences := yamlFence.FindAllStringSubmatch(string(page), -1)
	if len(fences) < 8 {
		t.Fatalf("the page shows %d runbooks, want at least 8", len(fences))
	}
	dir := t.TempDir()
	files := map[string]string{
		"inventory.yaml":     docsInventory,
		"group_vars/web.yml": "region: east\nport: 8080\n",
		"host_vars/web2.yml": "port: 9090\n",
		"templates/app.j2":   "env={{ vars.env }}\n",
		"runbooks/.gitkeep":  "",
	}
	for name, body := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for i, f := range fences {
		rel := filepath.Join("runbooks", "example"+string(rune('a'+i))+".yaml")
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(f[1]+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := runPleiades(t, dir, "validate", rel)
		if err != nil || !strings.Contains(out, "no issues found") || strings.Contains(out, "[warning]") {
			t.Errorf("the page's example %d does not validate cleanly: %v\n%s\n%s", i+1, err, out, f[1])
		}
	}
}
