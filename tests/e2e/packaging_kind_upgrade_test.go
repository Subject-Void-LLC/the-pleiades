//go:build integration

// Phase 84's Helm upgrade gate, on the pinned kind cluster: the previous
// release's chart and images installed, then `helm upgrade` to this
// checkout's chart and images, the way an operator does it, in both shapes
// the chart runs in.
//
//   - The default: one controller on a ReadWriteOnce volume, which the chart
//     upgrades with the Recreate strategy. It has a short outage by design,
//     so what it has to prove is what it keeps: the data, the key, the
//     controller's volume and the certificate on it.
//   - Two controllers with no volume and a certificate from a Secret, which
//     the chart upgrades with RollingUpdate. What it has to prove is the
//     overlap: a pod of the previous build is still Ready while a pod of
//     this build exists and migrates, and the rollout converges with every
//     pod on this build and the data intact. That requests do not fail
//     through the overlap is proven at the binary level, with a client that
//     routes by readiness (upgrade_binary_gate_test.go); a kubectl
//     port-forward pins one pod and cannot stand in for a Service.
package e2e

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth/authtest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
)

// Images of the previous release, built from its own tree.
const (
	previousControllerImage = "pleiades/controller:previous"
	previousRunnerImage     = "pleiades/runner:previous"
)

// buildPreviousImages builds the previous release's controller and runner
// images from its extracted tree. Docker's layer cache makes a second build of
// the same tree cheap, which is what lets the compose gate and this one each
// build their own.
func buildPreviousImages(t *testing.T, root string, prev previousBuild) {
	t.Helper()
	for _, image := range []struct{ tag, dockerfile string }{
		{previousControllerImage, "Dockerfile.controller"},
		{previousRunnerImage, "Dockerfile.runner"},
	} {
		mustRunPackagingTool(t, root, nil, "", "docker", "build",
			"-f", filepath.Join(prev.tree, image.dockerfile),
			"-t", image.tag,
			"--build-arg", "VCS_REF="+prev.ref,
			prev.tree)
	}
}

// helmRelease is one release this gate installs and upgrades.
type helmRelease struct {
	name, namespace string
	root, kube      string
	values          []string
}

// sets turns the release's values, plus the image tag, into helm flags.
func (r helmRelease) sets(tag string) []string {
	args := []string{
		"--namespace", r.namespace, "--create-namespace",
		"--set", "fullnameOverride=" + r.name,
		"--set", "secrets.masterEncryptionKey=" + kindMasterKey,
		"--set", "secrets.jwtSecret=" + kindJWTSecret,
		"--set", "postgresql.auth.password=" + kindPostgresPassword,
		"--set", "controller.image.pullPolicy=Never",
		"--set", "runner.image.pullPolicy=Never",
		"--set", "controller.image.tag=" + tag,
		"--set", "runner.image.tag=" + tag,
		"--wait", "--timeout", kindInstallTimeout.String(),
	}
	for _, v := range r.values {
		args = append(args, "--set", v)
	}
	return args
}

// helm runs one helm command against the gate's cluster.
func (r helmRelease) helm(t *testing.T, args ...string) string {
	t.Helper()
	out, err := runPackagingTool(t, r.root, kubeEnv(r.kube), "", "helm", args...)
	if err != nil {
		pods, _ := runPackagingTool(t, r.root, kubeEnv(r.kube), "", "kubectl", "-n", r.namespace, "get", "pods", "-o", "wide")
		events, _ := runPackagingTool(t, r.root, kubeEnv(r.kube), "", "kubectl", "-n", r.namespace, "get", "events", "--sort-by=.lastTimestamp")
		t.Fatalf("helm %s: %v\n%s\n--- pods ---\n%s\n--- events ---\n%s", strings.Join(args[:2], " "), err, out, pods, events)
	}
	return out
}

// kubectl runs one kubectl command in the release's namespace.
func (r helmRelease) kubectl(t *testing.T, args ...string) string {
	t.Helper()
	return mustRunPackagingTool(t, r.root, kubeEnv(r.kube), "", "kubectl", append([]string{"-n", r.namespace}, args...)...)
}

