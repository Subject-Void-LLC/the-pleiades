//go:build devtools

// Command breakglass returns this machine to the state every test in this
// repository assumes it starts from, and refuses to run while a test run is
// still using that state.
//
// WHY THIS EXISTS. The gates in tests/e2e provision real infrastructure:
// a kind cluster, a docker compose project, and a long tail of
// testcontainers-managed containers for NATS, PostgreSQL, sshd and toxiproxy.
// All of it is supposed to clean itself up, and nearly always does. The cases
// where it does not are the ones that matter, because none of them announce
// themselves:
//
//   - A test binary killed with SIGKILL runs no t.Cleanup at all, so its
//     cluster, its compose project and its containers outlive it.
//   - The testcontainers reaper (ryuk) removes a session's containers when
//     the session ends, but it is itself a container: if the daemon restarts
//     or the reaper is killed, everything it was holding is orphaned.
//   - A `docker compose down` without --volumes leaves the named volumes, so
//     the next run starts against a database that already has a schema and a
//     master key it cannot decrypt.
//
// Every one of those surfaces later as a test failure that looks like a
// product defect. That is not hypothetical: this tool was written the day a
// leftover-infrastructure problem cost a full CI cycle and produced a Helm
// chart failure that was not a Helm chart failure. See LESSONS_LEARNED.md.
//
// WHY IT IS NOT `docker system prune`. Prune is defined by what is unused,
// which is a fact about the daemon rather than about this repository, so it
// removes a developer's unrelated work with exactly the same confidence it
// removes ours. This machine has a long-lived kind cluster named "desktop"
// and its two supporting containers sitting beside the throwaway one these
// tests create. A prune takes them; this tool must not, and enforces that
// with a protected list rather than with care.
//
// So every removal here is positively attributed to this repository before it
// happens. A container is removed because it is in a compose project this
// repository defines, or a kind cluster this repository names, or a
// testcontainers session that is provably finished. Anything else is left
// alone and reported, which is the honest answer for state whose owner is
// unknown.
//
// THE LIVE-RUN GUARD IS THE POINT. Cleaning up shared infrastructure while a
// run is using it does not merely waste the run: it produces a failure at
// whichever assertion happened to be executing, which reads as a defect in
// whatever that assertion was about. That is the incident above. So this tool
// asks whether anybody is using the state before touching it, and the
// question it asks is a fact about a live process rather than about the age
// or the size of anything.
//
// Usage:
//
//	go run tools/breakglass/main.go            # clean, refusing if a run is live
//	go run tools/breakglass/main.go -n         # say what would be removed
//	go run tools/breakglass/main.go -images    # also drop the built images
//	go run tools/breakglass/main.go -force     # clean anyway (kills a live run)
//
// Or `make break-glass`, `make break-glass BREAK_GLASS_FLAGS=-n`.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// The names this repository creates. Each is a literal that appears in the
// code that creates it, and the test below asserts these match rather than
// letting the two drift into a tool that cleans up a name nothing uses.
const (
	// kindClusterPrefix names every throwaway cluster tests/e2e's
	// Kubernetes gate provisions. The gate appends its own process id, so
	// the full name is this prefix, a hyphen, and a number. It matches the
	// constant of the same name in tests/e2e/packaging_kind_test.go, which
	// the test below checks rather than trusts.
	kindClusterPrefix = "pleiades-release-gate"

	// composeProject is docker compose's project name for docker-compose.yml.
	// Compose derives it from the directory name when the file sets no
	// `name:`, and the directory is auto-roboto. Passing it explicitly means
	// this tool cleans up the project the repository actually creates even
	// when it is run from somewhere else.
	composeProject = "auto-roboto"

	// uidevPrefix is the container name prefix `make ui-dev` uses for the
	// broker it starts. ui-stop clears these too; this tool repeats it so a
	// single command is enough.
	uidevPrefix = "pleiades-uidev"
)

// builtImages are the images this repository's own builds produce. They are
// removed only with -images.
//
// Off by default because they are expensive rather than dangerous: rebuilding
// both costs several minutes and neither carries state between runs, since
// the gate builds them from the current tree every time. The flag exists for
// the case where the question is whether a build is reproducible from
// nothing, which is the one question a cached image can answer wrongly.
var builtImages = []string{
	"pleiades/controller:dev",
	"pleiades/runner:dev",
	"pleiades/legacy-ansible-runner:release-gate",
}

