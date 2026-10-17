// Release gate for container.run's secrets and engine pin: through the
// real binary, against a real Docker engine behind a real sshd, a run that
// creates a container keeps its environment value off every output the
// platform writes, a failed run does too, and the run acts on the engine
// onboarding pinned.
package main_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
)

// dockerRunRunbook writes a runbook running container.run on host,
// with one environment value, and returns its path.
func dockerRunRunbook(t *testing.T, dir, host, name, image, secret string) string {
	t.Helper()
	return dockerRunRunbookCmd(t, dir, host, name, image, secret, "sleep")
}

// dockerRunRunbookCmd is dockerRunRunbook with the container's program named.
func dockerRunRunbookCmd(t *testing.T, dir, host, name, image, secret, program string) string {
	t.Helper()
	path := filepath.Join(dir, "runbooks", "run-"+name+".yaml")
	body := "id: run-" + name + "\n" +
		"hosts: " + host + "\n" +
		"tasks:\n" +
		"  - name: run a container with a secret in its environment\n" +
		"    container.run:\n" +
		"      name: " + name + "\n" +
		"      image: " + image + "\n" +
		// The fixture's engine holds the image, loaded from an archive, so
		// it has no registry digest to pin and is run as it is.
		"      pull: never\n" +
		"      command:\n" +
		"        - " + program + "\n" +
		"        - \"300\"\n" +
		"      env:\n" +
		"        GATE_SECRET: " + secret + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestCLI_DockerRunKeepsItsSecretOffEveryOutput plants a random environment
// value and asserts it reaches the container and nothing the platform wrote:
// the run's report, its error on a failed run, and every file under the
// project's state directory (the journal included). The value travels to
// the engine in the create request's body, never on a command line.
func TestCLI_DockerRunKeepsItsSecretOffEveryOutput(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a real Docker engine behind a real sshd")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	d := testsupport.StartDockerHost(t, testsupport.EngineHostOptions{})
	// Starting one container copies the gate image into the fixture's
	// engine, so the run below never pulls.
	d.StartContainer(t, containerGateImage, "")

	raw := make([]byte, 12)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	secret := "gatesecret" + hex.EncodeToString(raw)

	dir := t.TempDir()
	if out, err := runPleiades(t, dir, "init"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	knownHosts, err := d.KnownHosts(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"PLEIADES_KNOWN_HOSTS": knownHosts}
	if out, err := runPleiades(t, dir, "add-host", "box", "--type", "container_host", "--set", "host="+d.Host, "--set", "port="+strconv.Itoa(d.Port)); err != nil {
		t.Fatalf("add-host: %v\n%s", err, out)
	}
	if out, err := runPleiadesWithStdin(t, dir, testsupport.DockerHostPassword, "add-credential", "box", "--username", testsupport.DockerHostUser, "--password-stdin"); err != nil {
		t.Fatalf("add-credential: %v\n%s", err, out)
	}
	onboardJSON(t, dir, env, "box")

	// A run that creates the container.
	name := gateContainerName(t)
	out, err := runPleiadesWithEnv(t, dir, env, "run", dockerRunRunbook(t, dir, "box", name, containerGateImage, secret), "--json")
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	if strings.Contains(out, secret) {
		t.Errorf("the run's report carries the environment value:\n%s", out)
	}
	// The value did reach the container, so its absence above is the
	// platform keeping it, not a run that never sent it.
	inside, err := d.RootExec(ctx, "docker", "inspect", "--format={{json .Config.Env}}", "--", name)
	if err != nil || !strings.Contains(inside, secret) {
		t.Fatalf("the container's environment = %q, %v; want the value delivered", inside, err)
	}

	// A run that fails in the engine itself, with the same value in its
	// environment: the image is present, so nothing is pulled, and the
	// container's program does not exist, so the engine creates the
	// container, fails to start it, and the run removes it again.
	failed, err := runPleiadesWithEnv(t, dir, env, "run", dockerRunRunbookCmd(t, dir, "box", gateContainerName(t), containerGateImage, secret, "/pleiades-no-such-program"), "--json")
	if err == nil {
		t.Fatalf("a run of an absent image succeeded:\n%s", failed)
	}
	if strings.Contains(failed, secret) {
		t.Errorf("the failed run's report carries the environment value:\n%s", failed)
	}

	// Nothing the platform wrote to its state directory holds it either.
	state := filepath.Join(dir, ".pleiades")
	walked := 0
	err = filepath.WalkDir(state, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		walked++
		data, err := os.ReadFile(path) // #nosec G304 -- a path under this test's own directory
		if err != nil {
			return err
		}
		if strings.Contains(string(data), secret) {
			t.Errorf("%s holds the environment value", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if walked == 0 {
		t.Fatalf("%s holds no files, so the check proves nothing", state)
	}
}
