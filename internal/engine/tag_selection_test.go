// Tests for TagSelection: a tag filter checked against several runbooks
// together, as pleiades validate runbooks/* applies one.
package engine_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
)

// TestTagSelection_ChecksNamesAcrossRunbooks covers the rule a filter
// over several runbooks follows: a name one of them carries is accepted
// for all of them, and selects nothing but always tasks from the rest,
// while a name none carries is still refused as a typo.
func TestTagSelection_ChecksNamesAcrossRunbooks(t *testing.T) {
	web, err := buildYAML(t, "id: web\ntasks:\n  - name: deploy\n    fqcn: noop\n    tags: web\n  - name: plain\n    fqcn: noop\n")
	if err != nil {
		t.Fatal(err)
	}
	db, err := buildYAML(t, "id: db\ntasks:\n  - name: migrate\n    fqcn: noop\n  - name: audit\n    fqcn: noop\n    tags: always\n")
	if err != nil {
		t.Fatal(err)
	}

	sel, err := engine.NewTagSelection(engine.TagFilter{Tags: []string{"web"}}, web, db)
	if err != nil {
		t.Fatalf("a tag one runbook carries was refused for both: %v", err)
	}
	for _, tc := range []struct {
		dag  *engine.DAG
		want []string
	}{
		{web, []string{"deploy"}},
		{db, []string{"audit"}},
	} {
		got, err := sel.Select(tc.dag)
		if err != nil {
			t.Fatalf("Select(%s): %v", tc.dag.ID, err)
		}
		if !slices.Equal(selected(got), tc.want) {
			t.Errorf("Select(%s) = %v, want %v", tc.dag.ID, selected(got), tc.want)
		}
	}

	// Alone, the runbook without the tag still refuses it: the wider
	// check applies only to runbooks named together.
	if _, err := engine.Select(db, engine.TagFilter{Tags: []string{"web"}}); err == nil {
		t.Error("Select accepted a tag the one runbook it was given does not carry")
	}

	for _, f := range []engine.TagFilter{{Tags: []string{"wbe"}}, {SkipTags: []string{"wbe"}}} {
		_, err := engine.NewTagSelection(f, web, db)
		if err == nil || !strings.Contains(err.Error(), "which no task carries") || !strings.Contains(err.Error(), "the tags these runbooks carry are always, web") {
			t.Errorf("NewTagSelection(%+v) = %v, want a refusal listing both runbooks' tags", f, err)
		}
	}
}

// TestTagSelection_RefusesAnUncheckedRunbook proves a selection projects
// only the runbooks its names were checked against. Anything else is
// where a misspelled --skip-tags would skip nothing unnoticed, so the
// zero value, which was checked against nothing, selects nothing.
func TestTagSelection_RefusesAnUncheckedRunbook(t *testing.T) {
	checked, err := buildYAML(t, "id: a\ntasks:\n  - name: x\n    fqcn: noop\n    tags: web\n")
	if err != nil {
		t.Fatal(err)
	}
	other, err := buildYAML(t, "id: b\ntasks:\n  - name: y\n    fqcn: noop\n")
	if err != nil {
		t.Fatal(err)
	}
	sel, err := engine.NewTagSelection(engine.TagFilter{SkipTags: []string{"web"}}, checked)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sel.Select(other); err == nil || !strings.Contains(err.Error(), "not one of the runbooks") {
		t.Errorf("Select(unchecked) = %v, want a refusal", err)
	}
	if _, err := (engine.TagSelection{}).Select(checked); err == nil {
		t.Error("the zero TagSelection selected from a runbook")
	}
	if _, err := engine.NewTagSelection(engine.TagFilter{}, checked, &engine.DAG{ID: "h"}); err == nil {
		t.Error("NewTagSelection accepted a DAG the builder never made")
	}
}