// api opens a port-forward to the release's controller Service and returns a
// function that calls its API, the certificate it presented, and a stop.
func (r helmRelease) api(t *testing.T) (func(method, path string, body any) (int, []byte), *x509.Certificate, func()) {
	t.Helper()
	forward := packagingCommand(t, r.root, kubeEnv(r.kube), "kubectl", "-n", r.namespace,
		"port-forward", "svc/"+r.name+"-controller", "0:8080")
	var out bytes.Buffer
	var mu sync.Mutex
	forward.Stdout = writerFunc(func(p []byte) (int, error) { mu.Lock(); defer mu.Unlock(); return out.Write(p) })
	forward.Stderr = forward.Stdout
	if err := forward.Start(); err != nil {
		t.Fatalf("starting kubectl port-forward: %v", err)
	}
	stop := func() { _ = forward.Process.Kill(); _ = forward.Wait() }
	var port string
	deadline := time.Now().Add(2 * time.Minute)
	for port == "" && time.Now().Before(deadline) {
		mu.Lock()
		if m := portForwardControllerListenLine.FindStringSubmatch(out.String()); m != nil {
			port = m[1]
		}
		mu.Unlock()
		time.Sleep(100 * time.Millisecond)
	}
	if port == "" {
		stop()
		t.Fatalf("kubectl port-forward never listened:\n%s", out.String())
	}

	presented := capturePresentedCertificate(t, port)
	pool := x509.NewCertPool()
	pool.AddCert(presented)
	client := &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{
		RootCAs: pool, ServerName: presented.DNSNames[0], MinVersion: tls.VersionTLS12,
	}}}
	token := authtest.NewWithSecret(t, kindJWTSecret, "pleiades-controller", "pleiades-api").
		Token(t, &auth.Identity{Subject: "helm-upgrade-gate", Role: auth.RoleAdmin})
	call := func(method, path string, body any) (int, []byte) {
		var raw []byte
		if body != nil {
			raw, _ = json.Marshal(body)
		}
		req, err := http.NewRequest(method, "https://127.0.0.1:"+port+"/api/v1"+path, bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		req.Host = presented.DNSNames[0]
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, b
	}
	return call, presented, stop
}

// writerFunc adapts a function to io.Writer.
type writerFunc func([]byte) (int, error)

// Write calls the function.
func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

// seedCredential creates an organization and a credential through the API and
// returns the organization's id, which listing the credential needs.
func seedCredential(t *testing.T, call func(string, string, any) (int, []byte), name string) string {
	t.Helper()
	status, body := call(http.MethodPost, "/organizations", map[string]any{"name": name + "-org"})
	if status != http.StatusCreated {
		t.Fatalf("creating an organization = %d: %s", status, body)
	}
	org := idOf(t, body)
	status, body = call(http.MethodGet, "/credential-types", nil)
	var types struct {
		Types []struct {
			ID        int    `json:"id"`
			Namespace string `json:"namespace"`
		} `json:"credential_types"`
	}
	_ = json.Unmarshal(body, &types)
	sshType := 0
	for _, ct := range types.Types {
		if ct.Namespace == "ssh" {
			sshType = ct.ID
		}
	}
	if status != http.StatusOK || sshType == 0 {
		t.Fatalf("no ssh credential type (%d): %s", status, body)
	}
	status, body = call(http.MethodPost, "/credentials", map[string]any{
		"name": name, "credential_type": sshType, "organization": atoi(t, org),
		"inputs": map[string]any{"username": "gate", "password": "sealed-under-the-chart-secret"},
	})
	if status != http.StatusCreated {
		t.Fatalf("creating a credential = %d: %s", status, body)
	}
	return org
}

// hasCredential reports whether the API lists a credential of that name in
// org. The list is per organization, and a request naming none is refused
// with 400, which is how this gate first failed when it finally ran.
func hasCredential(t *testing.T, call func(string, string, any) (int, []byte), org, name string) bool {
	t.Helper()
	status, body := call(http.MethodGet, "/credentials?organization="+org, nil)
	if status != http.StatusOK {
		t.Fatalf("listing credentials = %d: %s", status, body)
	}
	return strings.Contains(string(body), name)
}

