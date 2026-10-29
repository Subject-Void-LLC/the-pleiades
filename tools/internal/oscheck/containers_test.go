// Tests for the container pass a machine with Podman runs: the script's
// contents, and the script itself under /bin/sh with stand-ins for podman and
// go, so what it sets, starts and cleans up is seen as it happens.
package oscheck

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var containerPosix = POSIX{Tool: "maccheck", Machine: "the Mac", Dir: ".pleiades-maccheck", Path: "/opt/homebrew/bin", Wrap: "caffeinate -dims ",
	Files: "files", Containers: &Containers{Socket: ".pleiades-podman/podman.sock"}}

func TestContainerTestScript_Contents(t *testing.T) {
	s := containerPosix.ContainerTestScript("abc123", 0, []string{"github.com/x/a", "github.com/x/b"})
	for _, w := range []string{
		`cd "$HOME/.pleiades-maccheck/abc123" || exit 2`,
		`podman machine start`,
		`export DOCKER_HOST="unix://$HOME/.pleiades-podman/podman.sock" TESTCONTAINERS_RYUK_DISABLED=true`,
		"caffeinate -dims /bin/sh -c 'go run ./tools/testgate -full -p 1 -coverage-out .pleiades-coverage-containers.json -report-out .pleiades-record-containers.json github.com/x/a github.com/x/b' &",
		`trap 'stop_run "$job"; rm -f .maccheck-containers.pid; done_; exit 143' TERM HUP INT PIPE`,
	} {
		if !strings.Contains(s, w) {
			t.Errorf("script does not hold %q:\n%s", w, s)
		}
	}
	// Pleiades renders a template brace in a command before the machine sees
	// it, so none may reach it.
	if strings.Contains(s, "{{") || strings.Contains(s, "}}") {
		t.Errorf("the script holds a template brace, which the engine would render:\n%s", s)
	}
	if !strings.Contains(s, "podman.sock") {
		t.Error("the socket link's name must hold podman.sock, or testcontainers asks for a network called bridge")
	}
	if got := containerPosix.ContainerTestScript("abc", 3, []string{"a/b"}); !strings.Contains(got, "-full -p 3 ") {
		t.Errorf("an explicit parallelism is not used:\n%s", got)
	}
	if !strings.Contains(s, `export DOCKER_CONFIG="$cfg"`) || strings.Contains(s, "/.docker") {
		t.Errorf("the pass does not use a docker configuration of its own, or touches the login's ~/.docker:\n%s", s)
	}
	shared := containerPosix
	shared.Containers = &Containers{Socket: ".pleiades-podman/podman.sock", Share: true}
	if got := shared.ContainerTestScript("abc", 0, []string{"a/b"}); !strings.Contains(got, "testgate -full -host podman -p 1 ") {
		t.Errorf("a shared pass does not run Podman's share alone:\n%s", got)
	}
	if s := containerPosix.ContainerStopScript("abc"); strings.Contains(s, "{{") || strings.Contains(s, "}}") {
		t.Errorf("the stop script holds a template brace:\n%s", s)
	}
}

// The two passes run in one tree at once, so each stops only its own run.
func TestPIDFile_TheContainerPassHasItsOwn(t *testing.T) {
	if a, b := posix.PIDFile(), containerPosix.PIDFile(); a == b {
		t.Fatalf("both passes write their job to %s", a)
	}
	if s := containerPosix.StopScript("abc"); !strings.Contains(s, containerPosix.PIDFile()) {
		t.Errorf("the container pass's stop does not read its own job:\n%s", s)
	}
}

func TestRecordsScript_ReadsTheContainerRecordForAContainerRun(t *testing.T) {
	if s := containerPosix.RecordsScript("abc"); !strings.Contains(s, ContainerRecordFile) || strings.Contains(s, RecordFiles[0]) {
		t.Errorf("a container run reads its own record and no other:\n%s", s)
	}
	if s := posix.RecordsScript("abc"); strings.Contains(s, ContainerRecordFile) {
		t.Errorf("a container-free run must not read the container record:\n%s", s)
	}
}

