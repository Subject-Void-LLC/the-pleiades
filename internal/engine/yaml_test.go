package engine_test

import (
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"go.yaml.in/yaml/v3"
)

// TestBuildFromYAML_MatchesJSON is the bidirectional-compilation proof:
// a hand-written YAML runbook and the JSON-built equivalent of the same
// workflow must produce the identical *DAG shape, since both decode into
// the same WorkflowDef and share one compilation path.
func TestBuildFromYAML_MatchesJSON(t *testing.T) {
	eval, _ := engine.NewCELEvaluator()
	builder := engine.NewBuilder(eval)

	yamlPayload := []byte(`
id: runbook-1
tasks:
  - name: start
    fqcn: noop
  - name: ping
    fqcn: ssh_exec
  - name: reboot
    fqcn: ios_backup
    when: "stat.ping_ms > 100"
`)

	jsonPayload := []byte(`{
		"id": "runbook-1",
		"tasks": [
			{"name": "start", "fqcn": "noop"},
			{"name": "ping", "fqcn": "ssh_exec"},
			{"name": "reboot", "fqcn": "ios_backup", "when": "stat.ping_ms > 100"}
		]
	}`)

	yamlDAG, err := builder.BuildFromYAML(yamlPayload)
	if err != nil {
		t.Fatalf("failed to build DAG from YAML: %v", err)
	}
	jsonDAG, err := builder.Build(jsonPayload)
	if err != nil {
		t.Fatalf("failed to build DAG from JSON: %v", err)
	}

	if yamlDAG.ID != jsonDAG.ID {
		t.Errorf("ID mismatch: yaml=%q json=%q", yamlDAG.ID, jsonDAG.ID)
	}
	if len(yamlDAG.Nodes) != len(jsonDAG.Nodes) {
		t.Fatalf("node count mismatch: yaml=%d json=%d", len(yamlDAG.Nodes), len(jsonDAG.Nodes))
	}
	for id, jsonNode := range jsonDAG.Nodes {
		yamlNode, ok := yamlDAG.Nodes[id]
		if !ok {
			t.Fatalf("YAML DAG missing node %q present in JSON DAG", id)
		}
		if yamlNode.FQCN != jsonNode.FQCN {
			t.Errorf("node %q fqcn mismatch: yaml=%q json=%q", id, yamlNode.FQCN, jsonNode.FQCN)
		}
	}
	if len(yamlDAG.Adjacency["tasks[1]"]) != len(jsonDAG.Adjacency["tasks[1]"]) {
		t.Fatalf("adjacency mismatch for tasks[1]: yaml=%d json=%d", len(yamlDAG.Adjacency["tasks[1]"]), len(jsonDAG.Adjacency["tasks[1]"]))
	}
	if yamlDAG.Conditions["tasks[2]"] == nil {
		t.Errorf("expected condition to be compiled for tasks[2] from YAML")
	}
}

// TestBuildFromYAML_RoundTrip proves YAML -> WorkflowDef -> YAML is
// lossless at the data level: re-marshaling a parsed runbook and
// re-parsing it produces the same DAG, task for task.
func TestBuildFromYAML_RoundTrip(t *testing.T) {
	eval, _ := engine.NewCELEvaluator()
	builder := engine.NewBuilder(eval)

	original := []byte(`
id: roundtrip-1
tasks:
  - name: start
    fqcn: noop
    params:
      timeout: 30
  - name: ping
    fqcn: ssh_exec
    when: "stat.ok == true"
`)

	var def engine.WorkflowDef
	if err := yaml.Unmarshal(original, &def); err != nil {
		t.Fatalf("failed to unmarshal original YAML: %v", err)
	}

	remarshaled, err := yaml.Marshal(def)
	if err != nil {
		t.Fatalf("failed to re-marshal WorkflowDef: %v", err)
	}

	firstDAG, err := builder.BuildFromYAML(original)
	if err != nil {
		t.Fatalf("failed to build DAG from original YAML: %v", err)
	}
	secondDAG, err := builder.BuildFromYAML(remarshaled)
	if err != nil {
		t.Fatalf("failed to build DAG from re-marshaled YAML: %v", err)
	}

	if len(firstDAG.Nodes) != len(secondDAG.Nodes) {
		t.Fatalf("round-trip changed node count: %d -> %d", len(firstDAG.Nodes), len(secondDAG.Nodes))
	}
	for id, node := range firstDAG.Nodes {
		again, ok := secondDAG.Nodes[id]
		if !ok || again.FQCN != node.FQCN {
			t.Errorf("round-trip lost or changed node %q", id)
		}
	}
	if len(firstDAG.Adjacency["tasks[0]"]) != len(secondDAG.Adjacency["tasks[0]"]) {
		t.Errorf("round-trip changed edge count for tasks[0]")
	}
}