// tempDirs are the scratch directories the development server and the runner
// heartbeat write into, removed by exact path.
var tempDirs = []string{
	"/tmp/pleiades-runner",
}

// tempGlobs are scratch directories whose names carry a per-run suffix.
var tempGlobs = []string{
	"/tmp/pleiades-uidev-*",
}

func main() {
	dryRun := flag.Bool("n", false, "report what would be removed and remove nothing")
	force := flag.Bool("force", false, "clean even though a test run appears to be live, which will break that run")
	images := flag.Bool("images", false, "also remove the container images this repository builds, forcing the next run to build them from nothing")
	flag.Parse()

	if err := run(*dryRun, *force, *images); err != nil {
		fmt.Fprintf(os.Stderr, "break-glass: %v\n", err)
		os.Exit(1)
	}
}

func run(dryRun, force, images bool) error {
	if _, err := exec.LookPath("docker"); err != nil {
		return fmt.Errorf("docker is not on PATH, so there is no state to clean: %w", err)
	}
	if out, err := docker("version", "--format", "{{.Server.Version}}"); err != nil {
		return fmt.Errorf("the docker daemon is not reachable, so nothing can be inspected or removed: %v\n%s", err, out)
	}

	root, err := repoRoot()
	if err != nil {
		return err
	}

	// The guard, first, and before anything is inspected in detail. A tool
	// that enumerated everything and then refused would still have spent the
	// time; more importantly, a reader of the output would have seen a list
	// of things that were not removed and could easily read it as a list of
	// things that were.
	live, err := liveRuns(root)
	if err != nil {
		return err
	}
	if len(live) > 0 {
		fmt.Println("break-glass: a test run appears to be using this state right now:")
		for _, reason := range live {
			fmt.Printf("  - %s\n", reason)
		}
		switch {
		// -n removes nothing, so a live run is no reason to refuse it. It is
		// still worth saying, because the answer it prints is a snapshot of
		// something that is moving: containers this run owns are correctly
		// absent from the list now and will be in it once the run ends.
		case dryRun:
			fmt.Println()
			fmt.Println("Reporting anyway, because -n removes nothing. Note that the list below is a")
			fmt.Println("snapshot: what the live run owns is excluded now and will be included once")
			fmt.Println("that run finishes.")

		case !force:
			fmt.Println()
			fmt.Println("Nothing was removed. Removing infrastructure a run is using does not just")
			fmt.Println("waste the run: it fails whichever assertion happens to be executing, which")
			fmt.Println("reads as a defect in whatever that assertion was about.")
			fmt.Println()
			fmt.Println("Wait for the run to finish, or pass -force to clean anyway and break it.")
			return errRunIsLive

		default:
			fmt.Println()
			fmt.Println("-force given: cleaning anyway. The run above will fail, and its failure will")
			fmt.Println("not be about the code it was testing.")
			fmt.Println()
		}
	}

	plan := &plan{dryRun: dryRun}

	if err := cleanKind(plan); err != nil {
		return err
	}
	if err := cleanCompose(plan, root); err != nil {
		return err
	}
	if err := cleanTestcontainers(plan); err != nil {
		return err
	}
	if err := cleanUIDev(plan); err != nil {
		return err
	}
	cleanTempDirs(plan)
	if images {
		cleanImages(plan)
	}

	plan.report(images)
	return nil
}

// errRunIsLive is the refusal, distinguished from a failure so a caller can
// tell "I did not clean because somebody is working" from "I tried and could
// not."
var errRunIsLive = fmt.Errorf("refused: a test run is live (pass -force to override)")

// plan accumulates what was done, so the summary is a record of actions
// rather than a prediction made before them.
type plan struct {
	dryRun  bool
	removed []string
	kept    []string
	failed  []string
}

// do performs one removal, or describes it under -n.
func (p *plan) do(what string, args ...string) {
	if p.dryRun {
		p.removed = append(p.removed, what+"  (not removed: -n)")
		return
	}
	if out, err := docker(args...); err != nil {
		p.failed = append(p.failed, fmt.Sprintf("%s: %v: %s", what, err, strings.TrimSpace(out)))
		return
	}
	p.removed = append(p.removed, what)
}

