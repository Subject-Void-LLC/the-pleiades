package collection_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// TestManifest_RoundTrip is this phase's Release Gate: a hand-written
// Manifest, covering every field, round-trips through JSON (the stable
// serialized form Phase 42 later embeds as an OCI config layer) without
// losing or altering any of it.
func TestManifest_RoundTrip(t *testing.T) {
	original := collection.Manifest{
		SupportedTransports: []string{"ssh"},
		RequiredCapabilities: []capability.Name{
			capability.NameSSHTransport,
			capability.NameLinux,
		},
		ExecutionContext: collection.ExecutionContext{RequiresElevation: true},
		PlatformTargets: []collection.PlatformTarget{
			{
				Vendor:            "cisco",
				Model:             "isr4000",
				VersionRange:      "15.2 - 16.9",
				DeploymentContext: "carrier-only",
			},
		},
		EngineVersion:    ">=1.0.0",
		Status:           collection.StatusDeclared,
		EndsLoginSession: true,
		SeedsLogin:       "login",
	}

	encoded, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: unexpected error: %v", err)
	}

	var decoded collection.Manifest
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal: unexpected error: %v", err)
	}

	if !reflect.DeepEqual(original, decoded) {
		t.Fatalf("round-trip mismatch:\n  original: %+v\n  decoded:  %+v", original, decoded)
	}
}

// TestManifest_RoundTripZeroValue proves an entirely empty Manifest (the
// "declared, nothing else known yet" case) round-trips too, not just a
// fully populated one.
func TestManifest_RoundTripZeroValue(t *testing.T) {
	original := collection.Manifest{Status: collection.StatusDeclared}

	encoded, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: unexpected error: %v", err)
	}

	var decoded collection.Manifest
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal: unexpected error: %v", err)
	}

	if !reflect.DeepEqual(original, decoded) {
		t.Fatalf("round-trip mismatch:\n  original: %+v\n  decoded:  %+v", original, decoded)
	}
}
