// Rendering the Helm Secret manifest and the values file that points at it.
package setup

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
)

// The two files the Helm target writes.
const (
	// HelmSecretFile holds the secrets, as a Kubernetes Secret manifest.
	HelmSecretFile = "pleiades-secret.yaml"

	// HelmValuesFile holds the chart values that point at that Secret. It
	// holds no secret, so it can live wherever the rest of a deployment's
	// values do.
	HelmValuesFile = "pleiades-values.yaml"
)

// DefaultSecretName is the Secret's name when the operator names none.
const DefaultSecretName = "pleiades-secrets"

// helmDatabaseName and helmDatabaseUser are the in-chart database's name and
// role, the chart's own defaults, pinned in the values file so the
// credential fingerprint below is computed over the same three values the
// chart will see.
const (
	helmDatabaseName = "pleiades"
	helmDatabaseUser = "pleiades"
)

// chartDefaultLivenessSeconds is the chart's default for
// runner.heartbeat.livenessStaleAfterSeconds. The chart refuses a budget
// longer than it, so a longer budget raises it too.
const chartDefaultLivenessSeconds = 1800

// HelmInput is what the Helm target renders.
type HelmInput struct {
	// SecretName names the Secret; empty means DefaultSecretName.
	SecretName string

	// Namespace is written into the Secret when set, so `kubectl create -f`
	// puts it in the release's namespace without a flag.
	Namespace string

	// MasterKey is the raw master key.
	MasterKey []byte

	// JWTSecret is the JWT secret, as text.
	JWTSecret string

	// PostgresPassword is the in-chart database's password.
	PostgresPassword string

	// Budget is the outage budget.
	Budget topology.OutageBudget
}