// TestBuildFromYAML_NestedBlockRescueAlways confirms the YAML surface
// accepts nested block/rescue/always, producing the same synthesized ID
// shape and happy-path splicing as the JSON surface.
func TestBuildFromYAML_NestedBlockRescueAlways(t *testing.T) {
	eval, _ := engine.NewCELEvaluator()
	builder := engine.NewBuilder(eval)

	payload := []byte(`
id: runbook-block
pretasks:
  - name: setup
    fqcn: noop
tasks:
  - name: risky
    block:
      - name: inner
        block:
          - name: leaf
            fqcn: ssh_exec
    rescue:
      - name: recover
        fqcn: noop
    always:
      - name: cleanup
        fqcn: noop
posttasks:
  - name: teardown
    fqcn: noop
`)

	dag, err := builder.BuildFromYAML(payload)
	if err != nil {
		t.Fatalf("failed to build DAG from nested YAML: %v", err)
	}

	wantNodes := []string{
		"pretasks[0]", "tasks[0]", "tasks[0].block[0]", "tasks[0].block[0].block[0]",
		"tasks[0].rescue[0]", "tasks[0].always[0]", "posttasks[0]",
	}
	if len(dag.Nodes) != len(wantNodes) {
		t.Fatalf("expected %d nodes, got %d: %v", len(wantNodes), len(dag.Nodes), dag.Nodes)
	}
	for _, id := range wantNodes {
		if _, ok := dag.Nodes[id]; !ok {
			t.Errorf("expected node %q to exist", id)
		}
	}

	// The happy path splices straight through to the deepest leaf:
	// pretasks[0] -> tasks[0].block[0].block[0] -> posttasks[0].
	if len(dag.Adjacency["pretasks[0]"]) != 1 || dag.Adjacency["pretasks[0]"][0].To != "tasks[0].block[0].block[0]" {
		t.Fatalf("expected pretasks[0] -> tasks[0].block[0].block[0], got %v", dag.Adjacency["pretasks[0]"])
	}
	if len(dag.Adjacency["tasks[0].block[0].block[0]"]) != 1 || dag.Adjacency["tasks[0].block[0].block[0]"][0].To != "posttasks[0]" {
		t.Fatalf("expected tasks[0].block[0].block[0] -> posttasks[0], got %v", dag.Adjacency["tasks[0].block[0].block[0]"])
	}

	// Rescue and always are not part of the happy path.
	if _, ok := dag.Adjacency["tasks[0].rescue[0]"]; ok {
		t.Errorf("expected tasks[0].rescue[0] to have no outgoing adjacency")
	}
	if _, ok := dag.Adjacency["tasks[0].always[0]"]; ok {
		t.Errorf("expected tasks[0].always[0] to have no outgoing adjacency")
	}
}

func TestBuildFromYAML_MalformedInput(t *testing.T) {
	eval, _ := engine.NewCELEvaluator()
	builder := engine.NewBuilder(eval)

	cases := []string{
		"not: valid: yaml: at: all: :::",
		"id: [this, is, a, list, not, a, string]",
	}

	for _, c := range cases {
		if _, err := builder.BuildFromYAML([]byte(c)); err == nil {
			t.Errorf("expected an error for malformed input %q", c)
		}
	}
}

// TestBuildFromYAML_EmptyDocument documents the actual (correct) behavior
// for an empty runbook: an empty YAML document is not malformed, it is a
// zero-task WorkflowDef, and builds a trivial but valid DAG.
func TestBuildFromYAML_EmptyDocument(t *testing.T) {
	eval, _ := engine.NewCELEvaluator()
	builder := engine.NewBuilder(eval)

	dag, err := builder.BuildFromYAML([]byte(""))
	if err != nil {
		t.Fatalf("expected an empty document to build a trivial DAG, got error: %v", err)
	}
	if len(dag.Nodes) != 0 {
		t.Errorf("expected 0 nodes for an empty document, got %d", len(dag.Nodes))
	}
}

// TestBuildFromYAML_TypeDefault confirms a runbook with no `type` field, the
// current default shape shared by every existing runbook including the
// CLI's scaffolded sample, still builds successfully and unchanged.
func TestBuildFromYAML_TypeDefault(t *testing.T) {
	eval, _ := engine.NewCELEvaluator()
	builder := engine.NewBuilder(eval)

	payload := []byte(`
id: no-type-field
tasks:
  - name: start
    fqcn: noop
`)

	dag, err := builder.BuildFromYAML(payload)
	if err != nil {
		t.Fatalf("expected runbook with no type field to build, got error: %v", err)
	}
	if len(dag.Nodes) != 1 {
		t.Errorf("expected 1 node, got %d", len(dag.Nodes))
	}
}

