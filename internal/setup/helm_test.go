// Tests for the Helm output, rendered and run.
package setup_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
	"github.com/Subject-Void-LLC/the-pleiades/internal/setup"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
)

// helmInput is a valid input for RenderHelm.
func helmInput() setup.HelmInput {
	return setup.HelmInput{
		Namespace:        "pleiades",
		MasterKey:        []byte(strings.Repeat("h", 32)),
		JWTSecret:        crypto.EncodeKey([]byte(strings.Repeat("j", 32))),
		PostgresPassword: strings.Repeat("ab", 32),
		Budget:           topology.DefaultOutageBudget,
	}
}

// renderedSecret and renderedValues are the two files, decoded.
type renderedSecret struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		Name      string `yaml:"name"`
		Namespace string `yaml:"namespace"`
	} `yaml:"metadata"`
	StringData map[string]string `yaml:"stringData"`
}

type renderedValues struct {
	Secrets struct {
		ExistingSecret         string `yaml:"existingSecret"`
		ExistingSecretChecksum string `yaml:"existingSecretChecksum"`
	} `yaml:"secrets"`
	Postgresql struct {
		Auth struct {
			Username                  string `yaml:"username"`
			Database                  string `yaml:"database"`
			ExistingSecretFingerprint string `yaml:"existingSecretFingerprint"`
			Password                  string `yaml:"password"`
		} `yaml:"auth"`
	} `yaml:"postgresql"`
	Mesh struct {
		MaxOutageSeconds int `yaml:"maxOutageSeconds"`
	} `yaml:"mesh"`
	Runner struct {
		Heartbeat struct {
			LivenessStaleAfterSeconds int `yaml:"livenessStaleAfterSeconds"`
		} `yaml:"heartbeat"`
	} `yaml:"runner"`
}

// render renders in and decodes both files.
func render(t *testing.T, in setup.HelmInput) (renderedSecret, renderedValues, []byte, []byte) {
	t.Helper()
	secretYAML, valuesYAML, err := setup.RenderHelm(in)
	if err != nil {
		t.Fatalf("RenderHelm() error = %v", err)
	}
	var s renderedSecret
	var v renderedValues
	if err := yaml.Unmarshal(secretYAML, &s); err != nil {
		t.Fatalf("the Secret does not parse: %v", err)
	}
	if err := yaml.Unmarshal(valuesYAML, &v); err != nil {
		t.Fatalf("the values file does not parse: %v", err)
	}
	return s, v, secretYAML, valuesYAML
}

