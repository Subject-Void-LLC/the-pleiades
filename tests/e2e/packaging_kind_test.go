//go:build integration

// Phase 20's Release Gate, Kubernetes half.
//
// A real `helm install` of helm/the-pleiades into a real Kubernetes
// cluster, from images built on this machine, with no registry anywhere in
// the path. That last clause is the point: nothing publishes a Pleiades
// image, so an install that only worked by pulling from a registry would
// be an install nobody can perform.
//
// FOUR THINGS ARE PINNED, and every one of them was inherited from the
// machine before this gate was written:
//
//   - the kind binary version, checked rather than assumed, because kind
//     changes how it talks to the node's container runtime between minor
//     releases and a cluster that will not come up looks identical to a
//     chart that will not render;
//   - the node image, by tag AND digest, so the Kubernetes version this
//     chart is proven against is a fact in this file rather than whatever
//     kind's default happened to be on the day;
//   - the cluster name, so nothing here can touch a cluster somebody else
//     created (Docker Desktop ships one called "desktop");
//   - the kubeconfig, written into the test's own temporary directory, so
//     the run never edits ~/.kube/config and never changes which cluster
//     the developer's next kubectl command talks to.
//
// HOW THE IMAGES GET IN. `docker save <image> | docker exec -i <node> ctr
// --namespace=k8s.io images import -` writes the image straight into the
// node's containerd store, which is where the kubelet looks. It is used
// instead of `kind load docker-image` because it depends on nothing but
// containerd being present in the node image: kind's own loader has to
// detect the host's snapshotter first, and that detection is the part that
// breaks against newer Docker daemons. The chart's own values.yaml calls
// this out as the air-gapped install route.
//
// WHAT STILL PULLS. PostgreSQL and NATS are pulled from the registry as
// normal. They are multi-architecture upstream images, and side-loading a
// multi-architecture image into a kind node does not work, so pretending
// otherwise would only produce a confusing failure. This gate therefore
// needs the network, and skips rather than fails when the pull cannot
// happen.
package e2e

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The pins. Changing any of these is a deliberate act with a diff, which
// is the entire reason they are constants in a file rather than defaults
// in a tool.
const (
	// kindPinnedVersion is the kind release this gate is proven against,
	// written as it appears in `kind version`, which prints
	// "kind v0.20.0 go1.20.4 linux/amd64". The leading "v" is part of the
	// pin rather than decoration: without it, "0.20.0" would also match a
	// hypothetical v10.20.0.
	kindPinnedVersion = "v0.20.0"

	// kindPinnedNodeImage is the Kubernetes version the chart is proven
	// against, by tag and by digest. The digest is what makes it a pin: a
	// tag can be republished, and then the "pinned" cluster silently moves.
	//
	// v1.27.3 is the node image kind 0.20.0 ships with. Pairing a kind
	// release with a node image from a much later Kubernetes is not a
	// stricter test, it is an untested combination: kind writes the kubeadm
	// configuration, and the API version it writes has to be one the
	// kubelet in the node image still accepts.
	kindPinnedNodeImage = "kindest/node:v1.27.3@sha256:3966ac761ae0136263ffdb6cfd4db23ef8a83cba8a463690e98317add2c9ba72"

	// kindClusterPrefix names every cluster this gate has ever created,
	// and kindClusterName below appends the process id to it.
	//
	// A CONSTANT NAME WAS THE BUG. This gate used to call its cluster
	// exactly kindClusterPrefix and open by deleting any cluster of that
	// name, which is correct for reclaiming what a killed predecessor left
	// and cannot tell that from a live sibling. A name shared by every run
	// with no owner recorded in it is a race with every other run: a second
	// `make ci`, a `make push-gate` overlapping a manual invocation, or a
	// person tidying up all destroy the first one's cluster mid-install.
	// The failure that produces is not merely a lost run, it is a lost run
	// reported at whichever assertion happened to be executing, which reads
	// as a defect in whatever that assertion was about. See
	// FAILURE_PATTERNS.md #141 and LESSONS_LEARNED.md #129.
	kindClusterPrefix = "pleiades-release-gate"

	// helmReleaseName plus fullnameOverride below make every object name in
	// the release predictable, so this test names objects rather than
	// discovering them, and a rename in the chart's _helpers.tpl fails here
	// loudly instead of silently selecting nothing.
	helmReleaseName = "pleiades"
	helmNamespace   = "pleiades"
	helmFullname    = "pleiades"
)

// The credentials this install runs on. Throwaway values with the shapes
// the chart requires: the key is base64 of exactly 32 bytes and the JWT
// secret is at least 32 bytes. The chart refuses to invent either, which
// is the decision values.yaml explains at most length, so the gate has to
// supply them exactly as an operator would.
const (
	kindMasterKey        = "a2tra2tra2tra2tra2tra2tra2tra2tra2tra2tra2s="
	kindJWTSecret        = "release-gate-jwt-secret-at-least-32-bytes-long"
	kindPostgresPassword = "release-gate-postgres-password"
	kindAdminEmail       = "k8s-gate-admin@example.test"
	kindAdminPassword    = "a-real-kubernetes-gate-password"
)

