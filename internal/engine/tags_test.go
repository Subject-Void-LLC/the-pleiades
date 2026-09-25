// Tests for the tags key, TagFilter.Runs (Ansible's selection rule) and
// Select.
package engine_test

import (
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
)

// TestTagList_Unmarshal covers the shapes the tags key accepts and the
// names it refuses, in YAML and JSON.
func TestTagList_Unmarshal(t *testing.T) {
	for _, tc := range []struct {
		name, yaml string
		want       []string
		err        string
	}{
		{"list", "[web, db]", []string{"web", "db"}, ""},
		{"comma string", `"web, db ,web"`, []string{"web", "db"}, ""},
		{"number", "[5, web]", []string{"5", "web"}, ""},
		{"never and always are task tags", "[never, always]", []string{"never", "always"}, ""},
		{"empty", `[web, ""]`, nil, "may not be empty"},
		{"template", `["{{ role }}"]`, nil, "is a template"},
		{"control character", "[\"web\\x1b[2J\"]", nil, "control or text-direction"},
		{"filter-only all", "[all]", nil, "only has meaning in --tags"},
		{"filter-only untagged", "untagged", nil, "only has meaning in --tags"},
		{"map", "{a: b}", nil, "must be a string or a list"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dag, err := buildYAML(t, "id: t\ntasks:\n  - name: a\n    fqcn: noop\n    tags: "+tc.yaml+"\n")
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("error = %v, want one containing %q", err, tc.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := []string(dag.Nodes["tasks[0]"].Tags); !slices.Equal(got, tc.want) {
				t.Errorf("tags = %v, want %v", got, tc.want)
			}
		})
	}
	dag, err := buildJSON(t, `{"id":"j","tasks":[{"name":"a","fqcn":"noop","tags":["x",7]}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if got := []string(dag.Nodes["tasks[0]"].Tags); !slices.Equal(got, []string{"x", "7"}) {
		t.Errorf("JSON tags = %v, want [x 7]", got)
	}
}

// TestTagFilter_Runs is Ansible's Taggable.evaluate_tags as a table, each
// row one of its branches.
func TestTagFilter_Runs(t *testing.T) {
	for _, tc := range []struct {
		name       string
		only, skip []string
		tags       []string
		want       bool
	}{
		{"default runs an untagged task", nil, nil, nil, true},
		{"default runs a tagged task", nil, nil, []string{"web"}, true},
		{"default leaves out never", nil, nil, []string{"never"}, false},
		{"default leaves out never beside another tag", nil, nil, []string{"never", "wipe"}, false},
		{"naming a never task's other tag runs it", []string{"wipe"}, nil, []string{"never", "wipe"}, true},
		{"naming never runs it", []string{"never"}, nil, []string{"never"}, true},
		{"always runs whatever --tags says", []string{"web"}, nil, []string{"always"}, true},
		{"a match runs", []string{"web"}, nil, []string{"web", "db"}, true},
		{"no match does not run", []string{"web"}, nil, []string{"db"}, false},
		{"untagged selects an untagged task", []string{"untagged"}, nil, nil, true},
		{"untagged leaves out a tagged task", []string{"untagged"}, nil, []string{"web"}, false},
		{"tagged selects a tagged task", []string{"tagged"}, nil, []string{"web"}, true},
		{"tagged leaves out an untagged task", []string{"tagged"}, nil, nil, false},
		{"tagged leaves out never", []string{"tagged"}, nil, []string{"never", "wipe"}, false},
		{"skip wins over a match", []string{"web"}, []string{"web"}, []string{"web"}, false},
		{"skip of another tag keeps it", nil, []string{"db"}, []string{"web"}, true},
		{"skip untagged", nil, []string{"untagged"}, nil, false},
		{"skip tagged", nil, []string{"tagged"}, []string{"web"}, false},
		{"skip all keeps always", nil, []string{"all"}, []string{"always"}, true},
		{"skip all leaves out the rest", nil, []string{"all"}, []string{"web"}, false},
		{"skip all and always leaves out always", nil, []string{"all", "always"}, []string{"always"}, false},
		{"skip always leaves out always", nil, []string{"always"}, []string{"always"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := engine.TagFilter{Tags: tc.only, SkipTags: tc.skip}
			if got := f.Runs(tc.tags); got != tc.want {
				t.Errorf("Runs(%v) with --tags %v --skip-tags %v = %v, want %v", tc.tags, tc.only, tc.skip, got, tc.want)
			}
		})
	}
}

// tagRunbook is the runbook the Select tests project: tags on the play,
// on a block (inherited by its children and its always task), on single
// tasks, never and always, and a parallel group.
const tagRunbook = `id: tags
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
    always:
      - name: web cleanup
        fqcn: noop
  - name: wipe
    fqcn: noop
    tags: [never, wipe]
  - name: audit
    fqcn: noop
    tags: always
  - name: fan
    parallel:
      - name: db one
        fqcn: noop
        tags: db
      - name: web three
        fqcn: noop
        tags: web
`

// selected returns the names of dag's leaf tasks, sorted.
func selected(dag *engine.DAG) []string {
	var names []string
	for _, task := range dag.Nodes {
		if task.Kind() == engine.TaskKindLeaf {
			names = append(names, task.Name)
		}
	}
	slices.Sort(names)
	return names
}

// TestSelect covers projection through the builder's default and through
// explicit filters: which tasks remain, that their IDs are the full
// runbook's, that the chain stays connected, and that a filter naming a
// tag nobody carries is refused.
func TestSelect(t *testing.T) {
	built, err := buildYAML(t, tagRunbook)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"audit", "db one", "plain", "web cleanup", "web one", "web three", "web two"}; !slices.Equal(selected(built), want) {
		t.Errorf("default selection = %v, want %v (never left out)", selected(built), want)
	}
	if built.Nodes["tasks[2]"] != nil {
		t.Error("the never task is still in the default DAG")
	}

	for _, tc := range []struct {
		name string
		f    engine.TagFilter
		want []string
	}{
		{"web", engine.TagFilter{Tags: []string{"web"}}, []string{"audit", "web cleanup", "web one", "web three", "web two"}},
		{"web without slow", engine.TagFilter{Tags: []string{"web"}, SkipTags: []string{"slow"}}, []string{"audit", "web cleanup", "web one", "web three"}},
		{"wipe", engine.TagFilter{Tags: []string{"wipe"}}, []string{"audit", "wipe"}},
		{"db", engine.TagFilter{Tags: []string{"db"}}, []string{"audit", "db one"}},
		{"skip always", engine.TagFilter{Tags: []string{"db"}, SkipTags: []string{"always"}}, []string{"db one"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := engine.Select(built, tc.f)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(selected(got), tc.want) {
				t.Errorf("selection = %v, want %v", selected(got), tc.want)
			}
			if got.Version != built.Version || got.ID != built.ID {
				t.Error("Select changed the runbook's identity")
			}
			if len(got.Nodes) > 0 && got.EntryPoint == "" {
				t.Error("a non-empty selection has no entry point")
			}
		})
	}

	web, err := engine.Select(built, engine.TagFilter{Tags: []string{"web"}})
	if err != nil {
		t.Fatal(err)
	}
	if web.Nodes["tasks[1].block[1]"] == nil || web.Nodes["tasks[4].parallel[1]"] == nil {
		t.Errorf("selected tasks lost their full-runbook IDs: %v", web.Nodes)
	}
	if web.Nodes["tasks[4].fanout"] == nil || len(web.Adjacency["tasks[4].fanout"]) != 1 {
		t.Errorf("the parallel group with one kept child is not wired to it: %v", web.Adjacency["tasks[4].fanout"])
	}

	for _, f := range []engine.TagFilter{{Tags: []string{"wbe"}}, {SkipTags: []string{"slwo"}}} {
		if _, err := engine.Select(built, f); err == nil || !strings.Contains(err.Error(), "which no task carries") {
			t.Errorf("filter %+v = %v, want a refusal naming the unknown tag", f, err)
		}
	}
}

// TestSelect_RefusesDanglingRegisterRead covers the one selection Select
// refuses on content: a kept task reading a result only a left-out task
// registers.
func TestSelect_RefusesDanglingRegisterRead(t *testing.T) {
	built, err := buildYAML(t, "id: r\ntasks:\n  - name: probe\n    fqcn: noop\n    register: probe\n    tags: check\n  - name: act\n    fqcn: noop\n    tags: act\n    when_cel: stat.probe['x'].ok == true\n")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Select(built, engine.TagFilter{Tags: []string{"act"}}); err == nil || !strings.Contains(err.Error(), `reads the result registered as "probe"`) {
		t.Errorf("Select = %v, want a refusal naming probe", err)
	}
	if _, err := engine.Select(built, engine.TagFilter{Tags: []string{"act", "check"}}); err != nil {
		t.Errorf("keeping the producer too was refused: %v", err)
	}
}

// TestSelect_UntaggedRunbookIsUnchanged proves the builder's default
// projection costs nothing for a runbook with no tags: it is the DAG the
// walk built, and an explicit zero filter selects everything.
func TestSelect_UntaggedRunbookIsUnchanged(t *testing.T) {
	built, err := buildYAML(t, "id: u\ntasks:\n  - name: a\n    fqcn: noop\n  - name: g\n    block:\n      - name: b\n        fqcn: noop\n")
	if err != nil {
		t.Fatal(err)
	}
	again, err := engine.Select(built, engine.TagFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Nodes) != len(built.Nodes) || again.EntryPoint != built.EntryPoint {
		t.Errorf("zero filter changed an untagged DAG: %d nodes vs %d", len(again.Nodes), len(built.Nodes))
	}
	for id, edges := range built.Adjacency {
		if !slices.Equal(edges, again.Adjacency[id]) {
			t.Errorf("edge %s: %v vs %v", id, edges, again.Adjacency[id])
		}
	}
}

// TestSelect_DoesNotMutateSharedDAG runs many selections of one built DAG
// at once, as the Runner's cached DAGs will be, and checks the shared DAG
// afterwards. Run under -race it also proves the reads are safe.
func TestSelect_DoesNotMutateSharedDAG(t *testing.T) {
	built, err := buildYAML(t, tagRunbook)
	if err != nil {
		t.Fatal(err)
	}
	before := selected(built)
	edges := len(built.Adjacency)
	var wg sync.WaitGroup
	for _, tag := range []string{"web", "db", "wipe", "slow", "site"} {
		for range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if _, err := engine.Select(built, engine.TagFilter{Tags: []string{tag}}); err != nil {
					t.Error(err)
				}
			}()
		}
	}
	wg.Wait()
	if !slices.Equal(selected(built), before) || len(built.Adjacency) != edges {
		t.Error("concurrent selections changed the shared DAG")
	}
}

// TestSelect_HandBuiltDAGRefused proves Select needs a builder DAG rather
// than silently projecting a hand-built one onto nothing.
func TestSelect_HandBuiltDAGRefused(t *testing.T) {
	if _, err := engine.Select(&engine.DAG{ID: "h"}, engine.TagFilter{}); err == nil {
		t.Error("Select accepted a DAG the builder never made")
	}
}