// podmanHome is a fake home for running the container script: stand-ins for
// podman and go first on the PATH, recording what they are asked.
type podmanHome struct {
	home string
	x    POSIX
}

func newPodmanHome(t *testing.T, running bool) podmanHome {
	t.Helper()
	needsSh(t)
	home := t.TempDir()
	bin := filepath.Join(home, "bin")
	real := filepath.Join(home, "machine-api.sock")
	for _, dir := range []string{bin, filepath.Join(home, "trees", "abc")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(real, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	state := "Never"
	if running {
		state = "Currently running"
	}
	podman := `#!/bin/sh
echo "podman $*" >> "` + home + `/podman.calls"
case "$1 $2" in
"machine list") echo "NAME VM TYPE CREATED LAST UP"; echo "m applehv now ` + state + `" ;;
"machine inspect") printf '[{"ConnectionInfo": {\n "PodmanSocket": {\n  "Path": "` + real + `"\n },\n "PodmanPipe": null}}]\n' ;;
"ps -aq") case "$*" in *org.testcontainers*) echo c-test ;; esac ;;
"volume ls") echo named-data; echo ` + strings.Repeat("ab", 32) + ` ;;
"images ") echo "REPOSITORY TAG IMAGE ID CREATED SIZE"
	echo "docker.io/library/nats 2.14.4-alpine id-nats 3 months 27 MB"
	echo "docker.io/pleiades/telnetd 3.20.10 id-telnetd 1 hour 8 MB"
	echo "ghcr.io/notconf/notconf <none> id-notconf 4 months 199 MB"
	echo "docker.io/library/debian bookworm-slim id-debian 8 days 100 MB"
	echo "<none> <none> id-dangling 2 days 8 MB" ;;
esac
exit 0
`
	goStand := `#!/bin/sh
echo "go DOCKER_HOST=$DOCKER_HOST RYUK=$TESTCONTAINERS_RYUK_DISABLED $*" >> "` + home + `/go.calls"
if [ "$2" = ./tools/testimages ]; then echo nats:2.14.4-alpine; echo ghcr.io/notconf/notconf@sha256:9ef5; exit 0; fi
[ -f "` + home + `/go.exit" ] && exit "$(cat "` + home + `/go.exit")"
echo '{"coverage":{}}' > .pleiades-coverage-containers.json
echo '{"schema":1,"kind":"testgate","label":"full","verdict":"pass"}' > .pleiades-record-containers.json
exit 0
`
	for name, body := range map[string]string{"podman": podman, "go": goStand} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o700); err != nil { // #nosec G306 -- a test's own stand-in program
			t.Fatal(err)
		}
	}
	x := POSIX{Tool: "xcheck", Dir: "trees", Path: bin, Containers: &Containers{Socket: ".pleiades-podman/podman.sock"}}
	return podmanHome{home: home, x: x}
}

func (h podmanHome) run(t *testing.T) (string, int) {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", h.x.ContainerTestScript("abc", 0, []string{"a/b"}))
	cmd.Env = append(os.Environ(), "HOME="+h.home, "PATH="+h.x.Path+":/usr/bin:/bin")
	out, err := cmd.CombinedOutput()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("running the script: %v\n%s", err, out)
	}
	return string(out), code
}

func (h podmanHome) read(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(h.home, name))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(b)
}

