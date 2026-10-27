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
		`trap 'stop_run "$job"; rm -f .maccheck.pid; done_; exit 143' TERM HUP INT`,
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
esac
exit 0
`
	goStand := `#!/bin/sh
echo "go DOCKER_HOST=$DOCKER_HOST RYUK=$TESTCONTAINERS_RYUK_DISABLED $*" >> "` + home + `/go.calls"
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
	if n := strings.Count(podmanCalls, "podman rm -f -a"); n != 2 {
		t.Errorf("containers were removed %d time(s), want before and after:\n%s", n, podmanCalls)
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
	if n := strings.Count(h.read(t, "podman.calls"), "podman rm -f -a"); n != 2 {
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