// kindInstallTimeout bounds the wait for every workload in the release to
// report ready.
//
// Generous on purpose. On a first run this covers pulling the PostgreSQL
// and NATS images, provisioning three PersistentVolumes, and the
// controller's own schema migration, and the controller and the runners
// each crash and restart while their dependencies come up, because both
// binaries exit at startup on an unreachable database or broker and rely
// on the orchestrator to retry.
const kindInstallTimeout = 8 * time.Minute

// TestPackagingReleaseGate_KubernetesInstall is the Kubernetes half of
// Phase 20's Release Gate.
func TestPackagingReleaseGate_KubernetesInstall(t *testing.T) {
	requireDockerDaemon(t)
	requirePackagingTool(t, "kubectl", "talk to the cluster it creates")
	requirePackagingTool(t, "helm", "install the chart under test")
	requireKindAtPinnedVersion(t)

	root := ensurePleiadesImages(t)
	kubeconfig := filepath.Join(t.TempDir(), "kubeconfig")

	createKindCluster(t, root, kubeconfig)
	importImagesIntoKindNode(t, root)
	installChart(t, root, kubeconfig)

	assertControllerIsReady(t, root, kubeconfig)
	assertReadyzReportsDatabaseAndNats(t, root, kubeconfig)
	assertBootstrapAdminWorksThroughKubectlExec(t, root, kubeconfig)
	assertRunnersAreReady(t, root, kubeconfig)
	assertALongReleaseNameStillProducesFourWorkingWorkloads(t, root, kubeconfig)
	assertReinstallWithAChangedPasswordIsRefused(t, root, kubeconfig)
}

// assertRunnersAreReady proves the runner Deployment's own probes pass in
// a real cluster.
//
// It could not exist before Phase 20: the runner container shipped with no
// probes at all, so Available meant "the containers started" and told an
// operator nothing. Its readiness probe now execs `runner healthcheck`,
// which reads a heartbeat the agent writes only after a real round trip to
// the durable NATS consumer it pulls from. So a Deployment reporting
// Available here means every replica genuinely reached the broker, which
// is the claim FAILURE_PATTERNS.md #119 says cannot be made any other way.
//
// The in-pod exec afterwards is the same distinction assertControllerIsReady
// draws: the condition says a probe passed, and running the command says
// what it found.
func assertRunnersAreReady(t *testing.T, root, kubeconfig string) {
	t.Helper()

	deployment := "deployment/" + helmFullname + "-runner"
	mustRunPackagingTool(t, root, kubeEnv(kubeconfig), "",
		"kubectl", "-n", helmNamespace, "wait", "--for=condition=Available",
		"--timeout="+kindInstallTimeout.String(), deployment)

	out := mustRunPackagingTool(t, root, kubeEnv(kubeconfig), "",
		"kubectl", "-n", helmNamespace, "exec", deployment, "--",
		"/app/runner", "healthcheck")
	if !strings.Contains(out, "healthy") {
		t.Errorf("in-pod `runner healthcheck` succeeded but did not report a heartbeat age, "+
			"so the probe may be passing on something other than the heartbeat:\n%s", out)
	}
	t.Logf("in-pod `runner healthcheck`: %s", strings.TrimSpace(out))
}

// The second release this gate installs, whose whole purpose is its NAME.
//
// helmLongReleaseName is exactly helmMaxReleaseName characters, which is
// Helm's own limit, and it deliberately does NOT contain "the-pleiades":
// the naming helper appends the chart name when the release name does not
// already carry it, which is the longer of its two branches and therefore
// the one under most pressure.
const (
	helmLongReleaseName = "pleiades-release-with-a-deliberately-very-long-name-x"
	helmLongNamespace   = "pleiades-longname"
	helmMaxReleaseName  = 53
)