// dnsSubdomain and dnsLabel are Kubernetes' own rules for a Secret's name and
// a namespace. Both are checked before either appears in a file, and the
// files are marshaled rather than templated, so no value can add a field.
var (
	dnsSubdomain = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`)
	dnsLabel     = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
)

// secretManifest is a Kubernetes Secret.
type secretManifest struct {
	APIVersion string            `yaml:"apiVersion"`
	Kind       string            `yaml:"kind"`
	Metadata   secretMetadata    `yaml:"metadata"`
	Type       string            `yaml:"type"`
	StringData map[string]string `yaml:"stringData"`
}

// secretMetadata is a Secret's metadata.
type secretMetadata struct {
	Name      string            `yaml:"name"`
	Namespace string            `yaml:"namespace,omitempty"`
	Labels    map[string]string `yaml:"labels"`
}

// RenderHelm renders the Secret manifest and the values file.
//
// The Secret carries only what the chart reads from it when
// secrets.existingSecret is set and the in-chart database is on: the master
// key, the JWT secret, and the database password. The chart builds the
// database connection string from that password itself, so the Secret never
// has to repeat the release's service names.
//
// The values file carries two things that keep guards the chart would
// otherwise lose with an operator-managed Secret: a checksum of the Secret,
// so replacing it restarts the pods that read it, and the fingerprint of
// the database credentials, so a reinstall onto a volume created with a
// different password is refused rather than left failing to connect.
func RenderHelm(in HelmInput) (secret, values []byte, err error) {
	name := in.SecretName
	if name == "" {
		name = DefaultSecretName
	}
	if len(name) > 253 || !dnsSubdomain.MatchString(name) {
		return nil, nil, errors.New("setup: the Secret name must be a DNS subdomain: lowercase letters, digits, '-' and '.', at most 253 characters")
	}
	if in.Namespace != "" && (len(in.Namespace) > 63 || !dnsLabel.MatchString(in.Namespace)) {
		return nil, nil, errors.New("setup: the namespace must be a DNS label: lowercase letters, digits and '-', at most 63 characters")
	}
	if len(in.MasterKey) != 32 || in.JWTSecret == "" || in.PostgresPassword == "" || !in.Budget.Valid() {
		return nil, nil, errors.New("setup: the Helm output needs a 32-byte key, a JWT secret, a database password and a valid outage budget")
	}

	data := map[string]string{
		VarMasterKey:        crypto.EncodeKey(in.MasterKey),
		VarJWTSecret:        in.JWTSecret,
		"POSTGRES_PASSWORD": in.PostgresPassword,
	}
	manifest := secretManifest{
		APIVersion: "v1",
		Kind:       "Secret",
		Metadata: secretMetadata{
			Name:      name,
			Namespace: in.Namespace,
			Labels:    map[string]string{"app.kubernetes.io/managed-by": "pleiades-setup"},
		},
		Type:       "Opaque",
		StringData: data,
	}
	body, err := yaml.Marshal(manifest)
	if err != nil {
		return nil, nil, fmt.Errorf("setup: rendering the Secret: %w", err)
	}
	secret = []byte(secretHeader(name) + string(body))

	valuesDoc := map[string]any{
		"secrets": map[string]any{
			"existingSecret":         name,
			"existingSecretChecksum": secretChecksum(data),
		},
		"postgresql": map[string]any{
			"auth": map[string]any{
				"username":                  helmDatabaseUser,
				"database":                  helmDatabaseName,
				"existingSecretFingerprint": PostgresCredentialFingerprint(helmDatabaseUser, helmDatabaseName, in.PostgresPassword),
			},
		},
		"mesh": map[string]any{
			"maxOutageSeconds": int(time.Duration(in.Budget) / time.Second),
		},
	}
	if seconds := int(time.Duration(in.Budget) / time.Second); seconds > chartDefaultLivenessSeconds {
		valuesDoc["runner"] = map[string]any{"heartbeat": map[string]any{"livenessStaleAfterSeconds": seconds}}
	}
	vbody, err := yaml.Marshal(valuesDoc)
	if err != nil {
		return nil, nil, fmt.Errorf("setup: rendering the values: %w", err)
	}
	values = []byte(valuesHeader(name, in.Budget) + string(vbody))
	return secret, values, nil
}

// secretChecksum hashes the Secret's content in a fixed order, so the same
// content always gives the same value and any change gives a new one.
func secretChecksum(data map[string]string) string {
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	h := sha256.New()
	for _, k := range keys {
		fmt.Fprintf(h, "%s=%s\n", k, data[k])
	}
	return hex.EncodeToString(h.Sum(nil))
}

// PostgresCredentialFingerprint is the chart's own credential fingerprint,
// the-pleiades.postgres.credentialFingerprint: the first 16 hex characters
// of sha256("user:database:password"). The chart stamps it on the database
// volume at first install and refuses a later install whose value differs.
// tools/helm-lint's checkFingerprintFormulaAgrees renders the chart with helm
// on every run to prove the two agree.
func PostgresCredentialFingerprint(user, database, password string) string {
	sum := sha256.Sum256([]byte(user + ":" + database + ":" + password))
	return hex.EncodeToString(sum[:])[:16]
}

// secretHeader explains the Secret file to whoever opens it.
func secretHeader(name string) string {
	return strings.Join([]string{
		"# Written by pleiades-controller setup. This file holds the master encryption key,",
		"# and nothing else holds a copy: keep it somewhere safe, off this machine.",
		"#",
		"# Create the Secret with `kubectl create -f` rather than `apply`: create refuses to",
		"# replace a Secret named " + name + " that already exists, and replacing the key",
		"# of a release that stores data makes that data permanently unreadable.",
		"",
	}, "\n")
}

// valuesHeader explains the values file to whoever opens it.
func valuesHeader(name string, budget topology.OutageBudget) string {
	lines := []string{
		"# Written by pleiades-controller setup. This file holds no secret. Pass it to",
		"# `helm install -f` after creating the Secret " + name + ".",
		"#",
		"# existingSecretChecksum restarts the pods when the Secret changes, and",
		"# existingSecretFingerprint lets the chart refuse a reinstall onto a database",
		"# volume that was created with a different password.",
	}
	if int(time.Duration(budget)/time.Second) > chartDefaultLivenessSeconds {
		lines = append(lines,
			"#",
			"# The outage budget is longer than the runner's default liveness window, so the",
			"# window is raised to match: a runner that stops beating is noticed only after",
			"# that long.")
	}
	return strings.Join(append(lines, ""), "\n")
}
