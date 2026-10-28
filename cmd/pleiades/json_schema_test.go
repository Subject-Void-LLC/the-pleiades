// Tests that each --json document a tool reads as a contract fits its
// published schema, through the built binary: the run report of adhoc and
// run, whatever way the run ends, and the doc filters and conditions topics.
// The schemas are the ones tools/gendocs publishes, built here from the same
// internal/clireport definitions.
package main_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/clireport"
	"github.com/Subject-Void-LLC/the-pleiades/internal/jsonschema"
)

// fitsSchema validates doc, a --json document's standard output, against s
// and returns the validator, so a caller can ask what doc left unfilled.
func fitsSchema(t *testing.T, s clireport.Schema, label string, doc []byte) *jsonschema.Validator {
	t.Helper()
	built, err := s.Build(repoRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(built)
	var schema map[string]any
	if err := json.Unmarshal(encoded, &schema); err != nil {
		t.Fatal(err)
	}
	var v any
	if err := json.Unmarshal(doc, &v); err != nil {
		t.Fatalf("%s: standard output is not JSON: %v\n%s", label, err, doc)
	}
	val := jsonschema.NewValidator(schema)
	for _, problem := range val.Check(label, v, schema) {
		t.Errorf("%s: %s", label, problem)
	}
	return val
}

// stdoutOf runs the binary in dir and returns its standard output alone,
// whatever its exit status.
func stdoutOf(t *testing.T, dir string, args ...string) []byte {
	t.Helper()
	cmd := exec.Command(binPath, args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if _, exited := err.(*exec.ExitError); !exited {
			t.Fatalf("%v: pleiades did not run: %v", args, err)
		}
	}
	return stdout.Bytes()
}

// TestDocJSON_FitsItsSchema: doc filters and doc conditions print their
// version and fit their schemas, every property filled.
func TestDocJSON_FitsItsSchema(t *testing.T) {
	dir := t.TempDir()
	for _, c := range []struct {
		topic   string
		schema  clireport.Schema
		version int
	}{
		{"filters", clireport.FiltersSchema, clireport.FiltersVersion},
		{"conditions", clireport.ConditionsSchema, clireport.ConditionsVersion},
	} {
		out := stdoutOf(t, dir, "doc", c.topic, "--json")
		val := fitsSchema(t, c.schema, "doc "+c.topic, out)
		for _, unfilled := range val.Unfilled() {
			t.Errorf("doc %s --json never gives %s a value", c.topic, unfilled)
		}
		var head struct {
			SchemaVersion int `json:"schema_version"`
		}
		if err := json.Unmarshal(out, &head); err != nil || head.SchemaVersion != c.version {
			t.Errorf("doc %s --json: schema_version %d (%v), want %d", c.topic, head.SchemaVersion, err, c.version)
		}
	}
}

// runReportContract is the run report's keys written out by hand, each with
// its JSON kind, for a run of one task that completes. It is anchored outside
// internal/clireport on purpose: the schema is reflected from the same
// structs that write the report, so a renamed field changes both together
// and the schema test still passes. A key renamed, dropped or retyped fails
// here. The path's last element "[]" stands for each item of a list.
var runReportContract = map[string]string{
	"schema_version":       "number",
	"runbook":              "string",
	"mode":                 "string",
	"nodes":                "number",
	"inventory_hosts":      "number",
	"service_effecting":    "bool",
	"blast_radius":         "object",
	"blast_radius.devices": "number",
	"plan":                 "array",
	"plan[].section":       "string",
	"plan[].tasks":         "array",
	"tasks":                "array",
	"tasks[].id":           "string",
	"tasks[].name":         "string",
	"tasks[].method":       "string",
	"tasks[].device":       "string",
	"tasks[].host":         "string",
	"tasks[].status":       "string",
	"tasks[].stats":        "object",
	"tasks[].started_at":   "string",
	"tasks[].finished_at":  "string",
	"run_id":               "string",
	"journal":              "string",
	"outcome":              "object",
	"outcome.status":       "string",
	"outcome.message":      "string",
	"outcome.exit_code":    "number",
}

// jsonKind names the JSON kind of a decoded value.
func jsonKind(v any) string {
	switch v.(type) {
	case string:
		return "string"
	case float64:
		return "number"
	case bool:
		return "bool"
	case map[string]any:
		return "object"
	case []any:
		return "array"
	case nil:
		return "null"
	}
	return "unknown"
}

// lookupPath returns every value path names in doc: a dotted path whose
// "x[]" element stands for each item of the list x.
func lookupPath(doc any, path string) []any {
	values := []any{doc}
	for _, part := range strings.Split(path, ".") {
		key, each := strings.CutSuffix(part, "[]")
		var next []any
		for _, v := range values {
			obj, ok := v.(map[string]any)
			if !ok {
				continue
			}
			found, present := obj[key]
			if !present {
				continue
			}
			if !each {
				next = append(next, found)
				continue
			}
			list, _ := found.([]any)
			next = append(next, list...)
		}
		values = next
	}
	return values
}

// TestCLI_RunReportFitsItsSchema_AndItsHandWrittenContract: pleiades run --json for a
// fixed runbook writes every key runReportContract lists, of its kind, with
// the values the run must give (times, ids and paths only checked for
// shape), so a field renamed in the model fails even though the reflected
// schema moves with it.
func TestCLI_RunReportFitsItsSchema_AndItsHandWrittenContract(t *testing.T) {
	dir := adhocProject(t)
	runbook := "# Says hello on web01. Blast radius: one device. Changes nothing. Undo: none needed.\nid: contract\nhosts: web01\ntasks:\n  - name: say hello\n    noop:\n      msg: hello\n"
	if err := os.MkdirAll(filepath.Join(dir, "runbooks"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "runbooks", "contract.yaml"), []byte(runbook), 0o600); err != nil {
		t.Fatal(err)
	}
	_, out, code := runPleiadesJSON(t, dir, "run", "runbooks/contract.yaml")
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	var doc any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	for path, kind := range runReportContract {
		values := lookupPath(doc, path)
		if len(values) == 0 {
			t.Errorf("the run report has no %s", path)
		}
		for _, v := range values {
			if got := jsonKind(v); got != kind {
				t.Errorf("%s is %s, want %s", path, got, kind)
			}
		}
	}
	want := map[string]any{
		"schema_version": float64(clireport.RunReportVersion), "runbook": "runbooks/contract.yaml", "mode": "execute",
		"tasks[].name": "say hello", "tasks[].method": "noop", "tasks[].host": "web01", "tasks[].status": "ok",
		"outcome.status": "complete", "outcome.exit_code": float64(0),
	}
	for path, value := range want {
		for _, got := range lookupPath(doc, path) {
			if got != value {
				t.Errorf("%s is %v, want %v", path, got, value)
			}
		}
	}
	for _, path := range []string{"tasks[].started_at", "tasks[].finished_at"} {
		for _, v := range lookupPath(doc, path) {
			if s, _ := v.(string); s == "" {
				t.Errorf("%s is empty", path)
			} else if _, err := time.Parse(time.RFC3339Nano, s); err != nil {
				t.Errorf("%s %q is not an RFC 3339 time: %v", path, s, err)
			}
		}
	}
	if stats := lookupPath(doc, "tasks[].stats.msg"); len(stats) != 1 || stats[0] != "hello" {
		t.Errorf("tasks[].stats.msg is %v, want the one task's hello", stats)
	}
}

// TestCLI_RunReportFitsItsSchema: the run report fits its schema and states
// its version, for a run that completes, a check, a refused call, a failed
// one and a runbook that does not load.
func TestCLI_RunReportFitsItsSchema(t *testing.T) {
	dir := adhocProject(t)
	for name, args := range map[string][]string{
		"complete":        {"adhoc", "web", "noop", "msg=hello"},
		"check":           {"adhoc", "web", "noop", "--mode", "check"},
		"invalid":         {"adhoc", "web", "exec.command", "cmd=uptime", "bogus=1"},
		"failed":          {"adhoc", "nosuch", "noop"},
		"missing runbook": {"run", "runbooks/missing.yaml"},
	} {
		rep, out, _ := runPleiadesJSON(t, dir, args...)
		fitsSchema(t, clireport.RunReportSchema, name, []byte(out))
		if rep.SchemaVersion != clireport.RunReportVersion {
			t.Errorf("%s: schema_version %d, want %d", name, rep.SchemaVersion, clireport.RunReportVersion)
		}
	}
}
