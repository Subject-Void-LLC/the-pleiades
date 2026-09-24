//go:build integration

// Differential test: the native tag selection against a real
// ansible-playbook's, for the same tree and the same filters.
package engine_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
)

// tagDifferentialPlaybook and tagDifferentialRunbook are one task tree
// written twice: tags on the play, on a block (inherited by its tasks),
// never, always, untagged, and a nested block.
const tagDifferentialPlaybook = `- hosts: localhost
  gather_facts: false
  tags: [site]
  tasks:
    - name: plain
      ansible.builtin.debug: {msg: x}
    - name: web block
      tags: web
      block:
        - name: web one
          ansible.builtin.debug: {msg: x}
        - name: web two
          ansible.builtin.debug: {msg: x}
          tags: slow
        - name: inner
          tags: [deep]
          block:
            - name: deep one
              ansible.builtin.debug: {msg: x}
      always:
        - name: web cleanup
          ansible.builtin.debug: {msg: x}
    - name: wipe
      ansible.builtin.debug: {msg: x}
      tags: [never, wipe]
    - name: audit
      ansible.builtin.debug: {msg: x}
      tags: always
    - name: db one
      ansible.builtin.debug: {msg: x}
      tags: db
    - name: bare
      ansible.builtin.debug: {msg: x}
`

const tagDifferentialRunbook = `id: tagdiff
tags: [site]
tasks:
  - name: plain
    fqcn: noop
  - name: web block
    tags: web
    block:
      - name: web one
        fqcn: noop
      - name: web two
        fqcn: noop
        tags: slow
      - name: inner
        tags: [deep]
        block:
          - name: deep one
            fqcn: noop
    always:
      - name: web cleanup
        fqcn: noop
  - name: wipe
    fqcn: noop
    tags: [never, wipe]
  - name: audit
    fqcn: noop
    tags: always
  - name: db one
    fqcn: noop
    tags: db
  - name: bare
    fqcn: noop
`

// TestTagFilter_MatchesAnsible runs ansible-playbook --list-tasks (the
// pinned ansible-core in the repository's own runner image) once per
// filter and requires Select to keep exactly the tasks Ansible lists, in
// the same order. --list-tasks omits rescue and always tasks, and the
// engine does not run them yet, so both sides compare the happy path.
func TestTagFilter_MatchesAnsible(t *testing.T) {
	image := testsupport.BuildAnsibleRunnerImage(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pb.yml"), []byte(tagDifferentialPlaybook), 0o644); err != nil { // #nosec G306 -- a test fixture read by a container
		t.Fatal(err)
	}
	filters := []engine.TagFilter{
		{},
		{Tags: []string{"web"}},
		{Tags: []string{"web"}, SkipTags: []string{"slow"}},
		{Tags: []string{"deep"}},
		{Tags: []string{"wipe"}},
		{Tags: []string{"never"}},
		{Tags: []string{"db", "wipe"}},
		{Tags: []string{"tagged"}},
		{Tags: []string{"untagged"}},
		{Tags: []string{"all"}},
		{SkipTags: []string{"web"}},
		{SkipTags: []string{"always"}},
		{SkipTags: []string{"all"}},
		{SkipTags: []string{"tagged"}},
		{Tags: []string{"site"}, SkipTags: []string{"untagged"}},
	}
	var script strings.Builder
	for _, f := range filters {
		script.WriteString("echo '=== filter'; ansible-playbook -i localhost, --list-tasks")
		if len(f.Tags) > 0 {
			script.WriteString(" --tags " + strings.Join(f.Tags, ","))
		}
		if len(f.SkipTags) > 0 {
			script.WriteString(" --skip-tags " + strings.Join(f.SkipTags, ","))
		}
		script.WriteString(" /work/pb.yml;\n")
	}
	// #nosec G204 -- the image is this repository's own test image and the script is built from constants above
	out, err := exec.Command("docker", "run", "--rm", "-v", dir+":/work:ro", image, "sh", "-c", script.String()).CombinedOutput()
	if err != nil {
		t.Fatalf("ansible-playbook: %v\n%s", err, out)
	}
	listings := parseListTasks(t, string(out))
	if len(listings) != len(filters) {
		t.Fatalf("got %d listings for %d filters:\n%s", len(listings), len(filters), out)
	}

	built, err := buildYAML(t, tagDifferentialRunbook)
	if err != nil {
		t.Fatal(err)
	}
	for i, f := range filters {
		dag, err := engine.Select(built, f)
		if err != nil {
			t.Errorf("filter %+v: %v", f, err)
			continue
		}
		if got := happyPathNames(t, dag); !slices.Equal(got, listings[i]) {
			t.Errorf("filter %+v: native %v, Ansible %v", f, got, listings[i])
		}
	}
}

// parseListTasks splits --list-tasks output at the "=== filter" markers
// and returns each run's task names, in order.
func parseListTasks(t *testing.T, out string) [][]string {
	t.Helper()
	var listings [][]string
	inTasks := false
	for _, line := range strings.Split(out, "\n") {
		switch {
		case line == "=== filter":
			listings = append(listings, []string{})
			inTasks = false
		case strings.TrimSpace(line) == "tasks:":
			inTasks = true
		case inTasks && strings.HasPrefix(line, "      ") && strings.Contains(line, "\tTAGS:"):
			name, _, _ := strings.Cut(strings.TrimSpace(line), "\tTAGS:")
			listings[len(listings)-1] = append(listings[len(listings)-1], name)
		}
	}
	return listings
}

// happyPathNames returns the names of the leaf tasks dag runs, in the
// order it runs them.
func happyPathNames(t *testing.T, dag *engine.DAG) []string {
	t.Helper()
	order, err := engine.TopologicalOrder(dag)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, id := range order {
		if task := dag.Nodes[id]; task != nil && task.Kind() == engine.TaskKindLeaf {
			names = append(names, task.Name)
		}
	}
	return names
}
