// Release gate for the build methods: through the real binary, against a
// real sshd, a real Docker engine (the engine host fixture, onboarded as a
// linux_server) and a real registry it pushes to. Unpinned references are
// offenders; a check of a refresh writes nothing; a refresh pins each to
// the digest the registry serves; a resolve then finds no drift; a
// rollback writes every file back byte for byte; and a lockfile is
// verified, then caught short.
package main_test

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
)

// buildRegistryImage is the registry the gate pushes to.
const buildRegistryImage = "registry:2.8.3"

// TestCLI_BuildPinsRefreshesRollsBackAndVerifiesLocks is the release gate.
func TestCLI_BuildPinsRefreshesRollsBackAndVerifiesLocks(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a real registry, Docker engine and sshd")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	net, err := network.New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = net.Remove(context.Background()) })
	reg, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:          buildRegistryImage,
			Networks:       []string{net.Name},
			NetworkAliases: map[string][]string{net.Name: {"registry"}},
			WaitingFor:     wait.ForLog("listening on").WithStartupTimeout(testsupport.ContainerStartupTimeout),
		},
		Started: true,
	})
	testcontainers.CleanupContainer(t, reg)
	if err != nil {
		t.Fatalf("starting the registry: %v", err)
	}
	d := testsupport.StartDockerHost(t, testsupport.EngineHostOptions{
		Env:      map[string]string{"DOCKERD_EXTRA_ARGS": "--insecure-registry=registry:5000"},
		Networks: []string{net.Name},
	})
	d.StartContainer(t, containerGateImage, "")
	const ref = "registry:5000/pleiades/base:1"
	if out, err := d.RootExec(ctx, "sh", "-c", "docker tag "+containerGateImage+" "+ref+" && docker push -q "+ref); err != nil {
		t.Fatalf("pushing %s: %v\n%s", ref, err, out)
	}
	// What the tag names is what the registry serves for it, so the test
	// asks the registry, not the engine the method asks: a manifest's
	// digest is the SHA-256 of the exact bytes served for it. (The local
	// image's RepoDigests is not it: an engine holding an index pushes
	// what it has of it, which is a different document.)
	const accept = "application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json"
	served, err := d.RootExec(ctx, "sh", "-c", "wget -q -O- --header 'Accept: "+accept+"' http://registry:5000/v2/pleiades/base/manifests/1 | sha256sum")
	if err != nil {
		t.Fatalf("asking the registry for %s: %v\n%s", ref, err, served)
	}
	sum, _, _ := strings.Cut(strings.TrimSpace(served), " ")
	if len(sum) != 64 {
		t.Fatalf("the registry's manifest for %s hashed to %q", ref, served)
	}
	pushed := "sha256:" + sum

	dockerfile := "FROM " + ref + "\nRUN true\n"
	compose := "services:\n  app:\n    image: " + ref + "\n"
	pkg := `{"dependencies":{"left-pad":"^1.3.0"}}`
	lock := `{"lockfileVersion":3,"packages":{"":{"dependencies":{"left-pad":"^1.3.0"}},"node_modules/left-pad":{"version":"1.3.0","resolved":"https://registry.npmjs.org/left-pad/-/left-pad-1.3.0.tgz","integrity":"sha512-AAAA"}}}`
	setup := "mkdir -p /srv/app && cd /srv/app && " +
		"printf '%s' " + shq(dockerfile) + " > Dockerfile && printf '%s' " + shq(compose) + " > compose.yaml && " +
		"printf '%s' " + shq(pkg) + " > package.json && printf '%s' " + shq(lock) + " > package-lock.json && " +
		"chmod 0640 Dockerfile && chown -R " + testsupport.DockerHostUser + " /srv/app"
	if out, err := d.RootExec(ctx, "sh", "-c", setup); err != nil {
		t.Fatalf("writing the project: %v\n%s", err, out)
	}
	onDevice := func(file string) string {
		out, err := d.RootExec(ctx, "cat", "/srv/app/"+file)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}

	dir := t.TempDir()
	if out, err := runPleiades(t, dir, "init"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	knownHosts, err := d.KnownHosts(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"PLEIADES_KNOWN_HOSTS": knownHosts}
	if out, err := runPleiades(t, dir, "add-host", "buildbox", "--type", "linux_server", "--set", "host="+d.Host, "--set", "port="+strconv.Itoa(d.Port)); err != nil {
		t.Fatalf("add-host: %v\n%s", err, out)
	}
	if out, err := runPleiadesWithStdin(t, dir, testsupport.DockerHostPassword, "add-credential", "buildbox", "--username", testsupport.DockerHostUser, "--password-stdin"); err != nil {
		t.Fatalf("add-credential: %v\n%s", err, out)
	}
	onboardJSON(t, dir, env, "buildbox")

	runbook := func(id, body string) string {
		t.Helper()
		return writeRunbook(t, dir, id, "id: "+id+"\nhosts: buildbox\ntasks:\n  - name: "+id+"\n"+body)
	}
	report := func(args ...string) jsonReport {
		t.Helper()
		out, _ := runPleiadesWithEnv(t, dir, env, append(args, "--json")...)
		var r jsonReport
		if err := json.NewDecoder(strings.NewReader(out)).Decode(&r); err != nil {
			t.Fatalf("pleiades %v printed %q: %v", args, out, err)
		}
		return r
	}
	files := "      files: [/srv/app/Dockerfile, /srv/app/compose.yaml]\n"

	// Unpinned references are offenders, named.
	check := report("run", runbook("pins", "    build.digests.check:\n"+files))
	if check.Outcome.ExitCode == 0 || !strings.Contains(check.Tasks[0].Error, "Dockerfile:1 "+ref+": it names a tag but no digest") {
		t.Fatalf("an unpinned check: %+v", check.Tasks)
	}

	// The files belong to the login but to root's group, which the login
	// cannot give a new file: a refresh must keep both, and the mode.
	attrs := func() string {
		out, err := d.RootExec(ctx, "stat", "-c", "%U:%G %a", "/srv/app/Dockerfile", "/srv/app/compose.yaml")
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	owned := attrs()
	if !strings.Contains(owned, testsupport.DockerHostUser+":root 640") {
		t.Fatalf("the project's files are %q, want the Dockerfile %s:root 640", owned, testsupport.DockerHostUser)
	}

	// A check of the refresh resolves and writes nothing, and says what it
	// would change: each file's reference, to the digest the registry
	// serves, marked as a prediction. Without this the step proved only
	// that nothing was written, which a check that never ran also does.
	pinnedRef := ref + "@" + pushed
	refresh := runbook("refresh", "    build.digests.refresh:\n"+files)
	checked := report("run", refresh, "--mode", "check")
	if checked.Outcome.ExitCode != 0 || onDevice("Dockerfile") != dockerfile {
		t.Fatalf("a checked refresh: exit %d, Dockerfile %q", checked.Outcome.ExitCode, onDevice("Dockerfile"))
	}
	predicted, _ := checked.Tasks[0].Stats["changes"].([]any)
	if checked.Tasks[0].Stats["predicted"] != true || len(predicted) != 2 {
		t.Fatalf("a checked refresh predicted %v (predicted: %v), want both files' changes", predicted, checked.Tasks[0].Stats["predicted"])
	}
	for _, c := range predicted {
		if change, _ := c.(map[string]any); change["from"] != ref || change["to"] != pinnedRef {
			t.Errorf("a predicted change %v, want %s to %s", c, ref, pinnedRef)
		}
	}

	// The refresh pins each to what the registry serves, keeping the mode.
	ran := report("run", refresh)
	if ran.Outcome.ExitCode != 0 || onDevice("Dockerfile") != "FROM "+pinnedRef+"\nRUN true\n" || !strings.Contains(onDevice("compose.yaml"), "image: "+pinnedRef+"\n") {
		t.Fatalf("the refresh to %s: exit %d, Dockerfile %q, compose %q, tasks %+v", pinnedRef, ran.Outcome.ExitCode, onDevice("Dockerfile"), onDevice("compose.yaml"), ran.Tasks)
	}
	if got := attrs(); got != owned {
		t.Errorf("the refresh left the files' owner, group and mode %q, want %q", got, owned)
	}

	// Pinned now, and the tag still names the pin.
	resolved := report("run", runbook("resolved", "    build.digests.check:\n"+files+"      resolve: true\n"))
	if resolved.Outcome.ExitCode != 0 {
		t.Fatalf("a resolve after the refresh: %+v", resolved.Tasks)
	}

	// The rollback writes every file back as it was.
	out, _ := runPleiadesWithEnv(t, dir, env, "rollback", ran.RunID, "--json")
	var rolled struct {
		Rollback struct {
			Problems []struct{ Node, Reason string } `json:"problems"`
		} `json:"rollback"`
		Outcome struct {
			ExitCode int `json:"exit_code"`
		} `json:"outcome"`
	}
	if err := json.NewDecoder(strings.NewReader(out)).Decode(&rolled); err != nil || rolled.Outcome.ExitCode != 0 {
		t.Fatalf("the rollback: %v, %+v\n%s", err, rolled, out)
	}
	if onDevice("Dockerfile") != dockerfile || onDevice("compose.yaml") != compose || attrs() != owned {
		t.Errorf("after the rollback: Dockerfile %q, compose %q, attributes %q", onDevice("Dockerfile"), onDevice("compose.yaml"), attrs())
	}
	// The rollback spent the refresh's undo record (keeping one of its
	// own, since writing the references back is a change too).
	inverse, _ := ran.Tasks[0].Stats["inverse"].(map[string]any)
	undoParams, _ := inverse["params"].(map[string]any)
	id, _ := undoParams["undo"].(string)
	if left, _ := d.RootExec(ctx, "sh", "-c", "ls /home/"+testsupport.DockerHostUser+"/.local/state/pleiades/build-digests"); id == "" || strings.Contains(left, id) {
		t.Errorf("the refresh's undo record %q is still there after the rollback: %q", id, left)
	}

	// The lockfile verifies, then is caught short.
	verify := runbook("lock", "    build.lock.verify:\n      lockfile: /srv/app/package-lock.json\n")
	if v := report("run", verify); v.Outcome.ExitCode != 0 {
		t.Fatalf("a clean lockfile: %+v", v.Tasks)
	}
	if _, err := d.RootExec(ctx, "sed", "-i", `s/,"integrity":"sha512-AAAA"//`, "/srv/app/package-lock.json"); err != nil {
		t.Fatal(err)
	}
	if v := report("run", verify); v.Outcome.ExitCode == 0 || !strings.Contains(v.Tasks[0].Error, "left-pad@1.3.0: it has no integrity hash") {
		t.Errorf("a lockfile with no integrity: %+v", v.Tasks)
	}

	if doc, err := runPleiades(t, dir, "doc", "build.digests.refresh"); err != nil || !strings.Contains(doc, "Pins every container image reference") {
		t.Errorf("pleiades doc build.digests.refresh: %v\n%s", err, doc)
	}
}

// shq single-quotes text for sh.
func shq(text string) string { return "'" + strings.ReplaceAll(text, "'", `'\''`) + "'" }
