// Fuzz test of a program's probe output: the stats an untrusted probe sets,
// read the way onboarding reads them and recorded the way it records them.
package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// probeFuzzDefinition is the capability the fuzzed probe answers for.
func probeFuzzDefinition() capability.DataDefinition {
	return capability.DataDefinition{
		Name: "fuzzprobe.widget", Provider: "fuzzprobe", Probe: "fuzzprobe.widget.probe",
		Requires: []capability.Name{capability.NameSSHTransport},
		Properties: []capability.DataProperty{
			{Name: "endpoint", Type: "string", Required: true},
			{Name: "port", Type: "int"},
			{Name: "enabled", Type: "bool"},
		},
	}
}

// FuzzProbeOutput: whatever stats a probe sets, onboarding records a grant
// only when the probe said granted with properties its definition accepts;
// what it records holds only declared properties of their declared types,
// never a value shaped like a secret, carries the definition's digest and
// the probing build, reads back exactly as written, and grants the
// capability on a device holding what the definition requires, with no
// note against it.
func FuzzProbeOutput(f *testing.F) {
	for _, seed := range []string{
		`{"granted":true,"properties":{"endpoint":"https://w","port":443,"enabled":true}}`,
		`{"granted":true,"properties":{"endpoint":"https://w","port":443.5}}`,
		`{"granted":true,"properties":{"endpoint":"https://ops:hunter2-Orchid@w.example/api"}}`,
		`{"granted":true,"properties":{"endpoint":"x","password":"hunter2"}}`,
		`{"granted":"true","properties":{"endpoint":"x"}}`,
		`{"granted":true,"properties":["endpoint"]}`,
		`{"granted":true}`,
		`{"granted":false,"properties":{"endpoint":"x"}}`,
		`{"granted":true,"properties":{"endpoint":"a\u001b[2Jb"}}`,
		`{"granted":true,"properties":{"endpoint":"-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXktdjEAAAAA\n-----END OPENSSH PRIVATE KEY-----"}}`,
		`{"granted":true,"properties":{"endpoint":"x","port":1e300}}`,
		`{"note":"anything else a probe set","granted":true,"properties":{"endpoint":"x"}}`,
		`null`,
	} {
		f.Add([]byte(seed))
	}
	restore := capability.SnapshotForTest()
	f.Cleanup(restore)
	def := probeFuzzDefinition()
	if err := capability.RegisterData(def); err != nil {
		f.Fatal(err)
	}
	build := "sha256:" + strings.Repeat("cd", 32)
	f.Fuzz(func(t *testing.T, data []byte) {
		var stats map[string]any
		if json.Unmarshal(data, &stats) != nil {
			return
		}
		granted, props := probeOutcome(stats)
		if want, _ := stats[probeStatGranted].(bool); granted != want {
			t.Fatalf("probeOutcome read granted %v from %s", granted, data)
		}
		if !granted {
			return
		}
		grant, err := probedGrant(def, build, "", props)
		if err != nil {
			if capability.ValidateGrant(def, props) == nil {
				t.Fatalf("probedGrant refused properties the definition accepts: %v", err)
			}
			return
		}
		for key := range grant.Properties {
			if !declaredProperty(def, key) {
				t.Fatalf("a grant was made holding the undeclared property %q", key)
			}
		}
		if grant.Definition != capability.DefinitionDigest(def) || grant.Build != build || grant.Source != inventory.GrantProbed {
			t.Fatalf("the grant is not bound as a probe's must be: %+v", grant)
		}

		base := record.NewBase(record.Record{Name: "w1", Type: "fuzz_probe_type", Properties: map[string]inventory.PropertyValue{}}, nil)
		if err := base.RecordGrant(grant); err != nil {
			if !secretShaped(grant.Properties) {
				t.Fatalf("RecordGrant refused a grant holding nothing shaped like a secret: %v", err)
			}
			if _, present := base.Properties().Raw()[inventory.GrantedProperty]; present {
				t.Fatal("a refused grant was written")
			}
			return
		}
		if secretShaped(grant.Properties) {
			t.Fatalf("a grant holding a value shaped like a secret was recorded: %v", grant.Properties)
		}
		stored := base.Properties().Raw()
		rebuilt := record.NewBase(record.Record{Name: "w1", Type: "fuzz_probe_type", Properties: stored}, []capability.Name{capability.NameSSHTransport})
		if !rebuilt.Declares(def.Name) || len(rebuilt.GrantNotes()) != 0 {
			t.Fatalf("the recorded grant does not grant %s: notes %+v", def.Name, rebuilt.GrantNotes())
		}
		back, _, err := inventory.GrantsFrom(rebuilt.Properties())
		if err != nil || len(back) != 1 || back[0].Definition != grant.Definition || back[0].Build != build || len(back[0].Properties) != len(grant.Properties) {
			t.Fatalf("the grant read back as %+v, %v", back, err)
		}
	})
}

// declaredProperty reports whether def declares a property named key.
func declaredProperty(def capability.DataDefinition, key string) bool {
	for _, p := range def.Properties {
		if p.Name == key {
			return true
		}
	}
	return false
}

// secretShaped reports whether any text value of props is shaped like a
// secret.
func secretShaped(props map[string]any) bool {
	for _, v := range props {
		if s, ok := v.(string); ok && redact.SecretShaped(s) {
			return true
		}
	}
	return false
}
