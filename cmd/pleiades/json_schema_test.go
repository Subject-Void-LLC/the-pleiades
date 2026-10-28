// Tests that each --json document a tool reads as a contract fits its
// published schema, through the built binary: the run report of adhoc and
// run, whatever way the run ends, and the doc filters and conditions topics.
// The schemas are the ones tools/gendocs publishes, built here from the same
// internal/clireport definitions.
package main_test

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"testing"

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