// assertALongReleaseNameStillProducesFourWorkingWorkloads is the case this
// gate was missing.
//
// WHY IT WAS MISSING. Every other assertion here installs with
// fullnameOverride=pleiades, an eight-character name, which is short enough
// that no naming helper is under any pressure at all. The two defects this
// chart has actually had were both at the far end of that scale and neither
// could have been caught by an eight-character install:
//
//   - A release name of 49 to 53 characters, all legal to Helm, truncated
//     the controller, the runner and both StatefulSets to ONE name. Four
//     workloads, one object, and `helm install` reported success.
//   - The FIX for that gave every kind a 63-character budget, which is 11
//     too generous for a StatefulSet: its pods carry a
//     controller-revision-hash label built from its name, which the API
//     server refuses past 63 bytes. From about 40 characters, both
//     StatefulSets were created and produced ZERO PODS for the life of the
//     release, with a FailedCreate event as the only evidence.
//
// So this installs at the maximum legal release name with no override, and
// asserts what both defects would have broken: four distinct workloads, and
// both StatefulSets actually reaching Ready. `helm install --wait` already
// covers most of that, and the explicit assertions are here so a failure
// names the class rather than printing a timeout.
//
// It runs in its own namespace and removes itself, so it costs one extra
// release for the length of this function rather than for the whole gate.
func assertALongReleaseNameStillProducesFourWorkingWorkloads(t *testing.T, root, kubeconfig string) {
	t.Helper()

	// A guard on the constant, not on the chart: an edit that shortened this
	// name would leave the test green while it stopped exercising the
	// boundary it exists for.
	if len(helmLongReleaseName) != helmMaxReleaseName {
		t.Fatalf("helmLongReleaseName is %d characters; this case only exercises the boundary at Helm's own limit of %d",
			len(helmLongReleaseName), helmMaxReleaseName)
	}

	uninstall := func() {
		_, _ = runPackagingTool(t, root, kubeEnv(kubeconfig), "",
			"helm", "uninstall", helmLongReleaseName, "--namespace", helmLongNamespace)
		_, _ = runPackagingTool(t, root, kubeEnv(kubeconfig), "",
			"kubectl", "delete", "namespace", helmLongNamespace, "--ignore-not-found")
	}
	t.Cleanup(uninstall)

	out, err := runPackagingTool(t, root, kubeEnv(kubeconfig), "",
		"helm", "install", helmLongReleaseName, filepath.Join(root, "helm", "the-pleiades"),
		"--namespace", helmLongNamespace, "--create-namespace",
		"--set", "secrets.masterEncryptionKey="+kindMasterKey,
		"--set", "secrets.jwtSecret="+kindJWTSecret,
		"--set", "postgresql.auth.password="+kindPostgresPassword,
		"--set", "controller.image.pullPolicy=Never",
		"--set", "runner.image.pullPolicy=Never",
		// One runner rather than the default two. The claim here is about
		// object NAMES, which one replica proves exactly as well, and the
		// primary release above already carries the two-replica case.
		"--set", "runner.replicaCount=1",
		"--wait", "--timeout", kindInstallTimeout.String())
	if err != nil {
		pods, _ := runPackagingTool(t, root, kubeEnv(kubeconfig), "",
			"kubectl", "-n", helmLongNamespace, "get", "pods,statefulset,deployment", "-o", "wide")
		events, _ := runPackagingTool(t, root, kubeEnv(kubeconfig), "",
			"kubectl", "-n", helmLongNamespace, "get", "events", "--sort-by=.lastTimestamp")
		t.Fatalf("installing at a %d-character release name failed: %v\n%s\n--- objects ---\n%s\n--- events ---\n%s",
			len(helmLongReleaseName), err, out, pods, events)
	}

	statefulSets := releaseObjectNames(t, root, kubeconfig, helmLongNamespace, "statefulset")
	deployments := releaseObjectNames(t, root, kubeconfig, helmLongNamespace, "deployment")
	t.Logf("at a %d-character release name: StatefulSets %v, Deployments %v",
		len(helmLongReleaseName), statefulSets, deployments)

	// The collision class, stated as counts and as distinctness. Four
	// workloads that share a name arrive here as two objects, not four.
	if len(statefulSets) != 2 {
		t.Fatalf("the release has %d StatefulSets, want 2 (postgres and nats): %v", len(statefulSets), statefulSets)
	}
	if len(deployments) != 2 {
		t.Fatalf("the release has %d Deployments, want 2 (controller and runner): %v", len(deployments), deployments)
	}
	seen := map[string]bool{}
	for _, name := range append(append([]string{}, statefulSets...), deployments...) {
		if seen[name] {
			t.Fatalf("two workloads in this release share the name %q, so one silently replaced the other on apply: StatefulSets %v, Deployments %v",
				name, statefulSets, deployments)
		}
		seen[name] = true
	}

	// The zero-pods class. A StatefulSet whose name is too long is created
	// successfully and never produces a pod, so Ready is the only assertion
	// that can tell the two states apart.
	for _, name := range statefulSets {
		out, err := runPackagingTool(t, root, kubeEnv(kubeconfig), "",
			"kubectl", "-n", helmLongNamespace, "rollout", "status",
			"--timeout="+kindInstallTimeout.String(), "statefulset/"+name)
		if err != nil {
			events, _ := runPackagingTool(t, root, kubeEnv(kubeconfig), "",
				"kubectl", "-n", helmLongNamespace, "get", "events", "--sort-by=.lastTimestamp")
			t.Fatalf("StatefulSet %q (%d characters) never reached Ready. A StatefulSet past its own name limit is "+
				"CREATED and then produces no pods at all, with a FailedCreate event as the only evidence: %v\n%s\n--- events ---\n%s",
				name, len(name), err, out, events)
		}
		t.Logf("StatefulSet %q (%d characters) is Ready", name, len(name))
	}

	uninstall()
}

// releaseObjectNames lists the names of one kind of object in a namespace.
//
// It reads what the cluster HAS rather than computing what the chart should
// have produced. That is the whole point at these lengths: a test that
// derived the expected names from the same helper the chart uses would agree
// with a broken helper, which is how the collision survived a green suite in
// the first place.
func releaseObjectNames(t *testing.T, root, kubeconfig, namespace, kind string) []string {
	t.Helper()
	out := mustRunPackagingTool(t, root, kubeEnv(kubeconfig), "",
		"kubectl", "-n", namespace, "get", kind,
		"-o", "jsonpath={range .items[*]}{.metadata.name}{\"\\n\"}{end}")

	var names []string
	for _, line := range strings.Split(out, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			names = append(names, trimmed)
		}
	}
	return names
}

// kindChangedPostgresPassword is the second database password this gate
// installs with, which is the mistake the assertion below is about.
const kindChangedPostgresPassword = "release-gate-postgres-password-changed"

