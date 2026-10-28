// Removing what a container pass's tests left behind them in the engine, once
// the pass ends, pass or fail: pr-check's container leg calls it here, and the
// Mac's leg runs the same through its Podman machine's socket.
package main

import (
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"
)

// testLabels are the labels a test's container or network carries: every one
// testcontainers starts has the first (with Ryuk or without it), and every one
// tests/e2e's docker compose runs has the second. A container nothing labels
// is not a test's to remove.
var testLabels = []string{"org.testcontainers=true", "com.docker.compose.project"}

// anonymousVolume is the name the engine gives a volume nobody named: 64 hex
// digits. A named volume can hold someone's data and is never removed.
var anonymousVolume = regexp.MustCompile(`^[0-9a-f]{64}$`)

// engineCLI runs the docker command (which DOCKER_HOST may point at a Podman
// machine) with args, returning its standard output.
type engineCLI func(args ...string) (string, error)

// dockerCLI is the real engineCLI.
func dockerCLI(args ...string) (string, error) {
	out, err := exec.Command("docker", args...).Output() // #nosec G204 -- fixed subcommands, and IDs the engine itself listed
	return string(out), err
}

// cleanResult is what cleanContainers removed, by kind, and what it could not.
type cleanResult struct {
	containers, networks, volumes []string
	failed                        []string
}

// cleanContainers removes, from the engine run reaches, the test containers
// (testLabels) created at or after since, with their anonymous volumes
// (docker rm -f -v, which never removes a named one); then the test networks
// created since, which those containers held; then every anonymous volume
// created since that no container uses any more. It removes no image and no
// build cache, which the next run reuses (tools/testimages' pulls cost an hour
// and Docker Hub's rate limit), and nothing created before since, so a run
// that began earlier, or a container someone started by hand, is left alone.
// Each thing removed is named in the result.
func cleanContainers(run engineCLI, since time.Time) (cleanResult, error) {
	var res cleanResult
	containers, err := createdSince(run, since, "container", labelled(run, "ps", "-aq"))
	if err != nil {
		return res, err
	}
	if len(containers) > 0 {
		if _, err := run(append([]string{"rm", "-f", "-v"}, ids(containers)...)...); err != nil {
			res.failed = append(res.failed, fmt.Sprintf("removing %d container(s): %v", len(containers), err))
		} else {
			res.containers = names(containers)
		}
	}
	networks, err := createdSince(run, since, "network", labelled(run, "network", "ls", "-q"))
	if err != nil {
		return res, err
	}
	for _, n := range networks {
		if _, err := run("network", "rm", n.id); err != nil {
			res.failed = append(res.failed, fmt.Sprintf("network %s: %v", n.name, err))
			continue
		}
		res.networks = append(res.networks, n.name)
	}
	dangling, err := run("volume", "ls", "-q", "--filter", "dangling=true")
	if err != nil {
		return res, fmt.Errorf("listing unused volumes: %w", err)
	}
	var anonymous []string
	for _, v := range strings.Fields(dangling) {
		if anonymousVolume.MatchString(v) {
			anonymous = append(anonymous, v)
		}
	}
	volumes, err := createdSince(run, since, "volume", func() ([]string, error) { return anonymous, nil })
	if err != nil {
		return res, err
	}
	for _, v := range volumes {
		if _, err := run("volume", "rm", v.id); err != nil {
			res.failed = append(res.failed, fmt.Sprintf("volume %s: %v", v.name, err))
			continue
		}
		res.volumes = append(res.volumes, v.name)
	}
	return res, nil
}

// labelled lists the IDs list (a docker subcommand printing IDs) prints for
// each of testLabels, without repeats.
func labelled(run engineCLI, list ...string) func() ([]string, error) {
	return func() ([]string, error) {
		seen := map[string]bool{}
		var out []string
		for _, label := range testLabels {
			got, err := run(append(append([]string(nil), list...), "--filter", "label="+label)...)
			if err != nil {
				return nil, fmt.Errorf("docker %s: %w", strings.Join(list, " "), err)
			}
			for _, id := range strings.Fields(got) {
				if !seen[id] {
					seen[id] = true
					out = append(out, id)
				}
			}
		}
		return out, nil
	}
}

