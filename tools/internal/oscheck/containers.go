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
	// Share makes the pass run only the Podman machine's share of the packages
	// it is given (testgate -host podman), which pr-check gives the whole
	// container list: the machine and this one each compute the same plan from
	// it and run their own part, so the pass ends on both at about one time.
	Share bool
}

// StartedMark is where, relative to the login's home, a container pass notes
// that it started the Podman machine itself, so whichever way the pass ends
// (its own end, a stop sent from here, or an interrupt that killed the shell
// first and left a later run or stop to find the note), the machine it started
// is stopped again and one it found running is left running.
const StartedMark = ".pleiades-podman/started-by-check"

// podmanClean defines the shell function clean, which removes what a container
// pass's tests left in the Podman machine: the containers testcontainers or
// docker compose labelled (with their anonymous volumes), the networks so
// labelled, every unused anonymous volume (64 hex digits; a named volume can
// hold someone's data and is kept), and the dangling images a rebuilt fixture
// leaves untagged (telnetd's, each run). It keeps every tagged image, so the
// next run pulls nothing again. It counts what it removed into cleaned, and
// prints nothing itself, so it works on a channel that has gone away.
const podmanClean = `clean() {
	c=$(for l in org.testcontainers=true com.docker.compose.project; do podman ps -aq --filter "label=$l" 2>/dev/null; done | sort -u)
	n=$(for l in org.testcontainers=true com.docker.compose.project; do podman network ls -q --filter "label=$l" 2>/dev/null; done | sort -u)
	[ -n "$c" ] && podman rm -f -v $c >/dev/null 2>&1
	[ -n "$n" ] && podman network rm -f $n >/dev/null 2>&1
	v=$(podman volume ls -q --filter dangling=true 2>/dev/null | grep -E '^[0-9a-f]+$' | awk 'length($0) == 64')
	[ -n "$v" ] && podman volume rm -f $v >/dev/null 2>&1
	i=$(podman images -q --filter dangling=true 2>/dev/null | sort -u)
	[ -n "$i" ] && podman rmi $i >/dev/null 2>&1
	cleaned="$(printf '%s\n' $c | grep -c .) container(s), $(printf '%s\n' $n | grep -c .) network(s), $(printf '%s\n' $v | grep -c .) anonymous volume(s) and $(printf '%s\n' $i | grep -c .) dangling image(s)"
}`

// ContainerNeeds are the commands a machine must have to run the container
// packages: the tests build images and look for the docker command, and
// Podman runs the containers.
var ContainerNeeds = []Need{
	{Command: "podman", Why: "install it with `brew install podman`, then `podman machine init --rootful` (the fixtures that run privileged need a rootful machine)"},
	{Command: "docker", Why: "install the docker command with `brew install docker`: the tests shell out to it, and it talks to Podman through DOCKER_HOST"},
}

// ContainerTestScript is what the machine runs for a container pass in sha's
// tree: it starts the Podman machine when it is not running, noting so
// (StartedMark), links its socket under a name testcontainers accepts, points
// DOCKER_HOST at it, cleans before and after (podmanClean; Ryuk,
// testcontainers' own cleanup, is off: it never sees its readiness line
// through Podman's API and exits), and runs one testgate pass over packages
// with a coverage measurement, only Podman's share of them when Share is set.
// However it ends, pass, fail, a stop sent from here, or a TERM, HUP, INT or
// broken pipe, done_ cleans, stops the machine when this run (or an earlier
// one that never got to) started it, and only then says what it removed, with
// SIGPIPE ignored, so a channel that has gone away cannot stop it partway. It
// is TestScript's job handling, so an interrupt ends the pass and the
// containers it started.
//
// None of it uses a template brace, which Pleiades would render in the
// command before it reached the machine.
func (x POSIX) ContainerTestScript(sha string, parallel int, packages []string) string {
	sock := x.Containers.Socket
	pkgs := strings.Join(packages, " ")
	host := ""
	if x.Containers.Share {
		host = "-host podman "
	}
	return fmt.Sprintf(`exec 2>&1
export PATH="$PATH:%[1]s"
export GOCACHE="$HOME/%[2]s/%[10]s"
cd "$HOME/%[2]s/%[3]s" || exit 2
mark="$HOME/%[13]s"
mkdir -p "$(dirname "$mark")" || exit 2
started=0
if ! podman machine list | grep -q 'Currently running'; then podman machine start || exit 2; started=1; : > "$mark"; elif [ -f "$mark" ]; then started=1; fi
real=$(podman machine inspect | grep -A3 PodmanSocket | grep '"Path"' | sed 's/.*"Path": *"\(.*\)".*/\1/')
[ -n "$real" ] || { echo "no Podman machine socket: is a machine created (podman machine init --rootful)?"; exit 2; }
mkdir -p "$(dirname "$HOME/%[4]s")" && ln -sf "$real" "$HOME/%[4]s" || exit 2
export DOCKER_HOST="unix://$HOME/%[4]s" TESTCONTAINERS_RYUK_DISABLED=true
%[14]s
done_() { trap '' PIPE; clean; stopped=""; if [ "$started" = 1 ]; then podman machine stop >/dev/null 2>&1; rm -f "$mark"; stopped=", and stopped the Podman machine it started"; fi; echo "%[15]s: cleanup after the container pass removed $cleaned$stopped"; true; }
%[7]s
clean
[ "$cleaned" = "0 container(s), 0 network(s), 0 anonymous volume(s) and 0 dangling image(s)" ] || echo "%[15]s: before the container pass, cleanup removed what an earlier one left: $cleaned"
rm -f %[8]s %[9]s
set -m
%[6]s/bin/sh -c 'go run ./tools/testgate -full %[16]s-p %[11]d -coverage-out %[9]s -report-out %[8]s %[5]s' &
job=$!
set +m
echo "$job" > %[12]s
trap 'stop_run "$job"; rm -f %[12]s; done_; exit 143' TERM HUP INT PIPE
wait "$job"
status=$?
rm -f %[12]s
done_
exit $status`, x.Path, x.Dir, sha, sock, pkgs, x.Wrap, stopRun, ContainerRecordFile, ContainerCoverageFile, CacheDir, parallelOrOne(parallel), x.PIDFile(), StartedMark, podmanClean, x.Tool, host)
}

// ContainerStopScript is StopScript for a container pass: it ends the run, then
// cleans the Podman machine and stops it when a pass started it
// (StartedMark), since an interrupt can kill the pass's own shell before its
// done_ runs. It does nothing to a machine no pass started.
func (x POSIX) ContainerStopScript(sha string) string {
	return fmt.Sprintf(`export PATH="$PATH:%[1]s"
pid=$(cat "$HOME/%[2]s/%[3]s/%[4]s" 2>/dev/null)
%[5]s
case "$pid" in ''|*[!0-9]*) ;; *) stop_run "$pid" ;; esac
podman machine list 2>/dev/null | grep -q 'Currently running' || exit 0
%[6]s
clean
if [ -f "$HOME/%[7]s" ]; then podman machine stop >/dev/null 2>&1; rm -f "$HOME/%[7]s"; cleaned="$cleaned, and stopped the Podman machine a pass started"; fi
echo "%[8]s: stopping the container pass, cleanup removed $cleaned"
exit 0`, x.Path, x.Dir, sha, x.PIDFile(), stopRun, podmanClean, StartedMark, x.Tool)
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
