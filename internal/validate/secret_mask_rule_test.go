package validate_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/validate"
)

// dagWithSecretMask builds a two-node *engine.DAG: tasks[0] registers
// under registerName, tasks[1] carries a secret_mask referencing
// maskRegister. Both tasks are fqcn "noop" with no target, so
// CapabilityRule never fires and only SecretMaskRule's own behavior is
// under test.
func dagWithSecretMask(registerName, maskRegister string) *engine.DAG {
	return &engine.DAG{
		ID: "t",
		Nodes: map[string]*engine.Task{
			"tasks[0]": {Name: "collect", FQCN: "noop", Register: registerName},
			"tasks[1]": {
				Name: "mask it",
				FQCN: "noop",
				SecretMask: &engine.SecretMaskSpec{
					Register: maskRegister,
					Fields:   []string{"password"},
				},
			},
		},
		Adjacency: map[string][]engine.EdgeConfig{},
	}
}

// TestSecretMaskRule_ValidReferencePasses confirms a secret_mask.register
// matching a real Register produces no Finding.
func TestSecretMaskRule_ValidReferencePasses(t *testing.T) {
	world := validate.WorldView{DAG: dagWithSecretMask("creds", "creds")}
	report := validate.Validate(world)
	if report.HasErrors() {
		t.Errorf("expected no findings for a valid secret_mask reference, got: %s", report.String())
	}
}

// TestSecretMaskRule_TypoedReferenceProducesFinding confirms a
// secret_mask.register that matches no task's Register anywhere in the
// DAG is caught, naming the offending task and the unresolved register.
func TestSecretMaskRule_TypoedReferenceProducesFinding(t *testing.T) {
	world := validate.WorldView{DAG: dagWithSecretMask("creds", "cred")}
	report := validate.Validate(world)
	if !report.HasErrors() {
		t.Fatal("expected a finding for a secret_mask.register that matches no task's register")
	}

	msg := report.String()
	if !strings.Contains(msg, "cred") {
		t.Errorf("expected the message to name the unresolved register, got: %s", msg)
	}
	if !strings.Contains(msg, "tasks[1]") {
		t.Errorf("expected the message to name the offending task, got: %s", msg)
	}
	if !strings.Contains(msg, "mask it") {
		t.Errorf("expected the message to include the task's Name, got: %s", msg)
	}
}

// TestSecretMaskRule_NoSecretMaskIsUnaffected confirms a DAG with no
// secret_mask task at all produces no finding from this rule.
func TestSecretMaskRule_NoSecretMaskIsUnaffected(t *testing.T) {
	world := validate.WorldView{
		DAG: &engine.DAG{
			ID:        "t",
			Nodes:     map[string]*engine.Task{"tasks[0]": {Name: "plain", FQCN: "noop"}},
			Adjacency: map[string][]engine.EdgeConfig{},
		},
	}
	report := validate.Validate(world)
	if report.HasErrors() {
		t.Errorf("expected no findings, got: %s", report.String())
	}
}
