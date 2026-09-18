// Tests that hold docker-compose.yml to what the setup command depends on.
package setup_test

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/Subject-Void-LLC/the-pleiades/internal/setup"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
)

// These tests hold docker-compose.yml to what this command depends on. The
// file cannot import a Go constant, so the names it carries would drift
// from setup's without a test reading both.

// composeService is the part of a compose service these tests read.
type composeService struct {
	Profiles    []string          `yaml:"profiles"`
	Image       string            `yaml:"image"`
	Build       map[string]any    `yaml:"build"`
	Environment envEntries        `yaml:"environment"`
	Logging     map[string]string `yaml:"logging"`
	Entrypoint  []string          `yaml:"entrypoint"`
	Ports       []string          `yaml:"ports"`
}

// envEntries is a service's environment in either spelling compose accepts:
// a list of NAME=value strings, or a map.
type envEntries []string

// UnmarshalYAML accepts both spellings.
func (e *envEntries) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.SequenceNode {
		var list []string
		if err := node.Decode(&list); err != nil {
			return err
		}
		*e = list
		return nil
	}
	var m map[string]string
	if err := node.Decode(&m); err != nil {
		return err
	}
	for k, v := range m {
		*e = append(*e, k+"="+v)
	}
	return nil
}

// readComposeFile returns the shipped compose file's text and services, with
// YAML anchors resolved.
func readComposeFile(t *testing.T) (string, map[string]composeService) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(testsupport.RepoRoot(t), "docker-compose.yml"))
	if err != nil {
		t.Fatalf("reading docker-compose.yml: %v", err)
	}
	var doc struct {
		Services map[string]composeService `yaml:"services"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parsing docker-compose.yml: %v", err)
	}
	return string(raw), doc.Services
}

// envMap turns a list-form environment into a map.
func envMap(env []string) map[string]string {
	m := map[string]string{}
	for _, kv := range env {
		name, value, _ := strings.Cut(kv, "=")
		m[name] = value
	}
	return m
}

// TestComposeFileHoldsNoUsableKey is the phase's requirement in one
// assertion: the shipped file must not contain a usable encryption key, in
// any line, comments included. A comment calling a key a throwaway default
// is exactly how the last one shipped.
func TestComposeFileHoldsNoUsableKey(t *testing.T) {
	text, _ := readComposeFile(t)
	candidate := regexp.MustCompile(`[A-Za-z0-9+/]{43}=`)
	for _, token := range candidate.FindAllString(text, -1) {
		if raw, err := base64.StdEncoding.DecodeString(token); err == nil && len(raw) == 32 {
			t.Errorf("docker-compose.yml contains %q, which is base64 of 32 bytes: a usable MASTER_ENCRYPTION_KEY", token)
		}
	}
}

// TestComposeFileReadsEverySetupVariableFromDotEnv proves every variable
// setup writes or reads reaches the controller as ${NAME:-...}
// interpolation, so .env is where its value comes from and the file never
// carries one. The budget reaches the runner too, since the two services
// must agree on it.
func TestComposeFileReadsEverySetupVariableFromDotEnv(t *testing.T) {
	_, services := readComposeFile(t)
	controller := envMap(services["controller"].Environment)
	for _, name := range setup.ComposeVariables() {
		value, ok := controller[name]
		if !ok {
			t.Errorf("the controller service does not set %s, so a value setup writes to .env never reaches it", name)
			continue
		}
		if !strings.HasPrefix(value, "${"+name+":-") || !strings.HasSuffix(value, "}") {
			t.Errorf("the controller's %s is %q, want it read from .env as ${%s:-...}", name, value, name)
		}
	}
	if controller[setup.VarMasterKey] != "${MASTER_ENCRYPTION_KEY:-}" || controller[setup.VarJWTSecret] != "${JWT_SECRET:-}" {
		t.Error("the key or the JWT secret has a default in docker-compose.yml; a default is a published secret")
	}
	if runner := envMap(services["runner"].Environment); runner[setup.VarMaxOutage] != controller[setup.VarMaxOutage] {
		t.Errorf("the runner's %s is %q and the controller's is %q; they must agree", setup.VarMaxOutage, runner[setup.VarMaxOutage], controller[setup.VarMaxOutage])
	}
}

// TestComposeSetupServiceIsConfinedToWhatItNeeds pins the setup service's
// shape: started only on request, built rather than pulled, keeping no log
// of the key it shows, reading the controller's own database and holding no
// secret of its own.
func TestComposeSetupServiceIsConfinedToWhatItNeeds(t *testing.T) {
	_, services := readComposeFile(t)
	svc, ok := services["setup"]
	if !ok {
		t.Fatal("docker-compose.yml has no setup service")
	}
	if len(svc.Profiles) != 1 || svc.Profiles[0] != "setup" {
		t.Errorf("profiles = %v; without the setup profile, `docker compose up` would run setup on every start", svc.Profiles)
	}
	if len(svc.Build) == 0 {
		t.Error("the setup service does not build from the controller's Dockerfile, so `docker compose run` would pull an image name this project does not own")
	}
	if svc.Logging["driver"] != "none" {
		t.Errorf("the setup service's log driver is %q; at a terminal setup prints the key, and a log driver keeps it", svc.Logging["driver"])
	}
	if strings.Join(svc.Entrypoint, " ") != "/app/controller setup --target compose --dir /setup" {
		t.Errorf("entrypoint = %v", svc.Entrypoint)
	}
	env := envMap(svc.Environment)
	for _, name := range setup.ComposeVariables() {
		if _, set := env[name]; set {
			t.Errorf("the setup service's environment sets %s; setup reads the key from .env, and a copy here could disagree with it", name)
		}
	}
	controllerDSN := envMap(services["controller"].Environment)["DB_DSN"]
	if env["DB_DSN"] == "" || env["DB_DSN"] != controllerDSN {
		t.Errorf("setup counts %q and the controller opens %q; setup must count the database the controller uses", env["DB_DSN"], controllerDSN)
	}
}

// TestComposePublishesTheDatabaseAndBrokerOnLoopbackOnly pins the two ports
// that were open on every interface. The database's password is in this
// file, and the broker has no authorization while its dispatch messages
// carry job credentials, so either one published beyond this machine is
// readable by anything that can reach it.
func TestComposePublishesTheDatabaseAndBrokerOnLoopbackOnly(t *testing.T) {
	_, services := readComposeFile(t)
	for _, name := range []string{"postgres", "nats"} {
		ports := services[name].Ports
		if len(ports) == 0 {
			t.Errorf("%s publishes no port; this test would pass by checking nothing", name)
		}
		for _, p := range ports {
			if !strings.HasPrefix(p, "127.0.0.1:") {
				t.Errorf("%s publishes %q on every interface; bind it to 127.0.0.1", name, p)
			}
		}
	}
}