// TestRenderHelm_TheSecretHoldsExactlyWhatTheChartReads pins the Secret's
// shape: emitting the wrong shape for the target is the failure mode the
// phase names.
func TestRenderHelm_TheSecretHoldsExactlyWhatTheChartReads(t *testing.T) {
	in := helmInput()
	s, v, _, valuesYAML := render(t, in)

	if s.APIVersion != "v1" || s.Kind != "Secret" || s.Metadata.Name != setup.DefaultSecretName || s.Metadata.Namespace != "pleiades" {
		t.Fatalf("Secret header = %+v", s)
	}
	var keys []string
	for k := range s.StringData {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if strings.Join(keys, ",") != "JWT_SECRET,MASTER_ENCRYPTION_KEY,POSTGRES_PASSWORD" {
		t.Fatalf("the Secret holds %v; the chart reads exactly the key, the JWT secret and the database password from it", keys)
	}
	if s.StringData["MASTER_ENCRYPTION_KEY"] != crypto.EncodeKey(in.MasterKey) {
		t.Fatal("the Secret's key is not the key given")
	}

	if v.Secrets.ExistingSecret != setup.DefaultSecretName {
		t.Fatalf("values point at %q, want the Secret", v.Secrets.ExistingSecret)
	}
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(v.Secrets.ExistingSecretChecksum) {
		t.Fatalf("existingSecretChecksum = %q, want 64 hex characters", v.Secrets.ExistingSecretChecksum)
	}
	if v.Postgresql.Auth.ExistingSecretFingerprint != setup.PostgresCredentialFingerprint("pleiades", "pleiades", in.PostgresPassword) {
		t.Fatalf("existingSecretFingerprint = %q", v.Postgresql.Auth.ExistingSecretFingerprint)
	}
	if v.Mesh.MaxOutageSeconds != 1800 || v.Runner.Heartbeat.LivenessStaleAfterSeconds != 0 {
		t.Fatalf("mesh/runner = %d/%d; a default budget sets 1800 and leaves the liveness window alone", v.Mesh.MaxOutageSeconds, v.Runner.Heartbeat.LivenessStaleAfterSeconds)
	}
	for _, secret := range []string{crypto.EncodeKey(in.MasterKey), in.JWTSecret, in.PostgresPassword} {
		if bytes.Contains(valuesYAML, []byte(secret)) {
			t.Fatal("the values file, which holds no secret, contains one")
		}
	}
	if v.Postgresql.Auth.Password != "" {
		t.Fatal("the values file sets postgresql.auth.password")
	}
}

// TestRenderHelm_ALongerBudgetRaisesTheLivenessWindow proves a budget the
// chart would otherwise refuse comes with the liveness window it needs.
func TestRenderHelm_ALongerBudgetRaisesTheLivenessWindow(t *testing.T) {
	in := helmInput()
	in.Budget = topology.OutageBudget(2 * time.Hour)
	_, v, _, _ := render(t, in)
	if v.Mesh.MaxOutageSeconds != 7200 || v.Runner.Heartbeat.LivenessStaleAfterSeconds != 7200 {
		t.Fatalf("mesh/runner = %d/%d, want 7200/7200", v.Mesh.MaxOutageSeconds, v.Runner.Heartbeat.LivenessStaleAfterSeconds)
	}
}

// TestRenderHelm_TheChecksumFollowsTheContent proves the checksum that rolls
// the pods changes with any secret and with nothing else.
func TestRenderHelm_TheChecksumFollowsTheContent(t *testing.T) {
	_, base, _, _ := render(t, helmInput())
	_, same, _, _ := render(t, helmInput())
	if base.Secrets.ExistingSecretChecksum != same.Secrets.ExistingSecretChecksum {
		t.Fatal("the same content gave two checksums")
	}
	changed := helmInput()
	changed.JWTSecret = crypto.EncodeKey([]byte(strings.Repeat("x", 32)))
	_, other, _, _ := render(t, changed)
	if other.Secrets.ExistingSecretChecksum == base.Secrets.ExistingSecretChecksum {
		t.Fatal("a new JWT secret did not change the checksum, so the pods would keep the old one")
	}
}

// TestRenderHelm_RefusesNamesKubernetesWouldNot proves an invalid name is
// refused before it reaches a file.
func TestRenderHelm_RefusesNamesKubernetesWouldNot(t *testing.T) {
	for _, name := range []string{"Upper", "has space", "a;b", "-leading", strings.Repeat("a", 254), "x\nkind: Pod"} {
		in := helmInput()
		in.SecretName = name
		if _, _, err := setup.RenderHelm(in); err == nil {
			t.Errorf("RenderHelm accepted the Secret name %q", name)
		}
	}
	for _, ns := range []string{"has.dot", "Upper", strings.Repeat("n", 64)} {
		in := helmInput()
		in.Namespace = ns
		if _, _, err := setup.RenderHelm(in); err == nil {
			t.Errorf("RenderHelm accepted the namespace %q", ns)
		}
	}
}

// TestRun_HelmWritesNewFilesOnlyAndRefusesARerun covers the Helm target end
// to end without a terminal.
func TestRun_HelmWritesNewFilesOnlyAndRefusesARerun(t *testing.T) {
	dir := t.TempDir()
	opts := setup.Options{Target: setup.TargetHelm, Dir: dir, Namespace: "pleiades", MaxOutage: "20m"}
	var out bytes.Buffer
	result, err := setup.Run(context.Background(), opts, nil, &out)
	if err != nil {
		t.Fatalf("Run(helm) error = %v", err)
	}
	for _, name := range []string{setup.HelmSecretFile, setup.HelmValuesFile} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("%s: %v, mode %v; want it written at 0600", name, err, info)
		}
	}
	secret, _ := os.ReadFile(filepath.Join(dir, setup.HelmSecretFile))
	if !bytes.Contains(secret, []byte(crypto.EncodeKey(result.Key))) {
		t.Fatal("the Secret does not hold the key Run returned")
	}
	if strings.Contains(out.String(), crypto.EncodeKey(result.Key)) {
		t.Fatal("the Helm target printed the key")
	}
	for _, want := range []string{"kubectl create --namespace pleiades -f", "counted nothing"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output does not say %q:\n%s", want, out.String())
		}
	}

	_, err = setup.Run(context.Background(), opts, nil, &out)
	if msg := refusalOf(t, err); !strings.Contains(msg, filepath.Join(dir, setup.HelmSecretFile)) {
		t.Fatalf("the Helm re-run refusal does not name the file:\n%s", msg)
	}

	opts.Dir = t.TempDir()
	opts.NewJWTSecret = true
	if _, err := setup.Run(context.Background(), opts, nil, &out); err == nil {
		t.Fatal("the Helm target accepted a compose-only flag")
	}
}
