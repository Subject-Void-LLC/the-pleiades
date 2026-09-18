// Tests for the setup profile's rules, on synthetic renders.
package main

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/setup"
)

// setupRender builds the objects a correct render of setup's values holds.
func setupRender(checksum, fingerprint string) []manifest {
	var controller manifest
	controller.Kind = "Deployment"
	controller.Metadata.Name = "r-the-pleiades-controller"
	controller.Spec.Template.Metadata.Annotations = map[string]string{"checksum/secret": checksum}
	controller.Spec.Template.Spec.Containers = []container{{
		Name: "controller",
		Env: []envVar{
			{Name: "POSTGRES_PASSWORD", ValueFrom: &envVarSource{SecretKeyRef: &secretKeyRef{Name: setupSecretName, Key: "POSTGRES_PASSWORD"}}},
			{Name: "DB_DSN", Value: "postgres://pleiades:$(POSTGRES_PASSWORD)@r-the-pleiades-postgres:5432/pleiades?sslmode=disable"},
			{Name: "MASTER_ENCRYPTION_KEY", ValueFrom: &envVarSource{SecretKeyRef: &secretKeyRef{Name: setupSecretName, Key: "MASTER_ENCRYPTION_KEY"}}},
		},
	}}
	var postgres manifest
	postgres.Kind = "StatefulSet"
	postgres.Metadata.Name = "r-the-pleiades-postgres"
	postgres.Spec.VolumeClaimTemplates = []claimTemplate{{Metadata: objectMeta{Name: "data", Annotations: map[string]string{postgresStampAnnotation: fingerprint}}}}
	return []manifest{controller, postgres}
}

// TestCheckExistingSecretShape covers each way the chart and setup's output
// could disagree.
func TestCheckExistingSecretShape(t *testing.T) {
	emitted := setupOutput{
		keys:     map[string]bool{"MASTER_ENCRYPTION_KEY": true, "JWT_SECRET": true, "POSTGRES_PASSWORD": true},
		checksum: strings.Repeat("c", 64), fingerprint: strings.Repeat("f", 16),
	}
	if got := checkExistingSecretShape(setupRender(emitted.checksum, emitted.fingerprint), emitted); len(got) != 0 {
		t.Fatalf("a correct render produced findings: %v", got)
	}

	cases := map[string]func([]manifest) []manifest{
		"a key setup does not write": func(m []manifest) []manifest {
			m[0].Spec.Template.Spec.Containers[0].Env[2].ValueFrom.SecretKeyRef.Key = "DB_DSN"
			return m
		},
		"a connection string not built from the password": func(m []manifest) []manifest {
			m[0].Spec.Template.Spec.Containers[0].Env[1].Value = "postgres://fixed"
			return m
		},
		"the password defined after the connection string": func(m []manifest) []manifest {
			env := m[0].Spec.Template.Spec.Containers[0].Env
			env[0], env[1] = env[1], env[0]
			return m
		},
		"the wrong checksum": func(m []manifest) []manifest {
			m[0].Spec.Template.Metadata.Annotations["checksum/secret"] = ""
			return m
		},
		"the wrong fingerprint": func(m []manifest) []manifest {
			m[1].Spec.VolumeClaimTemplates[0].Metadata.Annotations[postgresStampAnnotation] = ""
			return m
		},
		"a Secret of the chart's own under the operator's name": func(m []manifest) []manifest {
			var s manifest
			s.Kind = "Secret"
			s.Metadata.Name = setupSecretName
			return append(m, s)
		},
	}
	for name, mutate := range cases {
		if got := checkExistingSecretShape(mutate(setupRender(emitted.checksum, emitted.fingerprint)), emitted); len(got) == 0 {
			t.Errorf("%s produced no finding", name)
		}
	}
}

// TestCheckFingerprintFormulaAgrees proves the rule passes on the chart's
// formula and fails on anything else.
func TestCheckFingerprintFormulaAgrees(t *testing.T) {
	good := setup.PostgresCredentialFingerprint("pleiades", "pleiades", "pw")
	if got := checkFingerprintFormulaAgrees("p", setupRender("", good), "pw"); len(got) != 0 {
		t.Fatalf("matching fingerprints produced findings: %v", got)
	}
	if got := checkFingerprintFormulaAgrees("p", setupRender("", "0000000000000000"), "pw"); len(got) != 1 {
		t.Fatalf("a mismatched fingerprint produced %d findings, want 1", len(got))
	}
}