// TestBuildFromYAML_TypeNativeExplicit confirms an explicit `type: native`
// builds successfully, identically to an absent Type field.
func TestBuildFromYAML_TypeNativeExplicit(t *testing.T) {
	eval, _ := engine.NewCELEvaluator()
	builder := engine.NewBuilder(eval)

	payload := []byte(`
id: explicit-native
type: native
tasks:
  - name: start
    fqcn: noop
`)

	dag, err := builder.BuildFromYAML(payload)
	if err != nil {
		t.Fatalf("expected runbook with type: native to build, got error: %v", err)
	}
	if len(dag.Nodes) != 1 {
		t.Errorf("expected 1 node, got %d", len(dag.Nodes))
	}
}

// TestBuildFromYAML_TypeAnsibleRejected confirms `type: ansible` produces the
// specific, actionable error described for the (currently unimplemented)
// Ansible interop path, not a generic parse failure.
func TestBuildFromYAML_TypeAnsibleRejected(t *testing.T) {
	eval, _ := engine.NewCELEvaluator()
	builder := engine.NewBuilder(eval)

	payload := []byte(`
id: an-ansible-runbook
type: ansible
tasks: []
`)

	_, err := builder.BuildFromYAML(payload)
	if err == nil {
		t.Fatalf("expected an error for type: ansible, got nil")
	}
	if !strings.Contains(err.Error(), "ansible") {
		t.Errorf("expected error to mention %q, got: %v", "ansible", err)
	}
	if !strings.Contains(err.Error(), "PLAN.md Section 23") {
		t.Errorf("expected error to be actionable (reference PLAN.md Section 23), got: %v", err)
	}
}

// TestBuildFromYAML_TypeUnrecognizedRejected confirms an unrecognized,
// non-empty type value produces a clear, specific error rather than
// silently proceeding as if it were native.
func TestBuildFromYAML_TypeUnrecognizedRejected(t *testing.T) {
	eval, _ := engine.NewCELEvaluator()
	builder := engine.NewBuilder(eval)

	payload := []byte(`
id: mystery-type
type: something-else
tasks: []
`)

	_, err := builder.BuildFromYAML(payload)
	if err == nil {
		t.Fatalf("expected an error for an unrecognized type, got nil")
	}
	if !strings.Contains(err.Error(), "something-else") {
		t.Errorf("expected error to mention the offending type value, got: %v", err)
	}
	if !strings.Contains(err.Error(), "native") || !strings.Contains(err.Error(), "ansible") {
		t.Errorf("expected error to list the recognized types, got: %v", err)
	}
}

// TestBuildFromYAML_AnsiblePlaybookShapeRejected confirms a realistic,
// minimal Ansible-shaped playbook (a top-level YAML list of plays) is
// rejected with the specific "shaped like an Ansible playbook" error, not a
// generic YAML-unmarshal error.
func TestBuildFromYAML_AnsiblePlaybookShapeRejected(t *testing.T) {
	eval, _ := engine.NewCELEvaluator()
	builder := engine.NewBuilder(eval)

	payload := []byte("- hosts: all\n  tasks: []\n")

	_, err := builder.BuildFromYAML(payload)
	if err == nil {
		t.Fatalf("expected an error for a top-level YAML list, got nil")
	}
	if !strings.Contains(err.Error(), "Ansible playbook") {
		t.Errorf("expected error to identify the Ansible playbook shape, got: %v", err)
	}
	if !strings.Contains(err.Error(), "PLAN.md Section 23") {
		t.Errorf("expected error to be actionable (reference PLAN.md Section 23), got: %v", err)
	}
}

// TestBuildFromYAML_NonAnsibleListNotOverclaimed guards against a false
// positive an adversarial review found: a bare top-level list with no play
// shape at all (empty, or scalars rather than maps) is not automatically
// Ansible-shaped. The error must still reject it (it is not a valid runbook
// either), but must not claim it is specifically an Ansible playbook.
func TestBuildFromYAML_NonAnsibleListNotOverclaimed(t *testing.T) {
	eval, _ := engine.NewCELEvaluator()
	builder := engine.NewBuilder(eval)

	cases := []string{
		"[]",
		"- foo\n- bar\n",
	}

	for _, c := range cases {
		_, err := builder.BuildFromYAML([]byte(c))
		if err == nil {
			t.Fatalf("expected an error for top-level list %q, got nil", c)
		}
		if strings.Contains(err.Error(), "Ansible playbook") {
			t.Errorf("expected %q to NOT be diagnosed as an Ansible playbook, got: %v", c, err)
		}
	}
}

