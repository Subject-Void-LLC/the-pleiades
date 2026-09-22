//go:build integration

// Phase 83's Release Gate, Kubernetes half: the Secret and values file
// `controller setup --target helm` writes, installed into a real cluster
// exactly as the production guide says, with no secret on any command line.
package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
	"github.com/Subject-Void-LLC/the-pleiades/internal/setup"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
)

// The third release this gate installs. Its own namespace and a
// fullnameOverride, so its object names are known and it cannot touch the
// other two.
const (
	setupReleaseName = "pleiades-setup"
	setupNamespace   = "pleiades-setup"
	setupFullname    = "pleiades-setup"
	setupSecretName  = "pleiades-setup-secrets"
	setupAdminEmail  = "setup-gate-admin@example.test"
)

// runSetupForHelm runs the built controller's setup command for the Helm
// target into a new directory and returns it.
func runSetupForHelm(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cmd := exec.Command(controllerBinPath, "setup", "--target", "helm", "--dir", dir,
		"--namespace", setupNamespace, "--secret-name", setupSecretName,
		"--non-interactive", "--max-outage", "2h")
	cmd.Env = append(scrubbedPackagingEnv(), "OTEL_TRACES_EXPORTER=none")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("setup --target helm failed: %v\n%s", err, out)
	}
	secret, err := os.ReadFile(filepath.Join(dir, setup.HelmSecretFile))
	if err != nil {
		t.Fatalf("setup did not write the Secret: %v", err)
	}
	var manifest struct {
		StringData map[string]string `yaml:"stringData"`
	}
	if err := yaml.Unmarshal(secret, &manifest); err != nil {
		t.Fatalf("setup's Secret does not parse: %v", err)
	}
	for name, value := range manifest.StringData {
		if strings.Contains(string(out), value) {
			t.Fatalf("setup printed the Secret's %s", name)
		}
	}
	return dir
}

// helmInstallFromSetup installs the release from a setup directory, with no
// secret on the command line: the only --set values are the image pull
// policy for side-loaded images and the names this gate pins.
func helmInstallFromSetup(t *testing.T, root, kubeconfig, dir string, wait bool) (string, error) {
	t.Helper()
	args := []string{"install", setupReleaseName, filepath.Join(root, "helm", "the-pleiades"),
		"--namespace", setupNamespace,
		"-f", filepath.Join(dir, setup.HelmValuesFile),
		"--set", "fullnameOverride=" + setupFullname,
		"--set", "controller.image.pullPolicy=Never",
		"--set", "runner.image.pullPolicy=Never",
		"--set", "runner.replicaCount=1"}
	if wait {
		args = append(args, "--wait", "--timeout", kindInstallTimeout.String())
	}
	return runPackagingTool(t, root, kubeEnv(kubeconfig), "", "helm", args...)
}

// logControllerRestarts logs how often pod's controller container has
// restarted and, when it has, why the last one ended and what it last said,
// which kubectl logs alone would never show.
func logControllerRestarts(t *testing.T, root, kubeconfig, pod string) {
	t.Helper()
	status := mustRunPackagingTool(t, root, kubeEnv(kubeconfig), "",
		"kubectl", "-n", setupNamespace, "get", "pod", pod,
		"-o", `jsonpath={.status.containerStatuses[?(@.name=="controller")].restartCount} {.status.containerStatuses[?(@.name=="controller")].lastState.terminated.reason} {.status.containerStatuses[?(@.name=="controller")].lastState.terminated.exitCode}`)
	fields := strings.Fields(status)
	if len(fields) == 0 || fields[0] == "0" {
		t.Logf("the controller container has not restarted")
		return
	}
	previous, _ := runPackagingTool(t, root, kubeEnv(kubeconfig), "",
		"kubectl", "-n", setupNamespace, "logs", pod, "-c", "controller", "--previous", "--tail", "15")
	t.Logf("the controller container restarted %s time(s); the last one ended %s:\n%s",
		fields[0], strings.Join(fields[1:], " exit "), previous)
}