// assertReinstallWithAChangedPasswordIsRefused proves the chart refuses to
// install onto a retained database volume it cannot open.
//
// THE FAILURE THIS IS ABOUT. `helm uninstall` leaves the PostgreSQL claim
// behind on purpose, because deleting a database on uninstall would be worse.
// So a reinstall under the same release name lands on an initialized volume,
// and PostgreSQL applies POSTGRES_PASSWORD only when it initializes an EMPTY
// directory. A second install with a different password therefore produced a
// release where every object was created, the database was healthy, and the
// controller failed authentication and crash-looped forever with nothing in
// any output naming the cause.
//
// WHY IT IS HERE AND NOT IN tools/helm-lint. The check reads the retained
// claim's annotation with `lookup`, which needs an API server: it does nothing
// under `helm template`, so the only honest proof is a real uninstall followed
// by a real install against a real cluster. That is this gate's whole job.
//
// It runs last because it uninstalls the release the assertions above needed.
// The cleanup registered by installChart still runs afterwards and still finds
// nothing to remove, which it already tolerates.
func assertReinstallWithAChangedPasswordIsRefused(t *testing.T, root, kubeconfig string) {
	t.Helper()

	claim := "data-" + helmFullname + "-postgres-0"

	mustRunPackagingTool(t, root, kubeEnv(kubeconfig), "",
		"helm", "uninstall", helmReleaseName, "--namespace", helmNamespace)

	// The claim outliving the release is the precondition for everything
	// below, and it is a claim about Kubernetes rather than about this chart,
	// so it is checked rather than assumed.
	if out, err := runPackagingTool(t, root, kubeEnv(kubeconfig), "",
		"kubectl", "-n", helmNamespace, "get", "pvc", claim); err != nil {
		t.Fatalf("the database claim %s did not survive `helm uninstall`, so the case this "+
			"assertion is about cannot arise here: %v\n%s", claim, err, out)
	}

	out, err := runPackagingTool(t, root, kubeEnv(kubeconfig), "",
		"helm", "install", helmReleaseName, filepath.Join(root, "helm", "the-pleiades"),
		"--namespace", helmNamespace,
		"--set", "fullnameOverride="+helmFullname,
		"--set", "secrets.masterEncryptionKey="+kindMasterKey,
		"--set", "secrets.jwtSecret="+kindJWTSecret,
		"--set", "postgresql.auth.password="+kindChangedPostgresPassword,
		"--set", "controller.image.pullPolicy=Never",
		"--set", "runner.image.pullPolicy=Never")
	if err == nil {
		// Installing succeeded, so the release is now running against a
		// database it cannot authenticate to. Remove it rather than leaving a
		// crash-looping release behind for the next assertion to trip over.
		_, _ = runPackagingTool(t, root, kubeEnv(kubeconfig), "",
			"helm", "uninstall", helmReleaseName, "--namespace", helmNamespace)
		t.Fatalf("installing over the retained database volume with a DIFFERENT password succeeded. "+
			"That release cannot work: PostgreSQL keeps the password the volume was initialized "+
			"with, so the controller crash-loops with nothing saying why.\n%s", out)
	}

	// The refusal has to be usable, not merely present. Each of these is a
	// piece an operator needs in order to act: which volume, which choice
	// keeps the data, and which one destroys it.
	for _, required := range []string{
		claim,
		"KEEP THE DATA",
		"THIS DESTROYS THE DATABASE",
		"kubectl delete pvc " + claim,
	} {
		if !strings.Contains(out, required) {
			t.Errorf("the refusal does not mention %q, so it does not tell the operator what to "+
				"do next:\n%s", required, out)
		}
	}

	// The other half of the claim: the check refuses a MISMATCH, not every
	// reinstall. An operator who reinstalls with the password the volume was
	// created with keeps their data and their release.
	mustRunPackagingTool(t, root, kubeEnv(kubeconfig), "",
		"helm", "install", helmReleaseName, filepath.Join(root, "helm", "the-pleiades"),
		"--namespace", helmNamespace,
		"--set", "fullnameOverride="+helmFullname,
		"--set", "secrets.masterEncryptionKey="+kindMasterKey,
		"--set", "secrets.jwtSecret="+kindJWTSecret,
		"--set", "postgresql.auth.password="+kindPostgresPassword,
		"--set", "controller.image.pullPolicy=Never",
		"--set", "runner.image.pullPolicy=Never")
	t.Logf("the chart refused the changed password, named %s, and reinstalled cleanly with the "+
		"original one", claim)
}

// requireKindAtPinnedVersion skips unless kind is present AND is the
// version this gate was proven against.
//
// Skipping on a version mismatch rather than failing is the honest
// behavior: a different kind may well work, but this gate has no evidence
// either way, and a red test on a machine with a newer kind teaches a
// developer to ignore red tests. The message names the pin so the reader
// can decide.
func requireKindAtPinnedVersion(t *testing.T) {
	t.Helper()
	requirePackagingTool(t, "kind", "create a throwaway Kubernetes cluster")

	out, err := exec.Command("kind", "version").CombinedOutput()
	if err != nil {
		t.Skipf("`kind version` failed, so the pin cannot be checked: %v\n%s", err, out)
	}
	reported := strings.TrimSpace(string(out))
	if !strings.Contains(reported, kindPinnedVersion) {
		t.Skipf("kind reports %q; this gate is pinned to %s and has no evidence about any other "+
			"version, so it skips rather than reporting a failure it cannot attribute",
			reported, kindPinnedVersion)
	}
	t.Logf("%s (pinned to %s), node image %s", reported, kindPinnedVersion, kindPinnedNodeImage)
}

