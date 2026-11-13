// Package main_test: the Walk-tier release gate for a capability an
// external program defines (Phase 127a): the grant the Controller ships
// with a dispatch is held by the Runner to the definition it loaded.
package main_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/loader"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// TestExternalCapabilityReleaseGate_TheRunnerHoldsAShippedGrant: a real
// Runner, over real NATS, rebuilds the dispatched linux_server with the
// grant the Controller shipped, and the program's method finds its
// notebook there and writes the note on a real sshd; a grant whose
// properties do not fit the definition grants nothing, and the task is
// refused naming the capability. A grant probed by another platform's
// build of the same source holds (a mixed fleet), and one made against a
// revision the author has since bumped does not, naming why.
func TestExternalCapabilityReleaseGate_TheRunnerHoldsAShippedGrant(t *testing.T) {
	if testing.Short() {
		t.Skip("starts NATS and sshd")
	}
	t.Cleanup(collection.SnapshotForTest())
	t.Cleanup(capability.SnapshotForTest())
	collections := buildApprovedExample(t)
	// Built before the harness gives the Runner a home of its own, so these
	// builds use this machine's module cache rather than filling a fresh one.
	other, darwin := otherBuildsOfTheExample(t, collections)
	if _, err := loader.Load(context.Background(), collections, loader.Options{}); err != nil {
		t.Fatalf("loading the approved example: %v", err)
	}
	h := newReleaseGateHarnessFor(t, knownHostsInEnvironment, map[string]string{
		"notebook.yaml": "id: notebook\ntasks:\n  - name: a note\n    example.notebook.write:\n      name: walk\n      content: through the runner\n",
	})
	addr := net.JoinHostPort(h.sshHost, strconv.Itoa(h.sshPort))
	notes := readOverSSH(t, addr, `mkdir -p "$HOME/notes" && cd "$HOME/notes" && pwd`)

	def, ok := capability.DataOf("example.notebook")
	if !ok {
		t.Fatal("the loaded example defines no example.notebook")
	}
	digest := capability.DefinitionDigest(def)
	dispatchGrants := func(grants ...inventory.Grant) []wire.JobEvent {
		t.Helper()
		granted := inventory.GrantsProperty(grants)
		return h.dispatchEvents(t, wire.DispatchPayload{
			JobID: uuid.New().String(), RunbookID: "notebook",
			DeviceID: "release-gate-device", DeviceName: "release-gate-device", DeviceHost: h.sshHost, SSHPort: h.sshPort,
			DeviceType: "linux_server",
			DeviceProperties: map[string]any{
				"host": h.sshHost, "port": h.sshPort, inventory.GrantedProperty: granted,
			},
			Capabilities: []capability.Name{capability.NameSSHTransport, capability.NamePOSIXFileSystem, "example.notebook"},
			Secrets:      credential.Flatten(credential.Credential{Username: releaseGateSSHUser, Password: releaseGateSSHPassword}),
		})
	}
	dispatchGrant := func(g inventory.Grant) wire.JobEvent {
		t.Helper()
		events := dispatchGrants(g)
		return events[len(events)-1]
	}
	dispatch := func(props map[string]any) wire.JobEvent {
		t.Helper()
		return dispatchGrant(inventory.Grant{
			Capability: "example.notebook", Source: inventory.GrantAsserted, Properties: props,
			By: "ops", Reason: "the release gate made the directory", At: time.Now(), Definition: digest,
		})
	}

	evt := dispatch(map[string]any{"dir": notes})
	if evt.Status != "changed" {
		t.Fatalf("the job did not write the note: %+v", evt)
	}
	if got := readOverSSH(t, addr, "cat "+notes+"/walk"); got != "through the runner" {
		t.Fatalf("the note holds %q", got)
	}

	evt = dispatch(map[string]any{})
	if evt.Status != "failed" || !strings.Contains(evt.EventData.Message, "example.notebook") {
		t.Errorf("a grant missing its required property: %+v", evt)
	}

	// A mixed fleet: the grant was probed by other builds of the same source
	// (another build's describe gives its definition, and a macOS build's
	// digest is its recorded provenance), and the Runner, running its own
	// build, holds it, since a grant is bound to the definition and never to
	// a program's digest.
	probed := inventory.Grant{
		Capability: "example.notebook", Source: inventory.GrantProbed, Program: "note",
		Properties: map[string]any{"dir": notes}, At: time.Now(), Definition: other, Build: darwin,
	}
	if evt := dispatchGrant(probed); evt.Status != "ok" && evt.Status != "changed" {
		t.Errorf("a grant another platform's build probed did not hold on this Runner: %+v", evt)
	}

	// The author bumping the definition's revision withdraws it: the task
	// is refused naming the capability, the program and what to do.
	bumped := def
	bumped.Revision++
	probed.Definition = capability.DefinitionDigest(bumped)
	evt = dispatchGrant(probed)
	for _, want := range []string{"example.notebook", `program "note"`, "onboard it again"} {
		if evt.Status != "failed" || !strings.Contains(evt.EventData.Message, want) {
			t.Errorf("a grant made against another revision: %+v, want a failure saying %q", evt, want)
		}
	}

	// A grant of a capability no program this Runner loaded defines grants
	// nothing; the job still runs, and the Runner's validation warns of it
	// in the job's log, as `pleiades validate` and `pleiades run` do.
	held := inventory.Grant{
		Capability: "example.notebook", Source: inventory.GrantAsserted, Properties: map[string]any{"dir": notes},
		By: "ops", Reason: "the release gate made the directory", At: time.Now(), Definition: digest,
	}
	gone := inventory.Grant{Capability: "gone.thing", Source: inventory.GrantAsserted, By: "ops", Reason: "a removed program", At: time.Now(), Definition: digest}
	events := dispatchGrants(held, gone)
	if final := events[len(events)-1]; final.Status != "ok" && final.Status != "changed" {
		t.Errorf("the job beside a grant no program defines: %+v", final)
	}
	warned := false
	for _, e := range events {
		warned = warned || (e.Task == "task.warning" && strings.Contains(e.EventData.Message, "grant of gone.thing grants nothing here: no loaded program defines gone.thing"))
	}
	if !warned {
		t.Errorf("the job log %+v holds no warning about the grant of gone.thing", events)
	}
}