// TestBuildFromYAML_RejectsAliasBomb is Phase 39's (Schema & Injection
// Hardening) curated regression test for a known attack shape: a YAML
// "billion laughs" document, a handful of anchors that each alias the
// previous level several times, expanding exponentially by the time the
// parser reaches the last one. Empirically verified (Phase 39) against
// this exact parser (go.yaml.in/yaml/v3): the library's own
// allowedAliasRatio guard rejects this with "excessive aliasing" in
// milliseconds rather than expanding it, so this asserts that behavior
// explicitly as a named claim instead of leaving it as an emergent
// property nobody wrote down. This is a curated, known-attack-shape test,
// not a fuzz-discovered one: FuzzBuildFromYAML already carries a smaller,
// self-referential-anchor seed, but a fuzzer finding an input that merely
// doesn't panic is a different claim than "this specific attack class is
// rejected quickly."
func TestBuildFromYAML_RejectsAliasBomb(t *testing.T) {
	eval, _ := engine.NewCELEvaluator()
	builder := engine.NewBuilder(eval)

	bomb := "id: bomb\n" +
		"tasks:\n" +
		"  - name: a\n" +
		"    fqcn: noop\n" +
		"    params:\n" +
		"      a: &a [1,1,1,1,1,1,1,1,1]\n" +
		"      b: &b [*a,*a,*a,*a,*a,*a,*a,*a,*a]\n" +
		"      c: &c [*b,*b,*b,*b,*b,*b,*b,*b,*b]\n" +
		"      d: &d [*c,*c,*c,*c,*c,*c,*c,*c,*c]\n" +
		"      e: &e [*d,*d,*d,*d,*d,*d,*d,*d,*d]\n" +
		"      f: [*e,*e,*e,*e,*e,*e,*e,*e,*e]\n"

	start := time.Now()
	_, err := builder.BuildFromYAML([]byte(bomb))
	elapsed := time.Since(start)

	if err == nil {
		t.Fatalf("expected an alias-bomb document to be rejected, got success in %v", elapsed)
	}
	if !strings.Contains(err.Error(), "alias") {
		t.Errorf("expected an aliasing-related error, got: %v", err)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("expected rejection well before this, took %v: %v", elapsed, err)
	}
}

// TestBuildFromYAML_CatalogMetadata proves the fields a catalog browses by
// survive the real parse-and-compile path.
//
// They are worth a test of their own rather than being assumed: they are
// the only fields in a runbook that nothing in execution reads, so a
// regression that dropped them would break no run, fail no existing test,
// and show up as a catalog that has quietly gone blank.
//
// The labels-not-tags decision is asserted here too. Ansible's tags: means
// task selection at run time, Pleiades is a superset of Ansible, and a
// runbook that spelled its catalog filters "tags" would collide with that
// the day real tag selection lands.
func TestBuildFromYAML_CatalogMetadata(t *testing.T) {
	const payload = `
id: patch-tuesday
name: Monthly Patch Window
metadata:
  description: Applies pending security updates and reboots if required.
  category: patching
  labels:
    - compliance
    - disruptive
tasks:
  - name: step
    fqcn: noop
`

	eval, _ := engine.NewCELEvaluator()
	dag, err := engine.NewBuilder(eval).BuildFromYAML([]byte(payload))
	if err != nil {
		t.Fatalf("BuildFromYAML() = %v, want nil", err)
	}

	if dag.Name != "Monthly Patch Window" {
		t.Errorf("Name = %q, want the play's own name:", dag.Name)
	}
	if dag.Metadata.Description == "" {
		t.Error("Description was dropped between the file and the compiled DAG")
	}
	if dag.Metadata.Category != "patching" {
		t.Errorf("Category = %q, want patching", dag.Metadata.Category)
	}
	if len(dag.Metadata.Labels) != 2 {
		t.Fatalf("Labels = %v, want two", dag.Metadata.Labels)
	}
}

// TestBuildFromYAML_CatalogMetadataIsOptional: every field added here is
// additive, so a runbook written before they existed must still compile
// unchanged. This is the assertion that would fail if any of them were
// accidentally made required.
func TestBuildFromYAML_CatalogMetadataIsOptional(t *testing.T) {
	const payload = `
id: bare
tasks:
  - name: step
    fqcn: noop
`

	eval, _ := engine.NewCELEvaluator()
	dag, err := engine.NewBuilder(eval).BuildFromYAML([]byte(payload))
	if err != nil {
		t.Fatalf("BuildFromYAML() on a runbook with no catalog metadata = %v, want nil", err)
	}
	if dag.Name != "" {
		t.Errorf("Name = %q, want empty so the catalog can fall back to the id", dag.Name)
	}
	if dag.Metadata.Category != "" || len(dag.Metadata.Labels) != 0 {
		t.Error("absent catalog metadata decoded as something other than its zero value")
	}
}