// controllerPodName returns the name of the release's one controller pod.
func controllerPodName(t *testing.T, root, kubeconfig string) string {
	t.Helper()
	return strings.TrimSpace(mustRunPackagingTool(t, root, kubeEnv(kubeconfig), "",
		"kubectl", "-n", setupNamespace, "get", "pods",
		"-l", "app.kubernetes.io/instance="+setupReleaseName+",app.kubernetes.io/component=controller",
		"--field-selector", "status.phase=Running",
		"-o", "jsonpath={.items[0].metadata.name}"))
}

// assertInstallFromSetupOutput is the gate.
//
// It proves four things in a real cluster. The Secret setup emits is the
// shape the chart reads: the release installs and the controller serves.
// The first administrator can be created and the key is recorded as first
// used. A new Secret with a new checksum restarts the controller, which a
// Secret the chart did not create used not to do. And a reinstall onto the
// retained database with a new setup's password is refused naming the
// claim, which a Secret the chart did not create used to skip.
func assertInstallFromSetupOutput(t *testing.T, root, kubeconfig string) {
	t.Helper()
	cleanup := func() {
		_, _ = runPackagingTool(t, root, kubeEnv(kubeconfig), "", "helm", "uninstall", setupReleaseName, "--namespace", setupNamespace)
		_, _ = runPackagingTool(t, root, kubeEnv(kubeconfig), "", "kubectl", "delete", "namespace", setupNamespace, "--ignore-not-found")
	}
	t.Cleanup(cleanup)

	dir := runSetupForHelm(t)
	mustRunPackagingTool(t, root, kubeEnv(kubeconfig), "", "kubectl", "create", "namespace", setupNamespace)
	mustRunPackagingTool(t, root, kubeEnv(kubeconfig), "", "kubectl", "create", "-f", filepath.Join(dir, setup.HelmSecretFile))
	if out, err := helmInstallFromSetup(t, root, kubeconfig, dir, true); err != nil {
		events, _ := runPackagingTool(t, root, kubeEnv(kubeconfig), "", "kubectl", "-n", setupNamespace, "get", "events", "--sort-by=.lastTimestamp")
		t.Fatalf("installing from setup's output failed: %v\n%s\n--- events ---\n%s", err, out, events)
	}

	deployment := "deploy/" + setupFullname + "-controller"
	mustRunPackagingTool(t, root, kubeEnv(kubeconfig), "", "kubectl", "-n", setupNamespace, "exec", deployment, "--", "/app/controller", "healthcheck")
	out := mustRunPackagingTool(t, root, kubeEnv(kubeconfig), kindAdminPassword+"\n",
		"kubectl", "exec", "-i", "-n", setupNamespace, deployment, "--",
		"/app/controller", "bootstrap-admin", "--email", setupAdminEmail, "--password-stdin")
	if !strings.Contains(out, "is ready") {
		t.Fatalf("bootstrap-admin on the release installed from setup's output did not succeed:\n%s", out)
	}
	// The registry row, not the log line reporting it. kubectl logs shows
	// only the running container, so a controller that restarted after
	// recording the key (the one that recorded it can still die later in
	// startup, waiting on the broker) had written the line somewhere this
	// check could no longer see (FAILURE_PATTERNS.md #295).
	pod := controllerPodName(t, root, kubeconfig)
	logControllerRestarts(t, root, kubeconfig, pod)
	origins := mustRunPackagingTool(t, root, kubeEnv(kubeconfig), "",
		"kubectl", "-n", setupNamespace, "exec", setupFullname+"-postgres-0", "-c", "postgres", "--",
		"psql", "-U", "pleiades", "-d", "pleiades", "-tAc", "SELECT origin FROM encryption_keys")
	if got := strings.Fields(origins); len(got) != 1 || got[0] != "first_use" {
		t.Errorf("the key registry holds origins %q; want exactly one key, recorded by the controller as first used", got)
	}

	assertANewSecretChecksumRestartsTheController(t, root, kubeconfig, dir)
	assertASetupReinstallWithANewPasswordIsRefused(t, root, kubeconfig)
	cleanup()
}