func TestContainerTestScript_RunsTheTestsAgainstPodmanAndCleansUp(t *testing.T) {
	h := newPodmanHome(t, true)
	out, code := h.run(t)
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	link, err := os.Readlink(filepath.Join(h.home, ".pleiades-podman", "podman.sock"))
	if err != nil || link != filepath.Join(h.home, "machine-api.sock") {
		t.Fatalf("the socket link is %q (%v), want the machine's own socket", link, err)
	}
	goCalls := h.read(t, "go.calls")
	if !strings.Contains(goCalls, "DOCKER_HOST=unix://"+filepath.Join(h.home, ".pleiades-podman", "podman.sock")) ||
		!strings.Contains(goCalls, "RYUK=true") || !strings.Contains(goCalls, "run ./tools/testgate -full -p 1 -coverage-out") {
		t.Errorf("the tests did not run against the linked socket with Ryuk off:\n%s", goCalls)
	}
	podmanCalls := h.read(t, "podman.calls")
	if strings.Contains(podmanCalls, "machine start") || strings.Contains(podmanCalls, "machine stop") {
		t.Errorf("a machine that was already running was started or stopped:\n%s", podmanCalls)
	}
	if n := strings.Count(podmanCalls, "podman rm -f -v c-test"); n != 2 {
		t.Errorf("the test containers were removed %d time(s), want before and after:\n%s", n, podmanCalls)
	}
	if strings.Contains(podmanCalls, "rm -f -a") || strings.Contains(podmanCalls, "rm -f named-data") || strings.Contains(podmanCalls, "volume prune") {
		t.Errorf("the cleanup removed what no test labelled, or a named volume:\n%s", podmanCalls)
	}
	if !strings.Contains(podmanCalls, "podman volume rm -f "+strings.Repeat("ab", 32)) {
		t.Errorf("the unused anonymous volume was not removed:\n%s", podmanCalls)
	}
	if !strings.Contains(out, "cleanup after the container pass removed 1 container(s), 0 network(s), 1 anonymous volume(s)") {
		t.Errorf("the cleanup did not say what it removed:\n%s", out)
	}
	cov, err := h.x.ContainerCoverage(context.Background(), "abc")
	_ = cov
	_ = err // the Pleiades-driven read is covered by the leg's own tests; the file exists here:
	if _, statErr := os.Stat(filepath.Join(h.home, "trees", "abc", ContainerCoverageFile)); statErr != nil {
		t.Errorf("the coverage measurement was not written in the tree: %v", statErr)
	}
}

func TestContainerTestScript_StartsAMachineThatIsNotRunning(t *testing.T) {
	h := newPodmanHome(t, false)
	if out, code := h.run(t); code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	calls := h.read(t, "podman.calls")
	if !strings.Contains(calls, "podman machine start") || !strings.Contains(calls, "podman machine stop") {
		t.Errorf("a stopped machine was not started and stopped again:\n%s", calls)
	}
	if _, err := os.Stat(filepath.Join(h.home, StartedMark)); !os.IsNotExist(err) {
		t.Errorf("the note that the pass started the machine outlived the stop: %v", err)
	}
}

