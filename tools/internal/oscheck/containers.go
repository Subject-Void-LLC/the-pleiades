// Running the container-dependent packages on a machine that has Podman
// instead of Docker (the Mac, whose containers run in a Podman machine).
package oscheck

import (
	"context"
	"fmt"
	"io"
	"strings"
)

// ContainerRecordFile and ContainerCoverageFile are where a container run
// writes its testgate record and its coverage measurement, in the unpacked
// tree, beside RecordFiles.
const (
	ContainerRecordFile   = ".pleiades-record-containers.json"
	ContainerCoverageFile = ".pleiades-coverage-containers.json"
)

// Containers makes POSIX.Test run its packages against the machine's Podman:
// one testgate pass over the container-dependent packages, with a coverage
// measurement, in place of the full and repeat passes over container-free ones.
type Containers struct {
	// Socket is where, relative to the login's home, a link to the Podman
	// machine's API socket is kept. Its name must contain "podman.sock":
	// testcontainers chooses its Podman provider by that text in DOCKER_HOST,
	// and without it asks the daemon for a network called bridge, which
	// Podman does not have.
	Socket string
}

// ContainerNeeds are the commands a machine must have to run the container
// packages: the tests build images and look for the docker command, and
// Podman runs the containers.
var ContainerNeeds = []Need{
	{Command: "podman", Why: "install it with `brew install podman`, then `podman machine init --rootful` (the fixtures that run privileged need a rootful machine)"},
	{Command: "docker", Why: "install the docker command with `brew install docker`: the tests shell out to it, and it talks to Podman through DOCKER_HOST"},
}

// ContainerTestScript is what the machine runs for a container pass in sha's
// tree: it starts the Podman machine when it is not running (and stops it
// again at the end, so its memory is free for what runs next on the Mac, the
// Windows ARM VM), links its socket
// under a name testcontainers accepts, points DOCKER_HOST at it, removes every
// container before and after (Ryuk, testcontainers' own cleanup, is off: it
// never sees its readiness line through Podman's API and exits), and runs one
// testgate pass over packages with a coverage measurement. It is TestScript's
// job handling, so an interrupt ends the pass and the containers it started.
//
// None of it uses a template brace, which Pleiades would render in the
// command before it reached the machine.
func (x POSIX) ContainerTestScript(sha string, parallel int, packages []string) string {
	sock := x.Containers.Socket
	pkgs := strings.Join(packages, " ")
	return fmt.Sprintf(`exec 2>&1
export PATH="$PATH:%[1]s"
export GOCACHE="$HOME/%[2]s/%[10]s"
cd "$HOME/%[2]s/%[3]s" || exit 2
started=0
if ! podman machine list | grep -q 'Currently running'; then podman machine start || exit 2; started=1; fi
real=$(podman machine inspect | grep -A3 PodmanSocket | grep '"Path"' | sed 's/.*"Path": *"\(.*\)".*/\1/')
[ -n "$real" ] || { echo "no Podman machine socket: is a machine created (podman machine init --rootful)?"; exit 2; }
mkdir -p "$(dirname "$HOME/%[4]s")" && ln -sf "$real" "$HOME/%[4]s" || exit 2
export DOCKER_HOST="unix://$HOME/%[4]s" TESTCONTAINERS_RYUK_DISABLED=true
clean() { podman rm -f -a >/dev/null 2>&1; podman network prune -f >/dev/null 2>&1; podman volume prune -f >/dev/null 2>&1; }
done_() { clean; [ "$started" = 1 ] && podman machine stop >/dev/null 2>&1; true; }
%[7]s
clean
rm -f %[8]s %[9]s
set -m
%[6]s/bin/sh -c 'go run ./tools/testgate -full -p %[11]d -coverage-out %[9]s -report-out %[8]s %[5]s' &
job=$!
set +m
echo "$job" > %[12]s
trap 'stop_run "$job"; rm -f %[12]s; done_; exit 143' TERM HUP INT
wait "$job"
status=$?
rm -f %[12]s
done_
exit $status`, x.Path, x.Dir, sha, sock, pkgs, x.Wrap, stopRun, ContainerRecordFile, ContainerCoverageFile, CacheDir, parallelOrOne(parallel), x.PIDFile())
}

// parallelOrOne is how many container packages run at once: one unless the
// caller said otherwise, since each starts brokers and databases of its own and
// the machine is a Podman VM with a few gigabytes.
func parallelOrOne(p int) int {
	if p <= 0 {
		return 1
	}
	return p
}

// ContainerCoverage prints the container pass's coverage measurement from
// sha's tree, or nothing when the pass wrote none.
func (x POSIX) ContainerCoverage(ctx context.Context, sha string) (string, error) {
	return x.Pleiades.Adhoc(ctx, "exec.command", "argv:="+ArgvYAML("/bin/sh", "-c",
		fmt.Sprintf(`cd "$HOME/%s/%s" || exit 0
[ -f %s ] && cat %s
exit 0`, x.Dir, sha, ContainerCoverageFile, ContainerCoverageFile)))
}

// CoverageReader is a Leg that can read back the coverage measurement its
// test run wrote, for the file Options.CoverageOut names.
type CoverageReader interface {
	// Coverage returns the measurement file's content for sha's tree, "" when
	// the run wrote none.
	Coverage(ctx context.Context, sha string) (string, error)
}

// writeCoverage reads the leg's coverage measurement back into o.CoverageOut,
// when one was asked for and the leg can. A measurement that cannot be read is
// said in rec.Error and does not change what the tests decided.
func (f Flow) writeCoverage(leg Leg, sha string, o *Options, rec *LegRecord, stderr io.Writer) {
	cr, ok := leg.(CoverageReader)
	if !ok || o.CoverageOut == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), RecordsGrace)
	defer cancel()
	data, err := cr.Coverage(ctx, sha)
	if err == nil && strings.TrimSpace(data) != "" {
		err = writeFileAtomic(o.CoverageOut, []byte(strings.TrimSpace(data)+"\n"))
	}
	if err != nil {
		fmt.Fprintf(stderr, "%s: reading the coverage measurement back from %s: %v\n", f.Tool, f.Target, err)
		if rec.Error == "" {
			rec.Error = "reading the coverage measurement back: " + err.Error()
		}
	}
}
