//go:build devtools

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The tests here cover the three decisions this tool makes and nothing else.
// Every removal it performs is a docker command, and a test that ran docker
// would be testing docker; what can be wrong in this file is which containers
// are judged finished, which are protected, and whether a live run is
// noticed. Those are pure functions on purpose, so they can be tested at all.

// TestAbandonedIsDecidedByTheReaperNotByAgeOrCount is the rule this tool
// exists to get right.
func TestAbandonedIsDecidedByTheReaperNotByAgeOrCount(t *testing.T) {
	// Two sessions, one live and one finished, exactly the shape a machine
	// running one test binary while an older one's leftovers sit beside it
	// really has.
	containers := []container{
		{Name: "reaper_live", Session: "live", Image: "testcontainers/ryuk:0.14.0", Running: true},
		{Name: "nats_a", Session: "live", Image: "nats:2.14.4-alpine", Running: true},
		{Name: "postgres_a", Session: "live", Image: "postgres:15.19-alpine", Running: false},

		{Name: "reaper_dead", Session: "dead", Image: "testcontainers/ryuk:0.14.0", Running: false},
		{Name: "nats_b", Session: "dead", Image: "nats:2.14.4-alpine", Running: true},
		{Name: "sshd_b", Session: "dead", Image: "openssh-server", Running: false},
	}

	got := names(abandoned(containers))
	want := []string{"reaper_dead", "nats_b", "sshd_b"}
	assertSameSet(t, got, want)

	// The two claims worth stating separately, because each is a rule that
	// looks reasonable and is wrong.
	//
	// A STOPPED container in a live session is not abandoned. Tests stop and
	// start containers; a rule that removed stopped ones would take
	// postgres_a out from under a running test.
	if contains(got, "postgres_a") {
		t.Error("a stopped container in a live session was judged abandoned; " +
			"tests stop containers, so stopped is not a statement about the owning run")
	}
	// A RUNNING container in a finished session IS abandoned. This is the
	// leak that matters: a reaper that died leaves its containers running
	// forever, and a rule keyed on running-ness would never collect them.
	if !contains(got, "nats_b") {
		t.Error("a running container whose reaper is gone was not judged abandoned; " +
			"that is precisely the leak this tool exists to clear")
	}
}

// TestAbandonedLeavesUnattributableContainersAlone covers the case where the
// honest answer is to do nothing.
func TestAbandonedLeavesUnattributableContainersAlone(t *testing.T) {
	containers := []container{
		{Name: "somebody_elses", Session: "", Image: "redis:7", Running: false},
	}
	if got := abandoned(containers); len(got) != 0 {
		t.Errorf("a container with no session id was judged abandoned: %v; "+
			"with no session there is no evidence about an owner either way", names(got))
	}
}

// TestAbandonedWithNoReaperAtAllTakesEverything is the daemon-restart case.
//
// After a docker daemon restart the reapers are gone and their containers are
// not, so every session looks finished. That is the correct reading: no
// process is holding any of them.
func TestAbandonedWithNoReaperAtAllTakesEverything(t *testing.T) {
	containers := []container{
		{Name: "nats_a", Session: "one", Running: true},
		{Name: "nats_b", Session: "two", Running: true},
	}
	assertSameSet(t, names(abandoned(containers)), []string{"nats_a", "nats_b"})
}

// TestProtectedContainerHoldsForAnyForeignCluster checks the guard is written
// against what a container is rather than against the one name on the machine
// it was written on.
func TestProtectedContainerHoldsForAnyForeignCluster(t *testing.T) {
	for _, tc := range []struct {
		name      string
		container container
		protected bool
	}{
		{"the developer's own cluster", container{Name: "desktop-control-plane", KindCluster: "desktop"}, true},
		{"a cluster nobody has named yet", container{Name: "whatever-control-plane", KindCluster: "some-other-cluster"}, true},
		{"Docker Desktop's provider", container{Name: "kind-cloud-provider"}, true},
		{"Docker Desktop's mirror", container{Name: "kind-registry-mirror"}, true},
		{"our own throwaway node", container{Name: kindClusterPrefix + "-4242-control-plane", KindCluster: kindClusterPrefix + "-4242"}, false},
		{"an ordinary test container", container{Name: "nats_a", Session: "one"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reason, got := protectedContainer(tc.container)
			if got != tc.protected {
				t.Fatalf("protectedContainer(%+v) = %v, want %v", tc.container, got, tc.protected)
			}
			if got && reason == "" {
				t.Error("a container was protected with no reason given; the reason is what gets printed")
			}
		})
	}
}

// TestLooksLikeATestRunIgnoresThisToolIsTheNegativeControl.
//
// This tool is started with `go run tools/breakglass/main.go`, which contains
// neither "go test" nor "make ci" but does start with "go ". Without the
// explicit exclusion it would still be caught by the trailing .test check on
// a bad day, and a break-glass that refuses because it is itself running is
// a break-glass nobody can use. The rest of the table is here so the
// exclusion cannot be widened without something failing.
func TestLooksLikeATestRun(t *testing.T) {
	for _, tc := range []struct {
		cmdline string
		want    bool
	}{
		{"go test -tags integration -race -count=1 -timeout 20m ./...", true},
		{"make ci", true},
		{"make push-gate", true},
		{"/tmp/go-build123/b001/e2e.test -test.run TestPackagingReleaseGate", true},
		{"go run ./tools/testgate", true},

		{"go run tools/breakglass/main.go", false},
		{"go run tools/breakglass/main.go -force", false},
		{"go build ./...", false},
		{"gopls -listen auto", false},
		{"docker compose up", false},
		{"", false},
	} {
		if got := looksLikeATestRun(tc.cmdline); got != tc.want {
			t.Errorf("looksLikeATestRun(%q) = %v, want %v", tc.cmdline, got, tc.want)
		}
	}
}