// kindCreateAttempts is how many times the cluster is provisioned before
// the gate gives up and skips.
//
// More than one because provisioning a Kubernetes cluster is not the thing
// under test, and it fails here for reasons that have nothing to do with
// this repository. Measured on the machine this gate was written on: one
// provisioning in eight lost etcd during the CNI install ("Error from
// server: etcdserver: request timed out"), and the one that did was the
// run that came straight after a container image build in the same test
// binary. Retrying an unrelated provisioning step is not
// papering over a defect; the steps that ARE under test, from `helm
// install` onward, are never retried and never will be.
const kindCreateAttempts = 3

// createKindCluster brings up the throwaway cluster and registers its
// deletion.
//
// --kubeconfig keeps the whole run out of ~/.kube/config. Without it, kind
// writes a context into the developer's real configuration and makes it
// current, so the next kubectl command they type in an unrelated terminal
// silently talks to a cluster this test is about to delete.
func createKindCluster(t *testing.T, root, kubeconfig string) {
	t.Helper()

	// Reclaim what previous runs abandoned, never what a live one is
	// using. This used to be an unconditional delete of a fixed name; see
	// kindClusterPrefix for why that was a race with every concurrent run.
	// This run's own name contains this process's id, so `kind create`
	// cannot collide with a live sibling and there is nothing to delete
	// first.
	reclaimAbandonedClusters(t, root)

	t.Cleanup(func() {
		if out, err := runPackagingTool(t, root, nil, "",
			"kind", "delete", "cluster", "--name", kindClusterName); err != nil {
			t.Errorf("the throwaway cluster was left behind: %v\n%s", err, out)
		}
	})

	var last string
	for attempt := 1; attempt <= kindCreateAttempts; attempt++ {
		started := time.Now()
		out, err := runPackagingTool(t, root, nil, "",
			"kind", "create", "cluster",
			"--name", kindClusterName,
			"--image", kindPinnedNodeImage,
			"--kubeconfig", kubeconfig,
			"--wait", "180s")
		if err == nil {
			t.Logf("kind cluster %q ready in %s (attempt %d)",
				kindClusterName, time.Since(started).Round(time.Millisecond), attempt)
			return
		}
		last = out

		// A node image that cannot be fetched is an absent prerequisite
		// rather than a flake, and retrying it only wastes time.
		if strings.Contains(out, "failed to pull image") || strings.Contains(out, "no such host") {
			t.Skipf("the pinned node image could not be fetched, so there is no cluster to install into:\n%s", out)
		}

		t.Logf("attempt %d to create the cluster failed: %v\n%s", attempt, err, out)
		// kind deletes the partial cluster itself on most failures, but not
		// on all of them, and `create` refuses an existing name.
		deleteKindCluster(t, root)
		time.Sleep(10 * time.Second)
	}

	// Skipping, not failing, and the distinction is precise: nothing this
	// repository ships has been exercised yet at this point. The chart has
	// not been rendered, the images have not been imported, and no Pleiades
	// process has started. A machine that cannot stand up an empty
	// Kubernetes cluster three times running is telling us about itself.
	t.Skipf("could not provision a kind cluster in %d attempts, so the Kubernetes half of this "+
		"gate could not run; nothing under test had been exercised yet. Last output:\n%s",
		kindCreateAttempts, last)
}

// deleteKindCluster removes THIS RUN's cluster, which is the only cluster
// this function can name: kindClusterName carries this process's id. It
// ignores the common case where there is nothing to remove.
func deleteKindCluster(t *testing.T, root string) {
	t.Helper()
	if out, err := runPackagingTool(t, root, nil, "",
		"kind", "delete", "cluster", "--name", kindClusterName); err != nil {
		t.Logf("no cluster to remove, which is the normal case: %v\n%s", err, out)
	}
}

// kindClusterName is this run's own cluster: the shared prefix plus this
// process's id, so no two runs can ever name the same cluster.
//
// The process id is not decoration, it is the ownership marker the constant
// name lacked. It lets a later run answer the one question that matters
// about a leftover cluster, which is not how old it is or how many there
// are but whether anybody is still using it. See reclaimAbandonedClusters.
var kindClusterName = fmt.Sprintf("%s-%d", kindClusterPrefix, os.Getpid())