// TestUpgradeReleaseGate_HelmUpgrade is the gate.
func TestUpgradeReleaseGate_HelmUpgrade(t *testing.T) {
	requireDockerDaemon(t)
	requirePackagingTool(t, "kubectl", "talk to the cluster it creates")
	requirePackagingTool(t, "helm", "install and upgrade the chart")
	requireKindAtPinnedVersion(t)

	root := ensurePleiadesImages(t)
	prev := requirePreviousBuild(t)
	t.Logf("upgrading from %s across %v", prev.ref, prev.crossed)
	buildPreviousImages(t, root, prev)
	kubeconfig := filepath.Join(t.TempDir(), "kubeconfig")
	createKindCluster(t, root, kubeconfig)
	importImagesIntoKind(t, root, packagingControllerImage, packagingRunnerImage, previousControllerImage, previousRunnerImage)

	previousChart := filepath.Join(prev.tree, "helm", "the-pleiades")
	currentChart := filepath.Join(root, "helm", "the-pleiades")

	t.Run("one controller, Recreate", func(t *testing.T) {
		r := helmRelease{name: "upgrade-recreate", namespace: "upgrade-recreate", root: root, kube: kubeconfig}
		r.helm(t, append([]string{"install", r.name, previousChart}, r.sets("previous")...)...)
		defer r.helm(t, "uninstall", r.name, "--namespace", r.namespace)

		call, certBefore, stop := r.api(t)
		recreateOrg := seedCredential(t, call, "kept-through-recreate")
		stop()
		secretBefore := r.kubectl(t, "get", "secret", r.name, "-o", "jsonpath={.data}")
		claimBefore := r.kubectl(t, "get", "pvc", r.name+"-controller-data", "-o", "jsonpath={.metadata.uid}")

		started := time.Now()
		r.helm(t, append([]string{"upgrade", r.name, currentChart}, r.sets("dev")...)...)
		t.Logf("helm upgrade with Recreate took %s, the outage included", time.Since(started).Round(time.Second))

		call, certAfter, stop := r.api(t)
		defer stop()
		if !hasCredential(t, call, recreateOrg, "kept-through-recreate") {
			t.Error("the credential stored before the upgrade is gone")
		}
		if r.kubectl(t, "get", "secret", r.name, "-o", "jsonpath={.data}") != secretBefore {
			t.Error("helm upgrade changed the chart's Secret, which holds the master key")
		}
		if r.kubectl(t, "get", "pvc", r.name+"-controller-data", "-o", "jsonpath={.metadata.uid}") != claimBefore {
			t.Error("helm upgrade replaced the controller's volume")
		}
		if !certBefore.Equal(certAfter) {
			t.Error("helm upgrade replaced the controller's serving certificate")
		}
	})

	t.Run("two controllers, RollingUpdate", func(t *testing.T) {
		r := helmRelease{name: "upgrade-rolling", namespace: "upgrade-rolling", root: root, kube: kubeconfig,
			values: []string{
				"controller.replicaCount=2",
				"controller.persistence.enabled=false",
				"controller.tls.mode=secret",
				"controller.tls.secretName=upgrade-rolling-tls",
			}}
		cert := testsupport.NewServingCertFor(t, t.TempDir())
		mustRunPackagingTool(t, root, kubeEnv(kubeconfig), "", "kubectl", "create", "namespace", r.namespace)
		r.kubectl(t, "create", "secret", "tls", "upgrade-rolling-tls", "--cert="+cert.CertFile, "--key="+cert.KeyFile)
		r.helm(t, append([]string{"install", r.name, previousChart}, r.sets("previous")...)...)
		defer r.helm(t, "uninstall", r.name, "--namespace", r.namespace)

		call, _, stop := r.api(t)
		rollingOrg := seedCredential(t, call, "kept-through-rolling")
		stop()

		// Watch the controller pods through the upgrade, and record whether
		// a previous-build pod was ever Ready while a pod of this build
		// existed: that is the overlap the compatibility window exists for.
		overlap := make(chan bool, 1)
		done := make(chan struct{})
		exited := make(chan struct{})
		var closeDone sync.Once
		stopWatching := func() { closeDone.Do(func() { close(done) }) }
		// A failed helm upgrade ends this subtest with Fatal, before the
		// stop below. Stop the watcher and wait for it either way, so it
		// never goes on running kubectl for a test that has finished.
		t.Cleanup(func() {
			stopWatching()
			<-exited
		})
		go func() {
			defer close(exited)
			seen := false
			for {
				select {
				case <-done:
					overlap <- seen
					return
				case <-time.After(500 * time.Millisecond):
				}
				out, err := runPackagingTool(t, root, kubeEnv(kubeconfig), "", "kubectl", "-n", r.namespace, "get", "pods",
					"-l", "app.kubernetes.io/component=controller,app.kubernetes.io/instance="+r.name,
					"-o", `jsonpath={range .items[*]}{.spec.containers[0].image} {.status.containerStatuses[0].ready}{"\n"}{end}`)
				if err != nil {
					continue
				}
				oldReady, newExists := false, false
				for _, line := range strings.Split(out, "\n") {
					if strings.HasPrefix(line, previousControllerImage+" true") {
						oldReady = true
					}
					if strings.HasPrefix(line, packagingControllerImage) {
						newExists = true
					}
				}
				seen = seen || (oldReady && newExists)
			}
		}()
		r.helm(t, append([]string{"upgrade", r.name, currentChart}, r.sets("dev")...)...)
		stopWatching()
		if !<-overlap {
			t.Error("no previous-build controller was Ready while this build's pod existed; the rollout did not overlap")
		}

		images := r.kubectl(t, "get", "pods", "-l", "app.kubernetes.io/component=controller,app.kubernetes.io/instance="+r.name,
			"-o", `jsonpath={range .items[*]}{.spec.containers[0].image}{"\n"}{end}`)
		if strings.Count(images, packagingControllerImage) != 2 || strings.Contains(images, previousControllerImage) {
			t.Errorf("after the rollout the controller pods run:\n%s\nwant two, both this build", images)
		}
		call, _, stop = r.api(t)
		defer stop()
		if !hasCredential(t, call, rollingOrg, "kept-through-rolling") {
			t.Error("the credential stored before the rolling upgrade is gone")
		}
	})
}
