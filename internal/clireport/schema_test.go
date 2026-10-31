// Tests that each published schema describes its model: a document filling
// every field fits it, strictly, and the schema states the contract.
package clireport

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/jsonschema"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
	"github.com/Subject-Void-LLC/the-pleiades/internal/rollback"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// repoRoot is the repository root, where the model files are.
var repoRoot = filepath.Join("..", "..")

// fullRunReport gives every field a run report can carry a value.
func fullRunReport() RunReport {
	at := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	gap := rollback.Gap{Node: "tasks[0]", DeviceID: "id-1", DeviceName: "r1", Reason: "left by --leave"}
	group := []PlanTask{{ID: "tasks[1].block[0]", Name: "inner", Method: "noop", Selected: true}}
	return RunReport{
		Runbook: "runbooks/site.yaml", Mode: collection.ModeExecute, Nodes: 3, InventoryHosts: 2, ServiceEffecting: true,
		BlastRadius: BlastRadius{Devices: 2, Tiers: []string{"core"}},
		Selection:   "--tags web",
		Plan: []PlanSection{{Section: "tasks", Tasks: []PlanTask{
			{ID: "tasks[0]", Name: "one", Method: "noop", Selected: true},
			{ID: "tasks[1]", Name: "group", Kind: "block", Selected: true, Block: group, Rescue: group, Always: group, Parallel: group},
		}}},
		Validation: []ValidationIssue{{Rule: "TransportRule", Node: "tasks[0]", Device: "id-1", Host: "r1", Message: "no transport"}},
		Tasks: []TaskReport{{
			ID: "tasks[0]", Name: "one", Method: "noop", Device: "id-1", Host: "r1", Status: StatusFailed, Error: "boom",
			RescuedBy: "tasks[1].rescue[0]", Undoes: "tasks[0].undo[0]", Unreached: true, Label: "item one", Ignored: true,
			Retried: true, Reason: "why", AllowedUnchecked: true, Warnings: []string{"weak tls"},
			Provider: &collection.Provider{Program: "/c/prog", Digest: "sha256:ab"}, Stats: map[string]any{"rc": 1},
			StartedAt: &at, FinishedAt: &at,
		}},
		Metadata: map[string]any{"ticket": "CHG-1"},
		RunID:    "run-1", Journal: ".pleiades/journal/run-1.jsonl",
		Warnings: []RunWarning{{Kind: WarnConcurrentRun, When: "start", Message: "another run", RunID: "run-0", Runbook: "x.yaml", PID: 42, Devices: []string{"r1"}}},
		Rollback: &RollbackReport{
			Of: "run-0",
			Levels: [][]rollback.Step{{{Node: "tasks[0]", DeviceID: "id-1", DeviceName: "r1", Index: 0, Emitter: "file.copy",
				FQCN: "file.remove", Params: map[string]any{"path": "/x"}, Name: "undo", Source: "recorded", Partial: true, Become: "root"}}},
			Left: []rollback.Gap{gap}, Unknown: []rollback.Gap{gap}, UnacceptedUnknown: 1, AlreadyUndone: []rollback.Gap{gap},
			NothingToUndo: []rollback.Gap{gap},
			Problems:      []rollback.Problem{{Node: "tasks[0]", DeviceID: "id-1", DeviceName: "r1", Reason: "no undo", Kind: "later_run", Accept: "--despite-run run-2"}},
			Provisional:   []ProvisionalStep{{Name: "undo", Device: "r1", Reason: "an earlier step changes it"}},
		},
		Outcome: RunOutcome{Status: "incomplete", Message: "check incomplete", ExitCode: 3, Unchecked: 1, AllowedUnchecked: []string{"tasks[3] [r1]"}},
	}
}

// fitsStrictly validates v, encoded as the command encodes it, against s,
// and fails on every property v leaves without a value when full is set.
func fitsStrictly(t *testing.T, s Schema, v any, full bool) {
	t.Helper()
	built, err := s.Build(repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(built)
	var schema map[string]any
	if err := json.Unmarshal(encoded, &schema); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var doc any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	val := jsonschema.NewValidator(schema)
	for _, problem := range val.Check(s.File, doc, schema) {
		t.Error(problem)
	}
	if full {
		for _, unfilled := range val.Unfilled() {
			t.Errorf("the fixture never gives %s a value, so the schema's claim about it is untested", unfilled)
		}
	}
}

func TestRunReportSchema_FitsAFullReport(t *testing.T) {
	fitsStrictly(t, RunReportSchema, fullRunReport(), true)
}

func TestFiltersSchema_FitsTheTopic(t *testing.T) {
	topic := FiltersTopic{SchemaVersion: FiltersVersion, Roots: []DocRoot{{Name: "vars", Holds: "the run's variables"}},
		Filters: render.Filters(), Excluded: render.ExcludedFilters(), Statements: render.Statements(),
		Operators: render.Operators(), Tests: render.Tests(), NotSupported: render.NotSupported()}
	fitsStrictly(t, FiltersSchema, topic, true)
}

func TestConditionsSchema_FitsTheTopic(t *testing.T) {
	libs, err := engine.ConditionLibraries()
	if err != nil {
		t.Fatal(err)
	}
	roots := []DocRoot{{Name: "vars", Holds: "the run's variables"}}
	fitsStrictly(t, ConditionsSchema, ConditionsTopic{SchemaVersion: ConditionsVersion, Forms: roots, Roots: roots, Libraries: libs}, true)
}

// TestRunReport_MarshalStampsTheVersion: a report built anywhere prints its
// version, and an encoder that keeps HTML characters (as the command's
// does) still keeps them inside it.
func TestRunReport_MarshalStampsTheVersion(t *testing.T) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(RunReport{SchemaVersion: 7, Runbook: "a>=b & c<d"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `"schema_version":1,`) {
		t.Errorf("not stamped with RunReportVersion: %s", buf.String())
	}
	if !strings.Contains(buf.String(), `a>=b & c<d`) {
		t.Errorf("HTML characters were escaped: %s", buf.String())
	}
}

// TestSchemas_StateTheContract: every schema says how it is versioned, is
// open to fields a newer producer adds, and publishes known values rather
// than closing them in an enum.
func TestSchemas_StateTheContract(t *testing.T) {
	var files []string
	for _, s := range Schemas {
		built, err := s.Build(repoRoot)
		if err != nil {
			t.Fatalf("%s: %v", s.File, err)
		}
		if !strings.Contains(built["description"].(string), Contract) || !strings.Contains(built["description"].(string), s.Command) {
			t.Errorf("%s does not state the contract and its command", s.File)
		}
		props := built["properties"].(map[string]any)
		if _, ok := props["schema_version"]; !ok || !slices.Contains(built["required"].([]string), "schema_version") {
			t.Errorf("%s does not require schema_version", s.File)
		}
		encoded, _ := json.Marshal(built)
		for _, closed := range []string{`"additionalProperties":false`, `"enum"`} {
			if strings.Contains(string(encoded), closed) {
				t.Errorf("%s closes a set the contract leaves open: %s", s.File, closed)
			}
		}
		if _, err := s.Build(t.TempDir()); err == nil {
			t.Errorf("%s was built with none of its model files", s.File)
		}
		files = append(files, s.File)
	}
	if len(slices.Compact(slices.Sorted(slices.Values(files)))) != len(Schemas) {
		t.Errorf("two schemas share a file: %v", files)
	}
}