// reclaimAbandonedClusters removes clusters this gate left behind, and only
// those it can PROVE nobody is using.
//
// The proof is the owning process. A cluster named with the id of a process
// that no longer exists cannot be in use by anything, because the only
// thing that ever used it was that process. A cluster whose owner is still
// alive is left alone and reported, even though it is almost certainly
// somebody else's concurrent run and therefore in the way: refusing to
// clean is recoverable, and deleting a live run's cluster is the incident
// this whole design exists to prevent.
//
// PID reuse is the one way this can be wrong, and it is wrong in the safe
// direction: a recycled id makes a dead cluster look alive, so the cluster
// leaks rather than a live one being destroyed. `make break-glass` clears
// leaks, using this same rule.
func reclaimAbandonedClusters(t *testing.T, root string) {
	t.Helper()

	out, err := runPackagingTool(t, root, nil, "", "kind", "get", "clusters")
	if err != nil {
		// "No kind clusters found" is the empty case, not a failure.
		return
	}

	for _, name := range strings.Fields(out) {
		if !strings.HasPrefix(name, kindClusterPrefix+"-") || name == kindClusterName {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimPrefix(name, kindClusterPrefix+"-"))
		if err != nil {
			t.Logf("leaving cluster %q alone: its name carries no process id this gate can check", name)
			continue
		}
		if processIsAlive(pid) {
			t.Logf("leaving cluster %q alone: process %d is still running, so another run is using it", name, pid)
			continue
		}
		t.Logf("reclaiming cluster %q: its owning process %d is gone", name, pid)
		if out, err := runPackagingTool(t, root, nil, "", "kind", "delete", "cluster", "--name", name); err != nil {
			t.Logf("could not reclaim %q: %v\n%s", name, err, out)
		}
	}
}

// processIsAlive reports whether a process id still names a running
// process. Signal 0 performs the permission and existence checks and
// delivers nothing.
func processIsAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

// kindNodeName is the container kind runs the single control plane node
// in. kind derives it from the cluster name, and every image import below
// targets it by that derived name.
func kindNodeName() string {
	return kindClusterName + "-control-plane"
}

// importImagesIntoKindNode writes both locally built images into the
// node's containerd image store.
//
// `docker save` streams a tar of the image on stdout and `ctr images
// import -` reads exactly that on stdin, so the two are connected by a
// pipe rather than by a file on disk: the controller image is about 64 MB
// exported and there is no reason for it to touch the filesystem twice.
//
// --namespace=k8s.io is not optional. containerd namespaces are hard
// boundaries, and an image imported into the default namespace is invisible
// to the kubelet, which produces an ImagePullBackOff on an image the node
// demonstrably holds.
func importImagesIntoKindNode(t *testing.T, root string) {
	t.Helper()

	for _, image := range []string{packagingControllerImage, packagingRunnerImage} {
		started := time.Now()

		save := packagingCommand(t, root, nil, "docker", "save", image)
		load := packagingCommand(t, root, nil, "docker", "exec", "-i", kindNodeName(),
			"ctr", "--namespace=k8s.io", "images", "import", "-")

		stream, err := save.StdoutPipe()
		if err != nil {
			t.Fatalf("opening the save stream for %s: %v", image, err)
		}
		load.Stdin = stream

		if err := save.Start(); err != nil {
			t.Fatalf("docker save %s: %v", image, err)
		}
		out, err := load.CombinedOutput()
		if err != nil {
			t.Fatalf("importing %s into the kind node: %v\n%s", image, err, out)
		}
		if err := save.Wait(); err != nil {
			t.Fatalf("docker save %s did not finish cleanly: %v", image, err)
		}
		t.Logf("imported %s into %s in %s: %s", image, kindNodeName(),
			time.Since(started).Round(time.Millisecond), strings.TrimSpace(string(out)))
	}

	// Proof rather than trust. `ctr images import` reporting success and
	// the kubelet being able to see the result are different claims, and
	// crictl is the kubelet's own view.
	seen := mustRunPackagingTool(t, root, nil, "",
		"docker", "exec", kindNodeName(), "crictl", "images")
	for _, image := range []string{packagingControllerImage, packagingRunnerImage} {
		repository := strings.SplitN(image, ":", 2)[0]
		if !strings.Contains(seen, repository) {
			t.Fatalf("the kubelet cannot see %s after the import:\n%s", image, seen)
		}
	}
}

// kubeEnv is the environment every kubectl and helm invocation runs with,
// which is the test's own kubeconfig and nothing else.
func kubeEnv(kubeconfig string) []string {
	return []string{"KUBECONFIG=" + kubeconfig}
}

// installChart runs the real `helm install` an operator runs, with the
// three values the chart refuses to invent and nothing else.
//
// pullPolicy=Never is the assertion that this install used no registry.
// With IfNotPresent a missing side-loaded image would be silently pulled
// from Docker Hub, so the test would pass for the wrong reason on a
// machine with network access and fail on the air-gapped machine the
// feature exists for. Never turns that into an ErrImageNeverPull the
// readiness wait reports.
func installChart(t *testing.T, root, kubeconfig string) {
	t.Helper()

	chart := filepath.Join(root, "helm", "the-pleiades")
	started := time.Now()
	out, err := runPackagingTool(t, root, kubeEnv(kubeconfig), "",
		"helm", "install", helmReleaseName, chart,
		"--namespace", helmNamespace, "--create-namespace",
		"--set", "fullnameOverride="+helmFullname,
		"--set", "secrets.masterEncryptionKey="+kindMasterKey,
		"--set", "secrets.jwtSecret="+kindJWTSecret,
		"--set", "postgresql.auth.password="+kindPostgresPassword,
		"--set", "controller.image.pullPolicy=Never",
		"--set", "runner.image.pullPolicy=Never",
		"--wait", "--timeout", kindInstallTimeout.String())
	if err != nil {
		// Collect what the cluster thinks happened before failing, because
		// helm's own timeout message names the object and not the reason.
		pods, _ := runPackagingTool(t, root, kubeEnv(kubeconfig), "",
			"kubectl", "-n", helmNamespace, "get", "pods", "-o", "wide")
		events, _ := runPackagingTool(t, root, kubeEnv(kubeconfig), "",
			"kubectl", "-n", helmNamespace, "get", "events", "--sort-by=.lastTimestamp")
		t.Fatalf("helm install: %v\n%s\n--- pods ---\n%s\n--- events ---\n%s", err, out, pods, events)
	}

	t.Cleanup(func() {
		if out, err := runPackagingTool(t, root, kubeEnv(kubeconfig), "",
			"helm", "uninstall", helmReleaseName, "--namespace", helmNamespace); err != nil {
			// Not an error: the cluster is deleted moments later anyway.
			// Uninstalling first is only so a failure here is attributable
			// to the chart rather than to the cluster disappearing.
			t.Logf("helm uninstall: %v\n%s", err, out)
		}
	})

	t.Logf("helm install completed in %s", time.Since(started).Round(time.Millisecond))
	if !strings.Contains(out, "bootstrap-admin") {
		t.Error("NOTES.txt did not tell the operator to create the first administrator, " +
			"which is the one thing an install cannot be used without")
	}
}

