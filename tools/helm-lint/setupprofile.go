// The one profile whose values are not written by hand: the values file the
// controller's setup command emits, rendered against the chart exactly as an
// operator would install it.
//
// The phase that added setup names its failure mode plainly: emitting the
// wrong shape for the target. A Secret missing a key the chart reads installs
// cleanly and then crash-loops, and a values file the chart refuses fails at
// install time on the operator's machine. Rendering setup's own output here
// catches both on every run, offline, before a cluster is involved.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/Subject-Void-LLC/the-pleiades/internal/setup"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
)

// setupProfileName names the profile in every finding.
const setupProfileName = "values written by controller setup"

// setupSecretName is the Secret name the profile renders with. It is not the
// default, so a template that ignored secrets.existingSecret and read its own
// name would be caught reading the wrong Secret.
const setupSecretName = "operator-managed-secrets"

// setupOutput is what setup emitted, decoded.
type setupOutput struct {
	// keys are the keys the emitted Secret carries.
	keys map[string]bool

	// checksum and fingerprint are what the values file tells the chart.
	checksum, fingerprint string
}

// checkSetupOutput renders setup's Helm output against the chart and returns
// what that proves. It returns an error only when it cannot run at all.
func checkSetupOutput(chart string) (int, []finding, error) {
	secretYAML, valuesYAML, err := setup.RenderHelm(setup.HelmInput{
		SecretName:       setupSecretName,
		Namespace:        "pleiades",
		MasterKey:        []byte(strings.Repeat("s", 32)),
		JWTSecret:        strings.Repeat("J", 44),
		PostgresPassword: strings.Repeat("ab", 32),
		// Longer than the runner's default liveness window, so the values
		// file has to raise that window too or the chart refuses it.
		Budget: topology.OutageBudget(2 * time.Hour),
	})
	if err != nil {
		return 0, nil, fmt.Errorf("setup could not render its Helm output: %w", err)
	}
	emitted, err := decodeSetupOutput(secretYAML, valuesYAML)
	if err != nil {
		return 0, nil, err
	}

	dir, err := os.MkdirTemp("", "helm-lint-setup-")
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	valuesFile := filepath.Join(dir, setup.HelmValuesFile)
	if err := os.WriteFile(valuesFile, valuesYAML, 0o600); err != nil {
		return 0, nil, err
	}

	// No baseValues: the values file alone has to be enough, which is the
	// point of it.
	if out, err := runHelm([]string{"lint", "--strict", chart, "-f", valuesFile}); err != nil {
		return 0, []finding{{profile: setupProfileName, object: "helm lint", message: "the chart rejects setup's values file:\n" + indent(out)}}, nil
	}
	rendered, err := runHelm([]string{"template", "helm-lint", chart, "-f", valuesFile})
	if err != nil {
		return 0, []finding{{profile: setupProfileName, object: "helm template", message: "the chart refuses setup's values file:\n" + indent(rendered)}}, nil
	}
	objects, err := decodeManifests(rendered)
	if err != nil {
		return 0, nil, fmt.Errorf("parsing the render of setup's values: %w", err)
	}

	var findings []finding
	findings = append(findings, checkManifests(setupProfileName, objects, "HTTPS")...)
	findings = append(findings, checkRetainedDataStamp(setupProfileName, objects)...)
	findings = append(findings, checkExistingSecretShape(objects, emitted)...)
	return countContainers(objects), findings, nil
}

// decodeSetupOutput reads the keys of the emitted Secret and the two guard
// values of the emitted values file.
func decodeSetupOutput(secretYAML, valuesYAML []byte) (setupOutput, error) {
	var secret struct {
		StringData map[string]string `yaml:"stringData"`
	}
	if err := yaml.Unmarshal(secretYAML, &secret); err != nil {
		return setupOutput{}, fmt.Errorf("setup's Secret does not parse: %w", err)
	}
	var values struct {
		Secrets struct {
			ExistingSecretChecksum string `yaml:"existingSecretChecksum"`
		} `yaml:"secrets"`
		Postgresql struct {
			Auth struct {
				ExistingSecretFingerprint string `yaml:"existingSecretFingerprint"`
			} `yaml:"auth"`
		} `yaml:"postgresql"`
	}
	if err := yaml.Unmarshal(valuesYAML, &values); err != nil {
		return setupOutput{}, fmt.Errorf("setup's values file does not parse: %w", err)
	}
	out := setupOutput{
		keys:        map[string]bool{},
		checksum:    values.Secrets.ExistingSecretChecksum,
		fingerprint: values.Postgresql.Auth.ExistingSecretFingerprint,
	}
	for k := range secret.StringData {
		out.keys[k] = true
	}
	return out, nil
}