func (p *plan) keep(what string) { p.kept = append(p.kept, what) }
func (p *plan) fail(what string) { p.failed = append(p.failed, what) }
func (p *plan) note(what string) { p.removed = append(p.removed, what) }
func (p *plan) anything() bool   { return len(p.removed) > 0 }

func (p *plan) report(images bool) {
	fmt.Println()
	if !p.anything() {
		fmt.Println("break-glass: nothing to remove; this machine is already clean.")
	} else {
		verb := "removed"
		if p.dryRun {
			verb = "would remove"
		}
		fmt.Printf("break-glass: %s\n", verb)
		for _, r := range p.removed {
			fmt.Printf("  - %s\n", r)
		}
	}
	if len(p.kept) > 0 {
		fmt.Println()
		fmt.Println("Left alone, because this repository did not create it:")
		for _, k := range p.kept {
			fmt.Printf("  - %s\n", k)
		}
	}
	if len(p.failed) > 0 {
		fmt.Println()
		fmt.Println("Could not be removed:")
		for _, f := range p.failed {
			fmt.Printf("  - %s\n", f)
		}
	}
	if !images && !p.dryRun {
		fmt.Println()
		fmt.Println("The built images were kept, so the next run does not rebuild them.")
		fmt.Println("Pass -images to drop those too and prove a build from nothing.")
	}
}

// cleanKind removes this repository's throwaway cluster and nothing else.
//
// By exact name. `kind delete cluster` takes a name and deletes only that
// cluster, so the protection here is that the name is a constant rather than
// anything derived from what is running. Every other cluster is listed and
// left, which is the difference between this and the delete-first the gate
// itself performs.
func cleanKind(p *plan) error {
	if _, err := exec.LookPath("kind"); err != nil {
		return nil
	}
	out, err := command("kind", "get", "clusters")
	if err != nil {
		// kind prints "No kind clusters found." on stderr and exits non-zero
		// on some versions, which is the empty case rather than a failure.
		if strings.Contains(out, "No kind clusters") {
			return nil
		}
		p.fail(fmt.Sprintf("listing kind clusters: %v: %s", err, strings.TrimSpace(out)))
		return nil
	}

	for _, name := range strings.Fields(out) {
		reason, ours := clusterIsReclaimable(name)
		if !ours {
			p.keep(fmt.Sprintf("kind cluster %q (%s)", name, reason))
			continue
		}
		if p.dryRun {
			p.note(fmt.Sprintf("kind cluster %q  (not removed: -n)", name))
			continue
		}
		if out, err := command("kind", "delete", "cluster", "--name", name); err != nil {
			p.fail(fmt.Sprintf("kind cluster %q: %v: %s", name, err, strings.TrimSpace(out)))
			continue
		}
		p.note(fmt.Sprintf("kind cluster %q", name))
	}
	return nil
}

// cleanCompose takes the compose project down with its volumes.
//
// --volumes is the whole reason this is here rather than in a developer's
// muscle memory. `docker compose down` keeps named volumes, so the next
// `docker compose up` finds a PostgreSQL data directory that is already
// initialized, with a role and a password from the previous run and rows
// encrypted under a master key this run does not have. The stack comes up,
// which is what makes it expensive: the failure arrives later, as a decrypt
// error, rather than at start-up as a schema mismatch.
func cleanCompose(p *plan, root string) error {
	composeFile := filepath.Join(root, "docker-compose.yml")
	if _, err := os.Stat(composeFile); err != nil {
		return nil
	}

	// Nothing to do when the project has no containers and no volumes: down
	// on an absent project succeeds silently, and reporting it would put a
	// line in the summary for work that did not happen.
	ids, err := docker("compose", "--project-name", composeProject, "--file", composeFile, "ps", "--all", "--quiet")
	if err != nil {
		p.fail(fmt.Sprintf("inspecting compose project %q: %v", composeProject, err))
		return nil
	}
	volumes, err := docker("volume", "ls", "--quiet", "--filter", "label=com.docker.compose.project="+composeProject)
	if err != nil {
		p.fail(fmt.Sprintf("listing compose volumes for %q: %v", composeProject, err))
		return nil
	}
	if strings.TrimSpace(ids) == "" && strings.TrimSpace(volumes) == "" {
		return nil
	}

	what := fmt.Sprintf("compose project %q, with its volumes and network", composeProject)
	p.do(what, "compose", "--project-name", composeProject, "--file", composeFile,
		"down", "--volumes", "--remove-orphans")
	return nil
}