// assertControllerIsReady proves the Deployment reached Available on its
// own terms, not merely that helm's --wait returned.
//
// Available is the condition that means "enough replicas passed their
// readiness probe", and the controller's readiness probe is /readyz, which
// queries the database and the live NATS connection. So this single
// condition already carries most of the claim; the endpoint is queried
// directly below anyway, because a condition says a probe passed and the
// body says what it found.
func assertControllerIsReady(t *testing.T, root, kubeconfig string) {
	t.Helper()

	deployment := "deployment/" + helmFullname + "-controller"
	mustRunPackagingTool(t, root, kubeEnv(kubeconfig), "",
		"kubectl", "-n", helmNamespace, "wait", "--for=condition=Available",
		"--timeout="+kindInstallTimeout.String(), deployment)

	pods := mustRunPackagingTool(t, root, kubeEnv(kubeconfig), "",
		"kubectl", "-n", helmNamespace, "get", "pods", "-o", "wide")
	t.Logf("release pods:\n%s", pods)

	// The shipped readiness probe, run as the shipped binary runs it. This
	// is the step that proves TLS is genuinely verified inside the cluster:
	// cmd/controller's healthcheck subcommand resolves the same certificate
	// path the server wrote to and verifies the local listener against that
	// exact file, so it cannot report ready because something else answered
	// on the port.
	out := mustRunPackagingTool(t, root, kubeEnv(kubeconfig), "",
		"kubectl", "-n", helmNamespace, "exec", deployment, "--",
		"/app/controller", "healthcheck")
	t.Logf("in-pod `controller healthcheck` succeeded: %q", strings.TrimSpace(out))
}

// readyz is the shape of the readiness document internal/api serves.
type readyz struct {
	Status string            `json:"status"`
	Checks map[string]string `json:"checks"`
}

// assertReadyzReportsDatabaseAndNats reads the readiness body over a real
// TLS connection through a port-forward.
//
// ON TRUST, stated exactly rather than glossed. The certificate here is
// the one the controller generated for itself inside the cluster, and
// there is no out-of-band way to fetch it: the image holds no shell, so
// `kubectl cp` (which needs tar in the container) and `kubectl exec cat`
// both fail by design. So this connects once to capture what the server
// presents, then reconnects with a pool holding exactly that certificate
// and full verification on. That second handshake proves the certificate
// is internally valid: unexpired, correctly signed, with a SAN covering
// the name being dialed. It does NOT prove identity, because the anchor
// came from the connection. Identity is proven separately and better by
// the in-pod healthcheck above, which verifies against the file on disk.
func assertReadyzReportsDatabaseAndNats(t *testing.T, root, kubeconfig string) {
	t.Helper()

	local, stop := portForwardController(t, root, kubeconfig)
	defer stop()

	presented := capturePresentedCertificate(t, local)
	assertCertificateCarriesServiceNames(t, presented)

	pool := x509.NewCertPool()
	pool.AddCert(presented)
	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{
			RootCAs:    pool,
			ServerName: "localhost",
			MinVersion: tls.VersionTLS12,
		}},
	}

	resp, err := client.Get(fmt.Sprintf("https://localhost:%s/readyz", local))
	if err != nil {
		t.Fatalf("GET /readyz through the port-forward: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /readyz = %d, want 200\n%s", resp.StatusCode, body)
	}

	var report readyz
	if err := json.Unmarshal(body, &report); err != nil {
		t.Fatalf("decoding the readiness document: %v\n%s", err, body)
	}
	t.Logf("/readyz: %s", strings.TrimSpace(string(body)))

	if report.Status != "ready" {
		t.Errorf("/readyz reports status %q, want ready", report.Status)
	}
	for _, dependency := range []string{"database", "nats"} {
		if got := report.Checks[dependency]; got != "ok" {
			t.Errorf("/readyz reports %s = %q, want ok; the chart wired the controller to a "+
				"dependency it cannot reach", dependency, got)
		}
	}
}

