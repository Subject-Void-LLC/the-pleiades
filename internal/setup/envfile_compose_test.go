//go:build integration

// The env file reader compared with the real docker compose on a corpus of
// tricky files.
package setup_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
	"github.com/Subject-Void-LLC/the-pleiades/internal/setup"
)

// TestParseEnvFile_AgreesWithDockerCompose is the RULE 0 half of the env file
// parser's proof. The unit tests pin what this parser does; this one asks the
// real docker compose what it does with the same bytes, because the only
// reading of a .env that matters is compose's.
//
// Every corpus entry is written to a file and resolved by `docker compose
// config` against a compose file that interpolates each owned variable, the
// same ${VAR:-} form docker-compose.yml uses. Where this parser accepts a
// file, every owned variable must resolve in compose to exactly the value
// this parser read. Where it refuses one, nothing is asserted about compose:
// refusing is always allowed, and is what the parser does whenever compose
// has a rule it would otherwise have to copy.
//
// It runs no container and needs no daemon, only the docker CLI.
func TestParseEnvFile_AgreesWithDockerCompose(t *testing.T) {
	docker, err := exec.LookPath("docker")
	if err != nil {
		t.Skip("docker is not installed")
	}

	key := crypto.EncodeKey([]byte(strings.Repeat("c", 32)))
	jwt := crypto.EncodeKey([]byte(strings.Repeat("w", 32)))
	corpus := []string{
		"",
		"MASTER_ENCRYPTION_KEY=" + key + "\n",
		"MASTER_ENCRYPTION_KEY=" + key,
		"MASTER_ENCRYPTION_KEY=" + key + "\r\nJWT_SECRET=" + jwt + "\r\n",
		"# comment\n\nMASTER_ENCRYPTION_KEY=" + key + "\nOTHER = spaced # trailing\n",
		"MASTER_ENCRYPTION_KEY=\nJWT_SECRET=" + jwt + "\n",
		"OTHER='single'\nMASTER_ENCRYPTION_KEY=" + key + "\n",
		"OTHER=\"double \\\" escaped\"\nJWT_SECRET=" + jwt + "\n",
		"PLEIADES_MAX_OUTAGE=20m\nMASTER_ENCRYPTION_KEY_VERSION=v2\nROTATE_ENCRYPTION_KEYS=true\n",
		"MASTER_ENCRYPTION_KEY_PREVIOUS=" + key + "\nMASTER_ENCRYPTION_KEY_PREVIOUS_VERSION=v1\n",
		// Shapes the parser refuses; compose's reading of each is logged so
		// the reason for refusing it stays visible.
		"MASTER_ENCRYPTION_KEY=\"" + key + "\"\n",
		"MASTER_ENCRYPTION_KEY: " + key + "\n",
		"export MASTER_ENCRYPTION_KEY=" + key + "\n",
		"MASTER_ENCRYPTION_KEY=" + key + " # note\n",
		"JWT_SECRET=" + jwt + "$HOME\n",
		"NOTES=\"one\nMASTER_ENCRYPTION_KEY=" + key + "\ntwo\"\n",
		"MASTER_ENCRYPTION_KEY=" + key + "\nMASTER_ENCRYPTION_KEY=" + crypto.EncodeKey([]byte(strings.Repeat("d", 32))) + "\n",
	}

	dir := t.TempDir()
	composeFile := filepath.Join(dir, "compose.yml")
	var env strings.Builder
	for _, name := range setup.ComposeVariables() {
		env.WriteString("      " + name + ": ${" + name + ":-}\n")
	}
	if err := os.WriteFile(composeFile, []byte("services:\n  probe:\n    image: scratch\n    environment:\n"+env.String()), 0o600); err != nil {
		t.Fatalf("writing the compose file: %v", err)
	}

	for i, text := range corpus {
		envFile := filepath.Join(dir, "corpus.env")
		if err := os.WriteFile(envFile, []byte(text), 0o600); err != nil {
			t.Fatalf("writing corpus entry %d: %v", i, err)
		}
		resolved := composeResolve(t, docker, dir, composeFile, envFile)

		parsed, err := setup.ParseEnvFile([]byte(text))
		if err != nil {
			t.Logf("corpus %d refused by setup (%v); compose resolves MASTER_ENCRYPTION_KEY to %d characters", i, err, len(resolved[setup.VarMasterKey]))
			continue
		}
		for _, name := range setup.ComposeVariables() {
			ours, _ := parsed.Get(name)
			if resolved[name] != ours {
				t.Errorf("corpus %d: compose resolves %s to a %d-character value, setup read a %d-character value; they must agree wherever setup accepts a file",
					i, name, len(resolved[name]), len(ours))
			}
		}
	}
}

// composeResolve runs `docker compose config` against envFile and returns the
// probe service's resolved environment. The child environment carries none
// of the owned variables and no COMPOSE_ setting, because either would
// outrank the file under test.
func composeResolve(t *testing.T, docker, dir, composeFile, envFile string) map[string]string {
	t.Helper()
	cmd := exec.Command(docker, "compose", "--project-directory", dir, "-f", composeFile, "--env-file", envFile, "config", "--format", "json")
	cmd.Env = scrubbedEnv()
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("docker compose config: %v", err)
	}
	var cfg struct {
		Services map[string]struct {
			Environment map[string]*string `json:"environment"`
		} `json:"services"`
	}
	if err := json.Unmarshal(out, &cfg); err != nil {
		t.Fatalf("decoding docker compose config output: %v", err)
	}
	resolved := map[string]string{}
	for name, v := range cfg.Services["probe"].Environment {
		if v != nil {
			resolved[name] = *v
		}
	}
	return resolved
}

// scrubbedEnv is this process's environment without any variable this
// command owns and without any COMPOSE_ setting.
func scrubbedEnv() []string {
	owned := map[string]bool{}
	for _, n := range setup.ComposeVariables() {
		owned[n] = true
	}
	var out []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if owned[name] || strings.HasPrefix(name, "COMPOSE_") {
			continue
		}
		out = append(out, kv)
	}
	return out
}