// assertANewSecretChecksumRestartsTheController replaces the JWT secret, the
// reversible change an operator makes, and requires the controller pod to be
// replaced by the upgrade that carries the new checksum.
func assertANewSecretChecksumRestartsTheController(t *testing.T, root, kubeconfig, dir string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, setup.HelmSecretFile))
	if err != nil {
		t.Fatalf("reading setup's Secret: %v", err)
	}
	var manifest struct {
		StringData map[string]string `yaml:"stringData"`
	}
	if err := yaml.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("parsing setup's Secret: %v", err)
	}
	key, err := crypto.DecodeKey(manifest.StringData[setup.VarMasterKey], "the Secret")
	if err != nil {
		t.Fatalf("decoding the Secret's key: %v", err)
	}
	newJWT, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	secret, values, err := setup.RenderHelm(setup.HelmInput{
		SecretName: setupSecretName, Namespace: setupNamespace,
		MasterKey: key, JWTSecret: crypto.EncodeKey(newJWT),
		PostgresPassword: manifest.StringData["POSTGRES_PASSWORD"],
		Budget:           topology.OutageBudget(2 * time.Hour),
	})
	if err != nil {
		t.Fatalf("RenderHelm() error = %v", err)
	}
	next := t.TempDir()
	for name, data := range map[string][]byte{setup.HelmSecretFile: secret, setup.HelmValuesFile: values} {
		if err := os.WriteFile(filepath.Join(next, name), data, 0o600); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}

	before := controllerPodName(t, root, kubeconfig)
	mustRunPackagingTool(t, root, kubeEnv(kubeconfig), "", "kubectl", "apply", "-f", filepath.Join(next, setup.HelmSecretFile))
	mustRunPackagingTool(t, root, kubeEnv(kubeconfig), "", "helm", "upgrade", setupReleaseName,
		filepath.Join(root, "helm", "the-pleiades"), "--namespace", setupNamespace,
		"-f", filepath.Join(next, setup.HelmValuesFile),
		"--set", "fullnameOverride="+setupFullname,
		"--set", "controller.image.pullPolicy=Never",
		"--set", "runner.image.pullPolicy=Never",
		"--set", "runner.replicaCount=1",
		"--wait", "--timeout", kindInstallTimeout.String())
	mustRunPackagingTool(t, root, kubeEnv(kubeconfig), "", "kubectl", "-n", setupNamespace, "rollout", "status",
		"--timeout="+kindInstallTimeout.String(), "deploy/"+setupFullname+"-controller")
	if after := controllerPodName(t, root, kubeconfig); after == before {
		t.Fatalf("the controller pod %s was not replaced after a new Secret checksum, so it still holds the old JWT secret", before)
	}
}

// assertASetupReinstallWithANewPasswordIsRefused uninstalls the release,
// keeps its database volume, and installs a fresh setup's output against it.
// The fresh output carries a different database password, so the chart must
// refuse and name the claim.
func assertASetupReinstallWithANewPasswordIsRefused(t *testing.T, root, kubeconfig string) {
	t.Helper()
	claim := "data-" + setupFullname + "-postgres-0"
	mustRunPackagingTool(t, root, kubeEnv(kubeconfig), "", "helm", "uninstall", setupReleaseName, "--namespace", setupNamespace)
	if out, err := runPackagingTool(t, root, kubeEnv(kubeconfig), "", "kubectl", "-n", setupNamespace, "get", "pvc", claim); err != nil {
		t.Fatalf("the database claim did not survive the uninstall, so this case cannot arise: %v\n%s", err, out)
	}
	mustRunPackagingTool(t, root, kubeEnv(kubeconfig), "", "kubectl", "-n", setupNamespace, "delete", "secret", setupSecretName)

	fresh := runSetupForHelm(t)
	mustRunPackagingTool(t, root, kubeEnv(kubeconfig), "", "kubectl", "create", "-f", filepath.Join(fresh, setup.HelmSecretFile))
	out, err := helmInstallFromSetup(t, root, kubeconfig, fresh, false)
	if err == nil {
		t.Fatalf("installing a fresh setup's output onto the retained database succeeded, so its controller cannot authenticate and crash-loops:\n%s", out)
	}
	for _, want := range []string{claim, "KEEP THE DATA"} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal does not mention %q:\n%s", want, out)
		}
	}
}