// assertCertificateCarriesServiceNames proves the chart, not just the
// controller, did its half of the TLS work.
//
// The controller puts localhost and its own hostname on a self-provisioned
// certificate with no help. Everything in-cluster that a client would dial
// it by is passed in by the chart, so a certificate missing these names is
// a chart defect that presents as an unexplained name-mismatch error weeks
// later.
func assertCertificateCarriesServiceNames(t *testing.T, cert *x509.Certificate) {
	t.Helper()

	names := map[string]bool{}
	for _, name := range cert.DNSNames {
		names[name] = true
	}
	t.Logf("served certificate: subject %q, DNS names %v", cert.Subject.CommonName, cert.DNSNames)

	service := helmFullname + "-controller"
	for _, required := range []string{
		service,
		service + "." + helmNamespace,
		service + "." + helmNamespace + ".svc",
		service + "." + helmNamespace + ".svc.cluster.local",
	} {
		if !names[required] {
			t.Errorf("the served certificate does not cover %q, so nothing in the cluster can "+
				"reach the controller by that name without a verification failure", required)
		}
	}
	if time.Now().After(cert.NotAfter) {
		t.Errorf("the served certificate expired at %s", cert.NotAfter)
	}
}

// portForwardControllerListenLine matches the line kubectl prints once the
// tunnel is up, which is also where the chosen local port appears.
var portForwardControllerListenLine = regexp.MustCompile(`Forwarding from 127\.0\.0\.1:(\d+)`)

// portForwardController opens a tunnel to the controller Service and
// returns the local port it landed on plus a function that closes it.
//
// The local port is 0, meaning kubectl picks a free one and prints it.
// Choosing a fixed port would collide with the compose half of this gate,
// which publishes 8080 on the same machine, and with whatever else the
// developer is running.
//
// The listen line is waited for rather than slept past. A fixed sleep is
// either wasted time or, under load, a connection refused that looks like
// a broken controller.
func portForwardController(t *testing.T, root, kubeconfig string) (string, func()) {
	t.Helper()

	forward := packagingCommand(t, root, kubeEnv(kubeconfig), "kubectl",
		"-n", helmNamespace, "port-forward",
		"svc/"+helmFullname+"-controller", "0:8080")
	stdout, err := forward.StdoutPipe()
	if err != nil {
		t.Fatalf("opening the port-forward output: %v", err)
	}
	forward.Stderr = forward.Stdout
	if err := forward.Start(); err != nil {
		t.Fatalf("starting kubectl port-forward: %v", err)
	}

	stop := func() {
		if forward.Process != nil {
			_ = forward.Process.Kill()
		}
		_ = forward.Wait()
	}

	type found struct {
		port string
		line string
	}
	result := make(chan found, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			if match := portForwardControllerListenLine.FindStringSubmatch(line); match != nil {
				result <- found{port: match[1], line: line}
				return
			}
		}
		result <- found{}
	}()

	select {
	case got := <-result:
		if got.port == "" {
			stop()
			t.Fatal("kubectl port-forward exited without reporting a listening port")
		}
		t.Logf("port-forward up: %s", got.line)
		return got.port, stop
	case <-time.After(2 * time.Minute):
		stop()
		t.Fatal("kubectl port-forward never reported a listening port")
	}
	return "", stop
}

// capturePresentedCertificate opens one deliberately unverified handshake
// to learn what the server serves.
//
// This connection is used for nothing else. Its only product is the leaf
// certificate, which becomes the trust anchor for the verified connection
// the caller then makes. The reason it has to exist is written out at the
// caller.
func capturePresentedCertificate(t *testing.T, port string) *x509.Certificate {
	t.Helper()

	conn, err := tls.Dial("tcp", "127.0.0.1:"+port, &tls.Config{
		// #nosec G402 -- deliberate, and load bearing: this handshake
		// exists only to read the certificate the controller generated
		// inside the cluster, which no out-of-band channel can reach. The
		// request that carries the assertion is made on a second,
		// fully verified connection anchored on what this one returns.
		InsecureSkipVerify: true,
		MinVersion:         tls.VersionTLS12,
	})
	if err != nil {
		t.Fatalf("TLS handshake with the controller through the port-forward: %v", err)
	}
	defer func() { _ = conn.Close() }()

	chain := conn.ConnectionState().PeerCertificates
	if len(chain) == 0 {
		t.Fatal("the controller presented no certificate")
	}
	return chain[0]
}

// assertBootstrapAdminWorksThroughKubectlExec runs the exact command
// NOTES.txt prints after an install.
//
// This is the step that makes the install usable at all. Before local
// password authentication existed, the only way into the UI was a JWT an
// operator on a fresh cluster had no way to obtain, so a green install was
// a control plane nobody could sign into.
func assertBootstrapAdminWorksThroughKubectlExec(t *testing.T, root, kubeconfig string) {
	t.Helper()

	out := mustRunPackagingTool(t, root, kubeEnv(kubeconfig), kindAdminPassword+"\n",
		"kubectl", "exec", "-i", "-n", helmNamespace,
		"deploy/"+helmFullname+"-controller", "--",
		"/app/controller", "bootstrap-admin", "--email", kindAdminEmail, "--password-stdin")
	if !strings.Contains(out, "is ready") {
		t.Fatalf("bootstrap-admin through kubectl exec did not report success:\n%s", out)
	}
	if strings.Contains(out, kindAdminPassword) {
		t.Error("bootstrap-admin echoed the password into the terminal")
	}
	t.Logf("bootstrap-admin: %s", strings.TrimSpace(out))
}
