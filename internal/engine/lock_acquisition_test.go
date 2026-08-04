package engine_test

import (
	"encoding/json"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/engine"
	"go.yaml.in/yaml/v3"
)

func TestAcquisitionStrategyString(t *testing.T) {
	tests := []struct {
		strategy engine.AcquisitionStrategy
		want     string
	}{
		{engine.AcquisitionPerDeviceAsReached, "per_device_as_reached"},
		{engine.AcquisitionAllAtPlanTime, "all_at_plan_time"},
		{engine.AcquisitionStrategy(99), "unknown"},
	}
	for _, tc := range tests {
		if got := tc.strategy.String(); got != tc.want {
			t.Errorf("AcquisitionStrategy(%d).String() = %q, want %q", tc.strategy, got, tc.want)
		}
	}
}

func TestParseAcquisitionStrategy(t *testing.T) {
	got, err := engine.ParseAcquisitionStrategy("all_at_plan_time")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != engine.AcquisitionAllAtPlanTime {
		t.Errorf("expected AcquisitionAllAtPlanTime, got %v", got)
	}

	if _, err := engine.ParseAcquisitionStrategy("not-a-real-strategy"); err == nil {
		t.Fatal("expected an error for an unrecognized strategy string, got nil")
	}
}

// TestAcquisitionStrategyYAMLRoundTrip proves a runbook author can write
// the human-readable string a real .yaml file would use, not the
// underlying int, closing a real gap found while manually verifying this
// phase's own runbook syntax against the built binary.
func TestAcquisitionStrategyYAMLRoundTrip(t *testing.T) {
	var s engine.AcquisitionStrategy
	if err := yaml.Unmarshal([]byte("all_at_plan_time"), &s); err != nil {
		t.Fatalf("unexpected error unmarshaling YAML: %v", err)
	}
	if s != engine.AcquisitionAllAtPlanTime {
		t.Errorf("expected AcquisitionAllAtPlanTime, got %v", s)
	}

	out, err := yaml.Marshal(s)
	if err != nil {
		t.Fatalf("unexpected error marshaling YAML: %v", err)
	}
	if got := string(out); got != "all_at_plan_time\n" {
		t.Errorf("expected round-tripped YAML %q, got %q", "all_at_plan_time\n", got)
	}

	if err := yaml.Unmarshal([]byte("not-a-real-strategy"), &s); err == nil {
		t.Fatal("expected an error unmarshaling an unrecognized YAML string, got nil")
	}
}

func TestAcquisitionStrategyJSONRoundTrip(t *testing.T) {
	var s engine.AcquisitionStrategy
	if err := json.Unmarshal([]byte(`"all_at_plan_time"`), &s); err != nil {
		t.Fatalf("unexpected error unmarshaling JSON: %v", err)
	}
	if s != engine.AcquisitionAllAtPlanTime {
		t.Errorf("expected AcquisitionAllAtPlanTime, got %v", s)
	}

	out, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("unexpected error marshaling JSON: %v", err)
	}
	if got := string(out); got != `"all_at_plan_time"` {
		t.Errorf("expected round-tripped JSON %q, got %q", `"all_at_plan_time"`, got)
	}

	if err := json.Unmarshal([]byte(`"not-a-real-strategy"`), &s); err == nil {
		t.Fatal("expected an error unmarshaling an unrecognized JSON string, got nil")
	}
}