// container is the subset of `docker ps` this tool reasons about.
type container struct {
	Name    string
	Session string
	Image   string
	Running bool
	// KindCluster is the cluster a kind node belongs to, empty for anything
	// that is not a kind node.
	KindCluster string
}

// cleanTestcontainers removes the containers of finished testcontainers
// sessions, and only those.
//
// THE DISCRIMINATOR. testcontainers labels every container it starts with a
// sessionId, and starts one reaper per session named reaper_<sessionId>. The
// reaper is the session's liveness: while it runs, a test binary is holding
// those containers open; once it is gone, nothing will ever use them again.
// So a session is abandoned when no reaper for its id is running, which is a
// fact about the owning process rather than about how old or how numerous its
// containers are. A rule keyed on age or count is wrong for the run that is
// slower or larger than the number somebody picked, and the way it is wrong
// is that it deletes live infrastructure.
func cleanTestcontainers(p *plan) error {
	containers, err := listContainers("label=org.testcontainers=true")
	if err != nil {
		p.fail(fmt.Sprintf("listing testcontainers containers: %v", err))
		return nil
	}
	if len(containers) == 0 {
		return nil
	}

	for _, c := range abandoned(containers) {
		if reason, protected := protectedContainer(c); protected {
			p.keep(fmt.Sprintf("container %q (%s)", c.Name, reason))
			continue
		}
		p.do(fmt.Sprintf("container %q (%s, from finished session %s)",
			c.Name, c.Image, short(c.Session)),
			"rm", "--force", "--volumes", c.Name)
	}
	return nil
}

// abandoned returns the containers whose testcontainers session has no
// running reaper.
//
// Pure, and separated from the docker plumbing, because this is the decision
// the whole tool turns on and it is the one thing here that can be tested
// without a daemon.
func abandoned(containers []container) []container {
	liveSessions := map[string]bool{}
	for _, c := range containers {
		if c.Running && strings.HasPrefix(c.Name, "reaper_") {
			liveSessions[c.Session] = true
		}
	}

	var out []container
	for _, c := range containers {
		// A session with no id at all cannot be attributed to a run, so it is
		// left for a human rather than guessed at.
		if c.Session == "" {
			continue
		}
		if liveSessions[c.Session] {
			continue
		}
		out = append(out, c)
	}
	return out
}

// clusterIsReclaimable reports whether a kind cluster is one this
// repository created AND has finished with, plus the reason either way.
//
// Two conditions, and the second is the one that matters. The name must
// carry this gate's prefix, which is what makes it ours rather than a
// developer's. And the process id the gate appended to that prefix must no
// longer name a running process, which is what makes it FINISHED rather
// than in use. A cluster whose owner is still alive belongs to a concurrent
// run and is left alone even though it is in the way, because refusing to
// clean is recoverable and deleting a live run's cluster is not: that is
// the incident this tool was written after (FAILURE_PATTERNS.md #141).
//
// This mirrors reclaimAbandonedClusters in tests/e2e/packaging_kind_test.go
// rather than sharing code with it, deliberately. That file is a test in a
// package this devtools binary must not import, and the alternative,
// hoisting a process-liveness predicate into a shared package so two
// callers can each use five lines of it, buys less than it costs. If a
// third caller appears, hoist it then.
func clusterIsReclaimable(name string) (string, bool) {
	suffix, ok := strings.CutPrefix(name, kindClusterPrefix+"-")
	if !ok {
		return "this repository did not create it", false
	}
	pid, err := strconv.Atoi(suffix)
	if err != nil {
		return "its name carries no process id, so there is no way to tell whether it is in use", false
	}
	// A non-positive id is not a dead process, it is not a process at all.
	// os.Getpid never returns one, so a cluster named this way was not
	// created by the gate and cannot be reasoned about; treating "cannot be
	// alive" as "safe to delete" would turn every anomaly into a removal.
	if pid <= 0 {
		return fmt.Sprintf("%q is not a process id any run could have written, so its origin is unknown", suffix), false
	}
	if processIsAlive(pid) {
		return fmt.Sprintf("process %d still holds it, so a test run is using it", pid), false
	}
	return fmt.Sprintf("its owning process %d is gone", pid), true
}