// checkExistingSecretShape is the rule the profile exists for.
//
// Every Secret key a workload reads from the operator's Secret must be one
// setup's Secret carries. The chart must not render a Secret of its own
// under that name, which would overwrite the operator's on install. The
// controller's pods must carry setup's checksum, so a new Secret rolls them.
// The database's volume must carry setup's fingerprint, so a reinstall with
// a different password is refused. And the connection string must be built
// from the Secret's password, with that variable defined before it.
func checkExistingSecretShape(objects []manifest, emitted setupOutput) []finding {
	var findings []finding
	add := func(object, message string) {
		findings = append(findings, finding{profile: setupProfileName, object: object, message: message})
	}

	for _, obj := range objects {
		if obj.Kind == "Secret" && obj.Metadata.Name == setupSecretName {
			add("Secret/"+obj.Metadata.Name, "the chart renders a Secret under the name of the operator's existing one, which an install would overwrite")
		}
		if !workloadKinds[obj.Kind] {
			continue
		}
		name := obj.Kind + "/" + obj.Metadata.Name
		for _, c := range obj.Spec.Template.Spec.Containers {
			sawPassword := false
			for _, env := range c.Env {
				if env.ValueFrom != nil && env.ValueFrom.SecretKeyRef != nil && env.ValueFrom.SecretKeyRef.Name == setupSecretName {
					if !emitted.keys[env.ValueFrom.SecretKeyRef.Key] {
						add(name, fmt.Sprintf("container %s reads key %s from the operator's Secret, which setup's Secret does not carry; it would fail to start", c.Name, env.ValueFrom.SecretKeyRef.Key))
					}
				}
				if env.Name == "POSTGRES_PASSWORD" {
					sawPassword = true
				}
				if env.Name == "DB_DSN" && strings.HasSuffix(obj.Metadata.Name, "-controller") {
					switch {
					case !strings.Contains(env.Value, "$(POSTGRES_PASSWORD)"):
						add(name, "DB_DSN is not built from $(POSTGRES_PASSWORD), so it needs a key setup's Secret does not carry")
					case !sawPassword:
						add(name, "DB_DSN refers to $(POSTGRES_PASSWORD) before POSTGRES_PASSWORD is defined, so Kubernetes leaves it unexpanded")
					}
				}
			}
		}
		if obj.Kind == "Deployment" && strings.HasSuffix(obj.Metadata.Name, "-controller") {
			if got := obj.Spec.Template.Metadata.Annotations["checksum/secret"]; got != emitted.checksum {
				add(name, fmt.Sprintf("checksum/secret is %q, want setup's %q, so replacing the Secret would not restart the controller", got, emitted.checksum))
			}
		}
		if obj.Kind == "StatefulSet" && strings.HasSuffix(obj.Metadata.Name, "-postgres") {
			for _, claim := range obj.Spec.VolumeClaimTemplates {
				if got := claim.Metadata.Annotations[postgresStampAnnotation]; got != emitted.fingerprint {
					add(name, fmt.Sprintf("the database volume is stamped %q, want setup's %q, so a reinstall with a different password would not be refused", got, emitted.fingerprint))
				}
			}
		}
	}
	return findings
}

// checkFingerprintFormulaAgrees proves setup computes the database credential
// fingerprint the way the chart does, using a render of the chart's own
// inline-password path. The two formulas live in two languages, and this is
// the only thing that keeps them the same.
func checkFingerprintFormulaAgrees(profile string, objects []manifest, password string) []finding {
	want := setup.PostgresCredentialFingerprint("pleiades", "pleiades", password)
	var findings []finding
	for _, obj := range objects {
		if obj.Kind != "StatefulSet" || !strings.HasSuffix(obj.Metadata.Name, "-postgres") {
			continue
		}
		for _, claim := range obj.Spec.VolumeClaimTemplates {
			if got := claim.Metadata.Annotations[postgresStampAnnotation]; got != want {
				findings = append(findings, finding{
					profile: profile, object: obj.Kind + "/" + obj.Metadata.Name,
					message: fmt.Sprintf("the chart stamps %q but setup computes %q for the same credentials, so a release installed from setup's values would refuse its own data", got, want),
				})
			}
		}
	}
	return findings
}