// otherBuildsOfTheExample builds examples/external_collection twice more,
// as other machines would: once for this platform with other flags, whose
// describe gives the definition digest a command line there would have
// recorded, and once for macOS on arm64, whose digest a grant probed there
// would name as its build. It fails unless all three builds (the Runner's,
// in runnerDir, among them) differ.
func otherBuildsOfTheExample(t *testing.T, runnerDir string) (definition, darwinBuild string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	build := exec.Command("go", "build", "-trimpath", "-ldflags=-s -w", "-o", filepath.Join(dir, "note"), "../../examples/external_collection")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building another build of the example: %v\n%s", err, out)
	}
	darwinPath := filepath.Join(t.TempDir(), "note-darwin")
	cross := exec.Command("go", "build", "-o", darwinPath, "../../examples/external_collection")
	cross.Env = append(os.Environ(), "GOOS=darwin", "GOARCH=arm64", "CGO_ENABLED=0")
	if out, err := cross.CombinedOutput(); err != nil {
		t.Fatalf("building the example for macOS: %v\n%s", err, out)
	}
	otherDigest, desc, err := loader.Inspect(context.Background(), dir, "note", loader.Options{})
	if err != nil {
		t.Fatalf("describing the other build: %v", err)
	}
	darwinBuild = fileDigest(t, darwinPath)
	runnerDigest := fileDigest(t, filepath.Join(runnerDir, "note"))
	if otherDigest == darwinBuild || otherDigest == runnerDigest || darwinBuild == runnerDigest {
		t.Fatalf("two of the builds share a digest (runner %s, other %s, macOS %s), so this case proves nothing", runnerDigest, otherDigest, darwinBuild)
	}
	for _, d := range desc.Capabilities {
		if d.Name == "example.notebook" {
			// The provider is the loader's own name for the program, its file
			// name, as register sets it.
			d.Provider = "note"
			return capability.DefinitionDigest(d), darwinBuild
		}
	}
	t.Fatal("the other build defines no example.notebook")
	return "", ""
}

// fileDigest is a file's SHA-256 as a program build's digest is written:
// "sha256:" and lower case hex.
func fileDigest(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path) // #nosec G304 -- a build this test made in its own directory
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}