// processIsAlive reports whether pid names a running process. Signal 0
// performs the existence and permission checks and delivers nothing.
//
// PID reuse can make a dead cluster look alive, and that is the safe
// direction: the cluster leaks and a later run reclaims it, where the
// opposite error destroys a live one.
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

// protectedContainer reports whether a container must never be removed by
// this tool, and why.
//
// Defense in depth rather than the primary control. Nothing on this list
// carries a testcontainers label, so the filter above cannot reach it in the
// first place; this is here so that a future filter which is wider than
// intended still cannot take a developer's long-lived cluster. The check is
// on what the container IS, so it holds for a cluster named anything.
func protectedContainer(c container) (string, bool) {
	if c.KindCluster != "" && !strings.HasPrefix(c.KindCluster, kindClusterPrefix+"-") {
		return fmt.Sprintf("a node of kind cluster %q, which this repository did not create", c.KindCluster), true
	}
	for _, name := range []string{"kind-cloud-provider", "kind-registry-mirror"} {
		if c.Name == name {
			return "Docker Desktop's Kubernetes support", true
		}
	}
	return "", false
}

// cleanUIDev clears what `make ui-dev` leaves behind, by name prefix.
func cleanUIDev(p *plan) error {
	containers, err := listContainers("name=" + uidevPrefix)
	if err != nil {
		p.fail(fmt.Sprintf("listing development-server containers: %v", err))
		return nil
	}
	for _, c := range containers {
		p.do(fmt.Sprintf("container %q (development server broker)", c.Name),
			"rm", "--force", "--volumes", c.Name)
	}
	return nil
}

// cleanTempDirs removes the scratch directories, which is the one part of
// this tool that touches the filesystem.
//
// By exact path and by a glob anchored to a fixed prefix, never by pattern
// over a directory this tool did not name. Failure here is reported and not
// fatal: a leftover directory makes a run untidy, where everything above
// makes it wrong.
func cleanTempDirs(p *plan) {
	paths := append([]string{}, tempDirs...)
	for _, glob := range tempGlobs {
		matches, err := filepath.Glob(glob)
		if err != nil {
			p.fail(fmt.Sprintf("expanding %s: %v", glob, err))
			continue
		}
		paths = append(paths, matches...)
	}

	for _, path := range paths {
		if _, err := os.Stat(path); err != nil {
			continue
		}
		if p.dryRun {
			p.note(fmt.Sprintf("directory %s  (not removed: -n)", path))
			continue
		}
		if err := os.RemoveAll(path); err != nil {
			p.fail(fmt.Sprintf("directory %s: %v", path, err))
			continue
		}
		p.note(fmt.Sprintf("directory %s", path))
	}
}

// cleanImages removes the images this repository builds.
func cleanImages(p *plan) {
	for _, image := range builtImages {
		if out, err := docker("image", "inspect", image, "--format", "{{.Id}}"); err != nil {
			_ = out
			continue
		}
		p.do(fmt.Sprintf("image %s", image), "image", "rm", "--force", image)
	}
}

// listContainers returns every container, running or not, matching one
// docker filter.
func listContainers(filter string) ([]container, error) {
	const format = `{{.Names}}` + "\t" + `{{.Image}}` + "\t" + `{{.State}}` + "\t" +
		`{{.Label "org.testcontainers.sessionId"}}` + "\t" + `{{.Label "io.x-k8s.kind.cluster"}}`

	out, err := docker("ps", "--all", "--filter", filter, "--format", format)
	if err != nil {
		return nil, fmt.Errorf("%v: %s", err, strings.TrimSpace(out))
	}
	return parseContainers(out), nil
}

// parseContainers turns `docker ps --format` output into containers.
//
// Split out so the shape of what this tool reasons about is testable without
// a daemon, which is the same reason abandoned() is pure.
func parseContainers(out string) []container {
	var containers []container
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 5 {
			continue
		}
		containers = append(containers, container{
			Name:        fields[0],
			Image:       fields[1],
			Running:     fields[2] == "running",
			Session:     fields[3],
			KindCluster: fields[4],
		})
	}
	return containers
}