// TestUnderRejectsASiblingDirectory covers the check that keeps another
// module's `go test` from blocking a clean here.
func TestUnderRejectsASiblingDirectory(t *testing.T) {
	for _, tc := range []struct {
		root, path string
		want       bool
	}{
		{"/home/x/repo", "/home/x/repo", true},
		{"/home/x/repo", "/home/x/repo/tests/e2e", true},
		{"/home/x/repo", "/home/x/repo-other", false},
		{"/home/x/repo", "/home/x", false},
		{"/home/x/repo", "/tmp", false},
	} {
		if got := under(tc.root, tc.path); got != tc.want {
			t.Errorf("under(%q, %q) = %v, want %v", tc.root, tc.path, got, tc.want)
		}
	}
}

// TestParseContainersSurvivesEmptyLabels is the shape docker really returns.
//
// An unset label renders as an empty field rather than being omitted, so the
// line still has its tabs. A parser that split on whitespace instead of tabs
// would lose the field count and drop the row entirely, which would read as
// "there is nothing to clean."
func TestParseContainersParsesRealDockerOutput(t *testing.T) {
	out := strings.Join([]string{
		"nats_a\tnats:2.14.4-alpine\trunning\tabc123\t",
		"desktop-control-plane\tkindest/node:v1.36.1\trunning\t\tdesktop",
		"",
	}, "\n")

	got := parseContainers(out)
	if len(got) != 2 {
		t.Fatalf("parsed %d containers, want 2: %+v", len(got), got)
	}
	if got[0].Session != "abc123" || !got[0].Running {
		t.Errorf("first row parsed as %+v", got[0])
	}
	if got[1].KindCluster != "desktop" || got[1].Session != "" {
		t.Errorf("second row parsed as %+v; an unset label is an empty field, not a missing one", got[1])
	}
}

// TestTheNamesMatchTheCodeThatCreatesThem is the drift guard.
//
// This tool cleans up by literal name. A name that changed in the code that
// creates it, and not here, produces a tool that reports a clean machine
// while the state it was supposed to remove is still there, which is worse
// than no tool at all. So the constants are checked against the files that
// define them rather than trusted.
func TestTheNamesMatchTheCodeThatCreatesThem(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatalf("locating the repository root: %v", err)
	}

	for _, tc := range []struct {
		file  string
		needs string
	}{
		{filepath.Join("tests", "e2e", "packaging_kind_test.go"), `kindClusterPrefix = "` + kindClusterPrefix + `"`},
		{filepath.Join("tests", "e2e", "packaging_support_test.go"), `"` + builtImages[0] + `"`},
		{filepath.Join("tests", "e2e", "packaging_support_test.go"), `"` + builtImages[1] + `"`},
		{filepath.Join("internal", "testsupport", "ansible_image.go"), `= "` + builtImages[2] + `"`},
	} {
		body, err := os.ReadFile(filepath.Join(root, tc.file))
		if err != nil {
			t.Errorf("reading %s: %v", tc.file, err)
			continue
		}
		if !strings.Contains(string(body), tc.needs) {
			t.Errorf("%s no longer contains %s, so this tool is cleaning up a name nothing creates",
				tc.file, tc.needs)
		}
	}

	// The compose project name is derived by compose from the directory, so
	// the check is that the directory really is what the constant says.
	if base := filepath.Base(root); base != composeProject {
		t.Errorf("the repository directory is %q but composeProject is %q; compose derives the "+
			"project name from the directory when docker-compose.yml sets no name:, so this tool "+
			"would take down a project that does not exist", base, composeProject)
	}
}

// TestOnlyAnAbandonedClusterIsReclaimable is the rule that replaced the
// delete-on-sight this tool inherited from the gate.
//
// The live case is the one that matters and it is asserted against THIS
// process, whose id is by definition running, so the test cannot pass by
// picking a number that happens to be free.
func TestOnlyAnAbandonedClusterIsReclaimable(t *testing.T) {
	live := fmt.Sprintf("%s-%d", kindClusterPrefix, os.Getpid())
	if _, ok := clusterIsReclaimable(live); ok {
		t.Errorf("%q was judged reclaimable while its owning process is this very test; "+
			"deleting a live run's cluster is the incident this rule exists to prevent", live)
	}

	// An id that cannot be running. Process 0 is not a user process on any
	// system this builds for, so no reuse can make it look alive.
	dead := kindClusterPrefix + "-0"
	if _, ok := clusterIsReclaimable(dead); ok {
		t.Errorf("%q was judged reclaimable, but pid 0 names no user process, so this should be left alone "+
			"as unattributable rather than deleted", dead)
	}

	for _, tc := range []struct {
		name        string
		reclaimable bool
	}{
		{kindClusterPrefix + "-999999999", true}, // a plausible id that is not running
		{"desktop", false},                       // a developer's own cluster
		{kindClusterPrefix, false},               // the OLD constant name, which now belongs to nobody
		{kindClusterPrefix + "-notanumber", false},
	} {
		_, ok := clusterIsReclaimable(tc.name)
		if ok != tc.reclaimable {
			t.Errorf("clusterIsReclaimable(%q) = %v, want %v", tc.name, ok, tc.reclaimable)
		}
	}
}

func names(containers []container) []string {
	var out []string
	for _, c := range containers {
		out = append(out, c.Name)
	}
	return out
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

func assertSameSet(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for _, w := range want {
		if !contains(got, w) {
			t.Fatalf("got %v, want %v (missing %q)", got, want, w)
		}
	}
}
