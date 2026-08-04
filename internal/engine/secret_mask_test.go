package engine_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/engine"
)

// TestDAGBuilder_SecretFieldsAndSecretMaskRoundTrip confirms secret_fields
// and secret_mask parse off a runbook and land on the compiled Task
// unchanged, the same round-trip guarantee every other Task field already
// has.
func TestDAGBuilder_SecretFieldsAndSecretMaskRoundTrip(t *testing.T) {
	eval, _ := engine.NewCELEvaluator()
	builder := engine.NewBuilder(eval)

	payload := []byte(`{
		"id": "runbook-secret-mask",
		"tasks": [
			{"name": "mark", "fqcn": "noop", "register": "creds", "secret_fields": ["password", "token"]},
			{"name": "mask", "fqcn": "noop", "secret_mask": {"register": "creds", "fields": ["password"]}}
		]
	}`)

	dag, err := builder.Build(payload)
	if err != nil {
		t.Fatalf("failed to build valid DAG: %v", err)
	}

	markTask := dag.Nodes["tasks[0]"]
	if !reflect.DeepEqual(markTask.SecretFields, []string{"password", "token"}) {
		t.Errorf("expected SecretFields to round-trip, got %#v", markTask.SecretFields)
	}
	if markTask.SecretMask != nil {
		t.Errorf("expected tasks[0] to have no SecretMask, got %#v", markTask.SecretMask)
	}

	maskTask := dag.Nodes["tasks[1]"]
	if maskTask.SecretMask == nil {
		t.Fatalf("expected tasks[1] to have a SecretMask")
	}
	want := &engine.SecretMaskSpec{Register: "creds", Fields: []string{"password"}}
	if !reflect.DeepEqual(maskTask.SecretMask, want) {
		t.Errorf("expected SecretMask to round-trip as %#v, got %#v", want, maskTask.SecretMask)
	}
}

// TestDAGBuilder_SecretMaskEmptyRegisterIsError confirms a secret_mask
// with no register named is rejected at build time, rather than silently
// no-op'ing at runtime.
func TestDAGBuilder_SecretMaskEmptyRegisterIsError(t *testing.T) {
	eval, _ := engine.NewCELEvaluator()
	builder := engine.NewBuilder(eval)

	payload := []byte(`{
		"id": "runbook-secret-mask-empty-register",
		"tasks": [
			{"name": "mask", "fqcn": "noop", "secret_mask": {"fields": ["password"]}}
		]
	}`)

	_, err := builder.Build(payload)
	if err == nil {
		t.Fatalf("expected an error for secret_mask with an empty register")
	}
	if !strings.Contains(err.Error(), "tasks[0]") {
		t.Errorf("expected error to name tasks[0], got: %v", err)
	}
}

// TestDAGBuilder_SecretMaskEmptyFieldsIsError confirms a secret_mask with
// no fields named is rejected at build time.
func TestDAGBuilder_SecretMaskEmptyFieldsIsError(t *testing.T) {
	eval, _ := engine.NewCELEvaluator()
	builder := engine.NewBuilder(eval)

	payload := []byte(`{
		"id": "runbook-secret-mask-empty-fields",
		"tasks": [
			{"name": "mask", "fqcn": "noop", "secret_mask": {"register": "creds"}}
		]
	}`)

	_, err := builder.Build(payload)
	if err == nil {
		t.Fatalf("expected an error for secret_mask with no fields")
	}
	if !strings.Contains(err.Error(), "tasks[0]") {
		t.Errorf("expected error to name tasks[0], got: %v", err)
	}
}