// engineObject is one container, network or volume: its ID and its name.
type engineObject struct{ id, name string }

// ids and names list objects' IDs and names.
func ids(objs []engineObject) []string {
	out := make([]string, len(objs))
	for i, o := range objs {
		out[i] = o.id
	}
	return out
}

func names(objs []engineObject) []string {
	out := make([]string, len(objs))
	for i, o := range objs {
		out[i] = o.name
	}
	return out
}

// createFormats is the inspect template giving an object's ID, creation time
// and name, by kind. Docker and Podman both print these fields.
var createFormats = map[string]string{
	"container": "{{.Id}} {{.Created}} {{.Name}}",
	"network":   "{{.Id}} {{.Created}} {{.Name}}",
	"volume":    "{{.Name}} {{.CreatedAt}} {{.Name}}",
}

// createdSince inspects the objects list names and keeps those created at or
// after since, sorted by name. One whose creation time cannot be read is kept
// out: a cleanup that cannot tell when something was made leaves it.
func createdSince(run engineCLI, since time.Time, kind string, list func() ([]string, error)) ([]engineObject, error) {
	found, err := list()
	if err != nil || len(found) == 0 {
		return nil, err
	}
	args := []string{"inspect", "--format", createFormats[kind]}
	if kind != "container" {
		args = []string{kind, "inspect", "--format", createFormats[kind]}
	}
	out, err := run(append(args, found...)...)
	if err != nil {
		return nil, fmt.Errorf("inspecting %d %s(s): %w", len(found), kind, err)
	}
	var keep []engineObject
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 3 {
			continue
		}
		created, ok := parseCreated(f[1 : len(f)-1])
		if !ok || created.Before(since) {
			continue
		}
		keep = append(keep, engineObject{id: f[0], name: strings.TrimPrefix(f[len(f)-1], "/")})
	}
	sort.Slice(keep, func(i, j int) bool { return keep[i].name < keep[j].name })
	return keep, nil
}

// parseCreated reads a creation time as Docker (RFC 3339) or Podman (Go's
// time.String form, "2006-01-02 15:04:05.999 -0700 MST") prints it.
func parseCreated(fields []string) (time.Time, bool) {
	text := strings.Join(fields, " ")
	if t, err := time.Parse(time.RFC3339Nano, text); err == nil {
		return t, true
	}
	// Podman can add a monotonic clock reading ("m=+0.1") after the zone.
	if i := strings.Index(text, " m="); i >= 0 {
		text = text[:i]
	}
	if t, err := time.Parse("2006-01-02 15:04:05.999999999 -0700 MST", text); err == nil {
		return t, true
	}
	return time.Time{}, false
}

// engineTime is the engine's own clock (docker info's SystemTime), which a
// cleanup's since is read from: the containers' creation times are the
// engine's, and Docker Desktop's VM and WSL's keep clocks that drift apart.
func engineTime(run engineCLI) (time.Time, error) {
	out, err := run("info", "--format", "{{.SystemTime}}")
	if err != nil {
		return time.Time{}, fmt.Errorf("asking the engine for its time: %w", err)
	}
	t, ok := parseCreated(strings.Fields(out))
	if !ok {
		return time.Time{}, fmt.Errorf("the engine gave its time as %q", strings.TrimSpace(out))
	}
	return t, nil
}

// printClean writes what cleanContainers removed, one line, then each failure.
func printClean(w io.Writer, res cleanResult, since time.Time) {
	fmt.Fprintf(w, "testgate: cleanup after the container pass removed %d container(s), %d network(s) and %d anonymous volume(s) the tests created since %s, and kept every image, the build cache and every named volume\n",
		len(res.containers), len(res.networks), len(res.volumes), since.Format(time.RFC3339))
	for _, kind := range []struct {
		name string
		list []string
	}{{"containers", res.containers}, {"networks", res.networks}, {"volumes", res.volumes}} {
		if len(kind.list) > 0 {
			fmt.Fprintf(w, "  %s: %s\n", kind.name, strings.Join(kind.list, " "))
		}
	}
	for _, f := range res.failed {
		fmt.Fprintf(w, "  could not remove: %s\n", f)
	}
}