// A machine an earlier, interrupted pass started (its note left behind) is
// stopped by the next pass, which finds it running; one nobody noted is not.
func TestContainerTestScript_StopsAMachineAnInterruptedPassStarted(t *testing.T) {
	h := newPodmanHome(t, true)
	if err := os.MkdirAll(filepath.Dir(filepath.Join(h.home, StartedMark)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.home, StartedMark), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if out, code := h.run(t); code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if calls := h.read(t, "podman.calls"); !strings.Contains(calls, "podman machine stop") {
		t.Errorf("the machine an interrupted pass started was left running:\n%s", calls)
	}
}

// The stop sent from here when a pass is interrupted cleans and stops a
// machine a pass started, whose own shell may already be gone, and leaves a
// machine no pass started running.
func TestContainerStopScript_CleansAndStopsOnlyAMachineAPassStarted(t *testing.T) {
	for _, noted := range []bool{true, false} {
		h := newPodmanHome(t, true)
		if noted {
			if err := os.MkdirAll(filepath.Dir(filepath.Join(h.home, StartedMark)), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(h.home, StartedMark), nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		cmd := exec.Command("/bin/sh", "-c", h.x.ContainerStopScript("abc"))
		cmd.Env = append(os.Environ(), "HOME="+h.home, "PATH="+h.x.Path+":/usr/bin:/bin")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("noted=%v: %v\n%s", noted, err, out)
		}
		calls := h.read(t, "podman.calls")
		if !strings.Contains(calls, "podman rm -f -v c-test") || !strings.Contains(string(out), "cleanup removed 1 container(s)") {
			t.Errorf("noted=%v: the stop did not clean, or did not say so:\n%s\n%s", noted, calls, out)
		}
		if got := strings.Contains(calls, "machine stop"); got != noted {
			t.Errorf("noted=%v: machine stopped %v:\n%s", noted, got, calls)
		}
	}
}

// A failing pass must exit with its status and still remove the containers it
// left: a pass that failed is the one most likely to have left some.
func TestContainerTestScript_AFailedPassExitsWithItsStatusAndCleansUp(t *testing.T) {
	h := newPodmanHome(t, true)
	if err := os.WriteFile(filepath.Join(h.home, "go.exit"), []byte("3"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, code := h.run(t)
	if code != 3 {
		t.Fatalf("exit %d, want the tests' 3:\n%s", code, out)
	}
	if n := strings.Count(h.read(t, "podman.calls"), "podman rm -f -v c-test"); n != 2 {
		t.Errorf("a failed pass removed containers %d time(s), want before and after", n)
	}
}

func TestContainerTestScript_NoMachineSocketIsStatus2(t *testing.T) {
	h := newPodmanHome(t, true)
	// A podman that inspects to nothing: no machine was ever created.
	empty := "#!/bin/sh\ncase \"$1 $2\" in \"machine list\") echo 'Currently running';; esac\nexit 0\n"
	if err := os.WriteFile(filepath.Join(h.x.Path, "podman"), []byte(empty), 0o700); err != nil { // #nosec G306 -- a test's own stand-in program
		t.Fatal(err)
	}
	out, code := h.run(t)
	if code != 2 || !strings.Contains(out, "no Podman machine socket") {
		t.Fatalf("exit %d, output %q; want 2 naming the missing socket", code, out)
	}
	if strings.Contains(h.read(t, "go.calls"), "testgate") {
		t.Error("the tests ran with no Podman socket")
	}
}

// The pass removes the tagged images no test names (debian here), keeps what
// the tests build (pleiades/...) and what tools/testimages lists, whatever its
// tag or digest, and with DropCache removes its own Go build cache, not the
// container-free pass's.
func TestContainerTestScript_PrunesStaleImagesAndDropsItsCache(t *testing.T) {
	h := newPodmanHome(t, true)
	h.x.DropCache = true
	for _, dir := range []string{ContainerCacheDir, CacheDir} {
		if err := os.MkdirAll(filepath.Join(h.home, "trees", dir), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	out, code := h.run(t)
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	calls := h.read(t, "podman.calls")
	if !strings.Contains(calls, "podman rmi -f id-debian") {
		t.Errorf("the image no test names was not removed:\n%s", calls)
	}
	for _, kept := range []string{"id-nats", "id-telnetd", "id-notconf"} {
		if strings.Contains(calls, "rmi -f "+kept) {
			t.Errorf("removed %s, which a test names or builds:\n%s", kept, calls)
		}
	}
	if !strings.Contains(out, "1 image(s) no test names, its Go build cache") {
		t.Errorf("the cleanup did not say what it removed:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(h.home, "trees", ContainerCacheDir)); !os.IsNotExist(err) {
		t.Errorf("the container pass's build cache is still there: %v", err)
	}
	if _, err := os.Stat(filepath.Join(h.home, "trees", CacheDir)); err != nil {
		t.Errorf("the container-free pass's build cache was removed too: %v", err)
	}
	if s := h.x.TestScript("abc", 0, []string{"a/b"}); strings.Count(s, `rm -rf "$HOME/trees/`+CacheDir+`"`) != 2 {
		t.Errorf("the container-free pass does not drop its own cache at its end and on an interrupt:\n%s", s)
	}
	if s := h.x.VulnScript("abc", "v1.0.0", "linux", "amd64", "go1.26.0"); !strings.Contains(s, VulnCacheDir) || !strings.Contains(s, `rm -rf "$HOME/trees/`+VulnCacheDir+`"`) {
		t.Errorf("the scan does not build into and drop its own cache:\n%s", s)
	}
	h.x.DropCache = false
	if s := h.x.ContainerTestScript("abc", 0, []string{"a/b"}); strings.Contains(s, "rm -rf \"$HOME/trees/"+ContainerCacheDir) {
		t.Errorf("a machine that keeps its cache had it dropped:\n%s", s)
	}
}
