// Tests that the published schema describes the model: a report filling
// every field fits it, strictly, and the schema states its contract and
// leaves its sets open.
package factsreport

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/jsonschema"
)

// fullReport gives every field a report can carry a value.
func fullReport() Report {
	at := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
	index := 2
	failure := &RowFailure{Row: 41, Position: 3, ChainIndex: &index, Kind: FaultChainHash, Stable: true, KeyID: "controller:00112233aabbccdd",
		SealedVersion: "v1", ObservedAt: at, Observation: 2, Fact: "os.kernel"}
	return Report{
		SchemaVersion: Version, Keys: Keys{Current: "controller:00112233aabbccdd", Previous: "controller:99887766ffeeddcc"},
		Scope: ScopeFleet, Scanned: 4, NeedsRepair: 1, Advice: "every device is under another facts key",
		Devices: []Device{{
			Device: "web1", ID: "0f8fad5b-d9cb-469f-a165-70867728950e", State: StateRefused,
			Rows: Rows{Total: 7, Stable: 6, Volatile: 1, Unopened: 1}, Coverages: 3, Observations: 4,
			KeyedBy:         []KeyedRows{{KeyID: "controller:99887766ffeeddcc", Role: RolePrevious, Rows: 7}},
			KeyedByMore:     1,
			SealedUnder:     []SealedRows{{Version: "v1", Role: RoleCurrent, Rows: 6}},
			SealedUnderMore: 1,
			Under:           []KeyCheck{{Role: RolePrevious, KeyID: "controller:99887766ffeeddcc", Verifies: false, Checked: 3, Opened: 7, Failed: 0, Failure: failure}},
			Unverifiable:    true,
			TruncateFrom:    2, Repairs: []string{RepairTruncate, RepairDiscard}, Advice: "truncate or discard",
		}},
	}
}

// built is the schema as a reader decodes it from its JSON.
func built(t *testing.T) map[string]any {
	t.Helper()
	schema, err := Schema(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(schema)
	var out map[string]any
	if err := json.Unmarshal(encoded, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestSchema_FitsAFullReport(t *testing.T) {
	schema := built(t)
	data, _ := json.Marshal(fullReport())
	var doc any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	val := jsonschema.NewValidator(schema)
	for _, problem := range val.Check(SchemaFile, doc, schema) {
		t.Error(problem)
	}
	for _, unfilled := range val.Unfilled() {
		t.Errorf("the fixture never gives %s a value, so the schema's claim about it is untested", unfilled)
	}
}

func TestSchema_StatesTheContract(t *testing.T) {
	schema := built(t)
	if d, _ := schema["description"].(string); !strings.Contains(d, Contract) || !strings.Contains(d, "controller facts inspect --json") {
		t.Errorf("the schema does not state its contract and command: %q", d)
	}
	required, _ := schema["required"].([]any)
	if !slices.Contains(required, any("schema_version")) {
		t.Error("the schema does not require schema_version")
	}
	encoded, _ := json.Marshal(schema)
	for _, closed := range []string{`"additionalProperties":false`, `"enum"`} {
		if strings.Contains(string(encoded), closed) {
			t.Errorf("the schema closes a set the contract leaves open: %s", closed)
		}
	}
	if _, err := Schema(t.TempDir()); err == nil {
		t.Error("the schema was built with none of its model files")
	}
}

// TestState_NeedsRepair: the states a controller with the same keys serves
// need none; every other, and one a newer release names, does.
func TestState_NeedsRepair(t *testing.T) {
	for _, s := range KnownStates {
		want := s != string(StateEmpty) && s != string(StateHealthy) && s != string(StateRekeyPending)
		if State(s).NeedsRepair() != want {
			t.Errorf("%s needs repair %v, want %v", s, !want, want)
		}
	}
	if !State("from_a_newer_release").NeedsRepair() {
		t.Error("an unknown state reads as needing no repair")
	}
}