// liveRuns reports every reason to believe a test run is using this state.
//
// Two independent signals, because neither covers the other's window. A
// running reaper proves a testcontainers session is open, but a run that has
// started and not yet provisioned anything has no reaper; a `go test` process
// proves a run exists, but a test binary invoked directly is not one. Both
// are cheap and either is sufficient to refuse.
func liveRuns(root string) ([]string, error) {
	var reasons []string

	containers, err := listContainers("name=reaper_")
	if err != nil {
		return nil, fmt.Errorf("checking for live test sessions: %w", err)
	}
	for _, c := range containers {
		if c.Running {
			reasons = append(reasons, fmt.Sprintf(
				"testcontainers session %s is open (its reaper %q is running)", short(c.Session), c.Name))
		}
	}

	procs, err := testProcesses(root)
	if err != nil {
		return nil, err
	}
	reasons = append(reasons, procs...)

	return reasons, nil
}

// testProcesses reports running test processes whose working directory is
// inside this repository.
//
// Reading /proc directly rather than shelling out to pgrep, so the working
// directory can be checked: a `go test` in some other module is not this
// repository's run and must not stop a clean here. Anything unreadable is
// skipped rather than treated as live, because a process this tool cannot
// inspect is one it cannot attribute either.
func testProcesses(root string) ([]string, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		// Not Linux, or /proc is not mounted. The reaper signal above still
		// applies; this one simply does not exist here.
		return nil, nil
	}

	self := os.Getpid()
	var reasons []string
	for _, entry := range entries {
		pid := 0
		if _, err := fmt.Sscanf(entry.Name(), "%d", &pid); err != nil || pid == 0 || pid == self {
			continue
		}

		raw, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "cmdline"))
		if err != nil {
			continue
		}
		cmdline := strings.TrimSpace(strings.ReplaceAll(string(raw), "\x00", " "))
		if !looksLikeATestRun(cmdline) {
			continue
		}

		cwd, err := os.Readlink(filepath.Join("/proc", entry.Name(), "cwd"))
		if err != nil || !under(root, cwd) {
			continue
		}

		reasons = append(reasons, fmt.Sprintf("pid %d is running %s", pid, truncate(cmdline, 80)))
	}
	return reasons, nil
}

// looksLikeATestRun reports whether a command line is one of this
// repository's test or CI invocations.
//
// Deliberately broad on the make side and narrow on the binary side. `make
// ci` and `make push-gate` both reach the gates, and so does a bare `go
// test`; an editor's background type-check does not, and neither does a
// `go build`.
func looksLikeATestRun(cmdline string) bool {
	// The tool's own invocation, which is a `go run` of this file and would
	// otherwise match the `go ` prefixes below.
	if strings.Contains(cmdline, "tools/breakglass") {
		return false
	}
	for _, marker := range []string{
		"go test",
		"make ci",
		"make push-gate",
		"make test",
		".test -test.",
		"/testgate",
	} {
		if strings.Contains(cmdline, marker) {
			return true
		}
	}
	// A compiled test binary run directly, which is neither `go test` nor a
	// make target. Guarded on the empty case because that case is not exotic:
	// every kernel thread in /proc has an empty cmdline, so an unguarded index
	// here panics on the first one, on any Linux machine.
	fields := strings.Fields(cmdline)
	if len(fields) == 0 {
		return false
	}
	return strings.HasSuffix(fields[0], ".test")
}

// under reports whether path is root or inside it.
func under(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || !strings.HasPrefix(rel, "..")
}

// repoRoot walks up from this process's working directory until it finds the
// go.mod, the same way tests/e2e's own helper does.
func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no go.mod above the working directory, so the repository root is unknown")
		}
		dir = parent
	}
}

// docker runs one docker command and returns its combined output.
func docker(args ...string) (string, error) { return command("docker", args...) }

// command runs one program and returns its combined output.
//
// The program is always a fixed literal from this file and every argument is
// either a constant here or a name read back from the daemon, so nothing in
// any invocation comes from user input. This file is behind the devtools
// build tag and is therefore outside the scan `make gosec` performs, the same
// boundary tools/devcert and tools/uidev sit on.
func command(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// short truncates a testcontainers session id to something readable, which is
// all it is ever used for here.
func short(session string) string {
	if len(session) <= 12 {
		return session
	}
	return session[:12]
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
