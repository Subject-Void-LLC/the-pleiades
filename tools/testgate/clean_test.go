// Package main: the cleanup after a container pass removes only what the
// pass's tests made, never a named volume, an image or an earlier run's.
package main

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// fakeEngine answers the docker commands cleanContainers runs from fixed
// tables, and records every removal it is asked for.
type fakeEngine struct {
	listed  map[string]string // the joined arguments of a list command -> its output
	inspect map[string]string // an ID -> its "ID created name" line
	removed []string
}

func (f *fakeEngine) run(args ...string) (string, error) {
	joined := strings.Join(args, " ")
	switch {
	case args[0] == "rm" || (len(args) > 1 && args[1] == "rm"):
		f.removed = append(f.removed, joined)
		return "", nil
	case strings.Contains(joined, "inspect"):
		var lines []string
		for _, a := range args {
			if line, ok := f.inspect[a]; ok {
				lines = append(lines, line)
			}
		}
		return strings.Join(lines, "\n") + "\n", nil
	}
	return f.listed[joined], nil
}

// TestCleanContainers_RemovesOnlyThisPassesTestObjects checks the selection:
// a labelled container and network made after the pass began go, with an
// anonymous volume made then; a labelled container from before the pass, a
// named volume and an anonymous volume from before are all kept; Podman's
// time form is read as well as Docker's.
func TestCleanContainers_RemovesOnlyThisPassesTestObjects(t *testing.T) {
	since := time.Date(2026, 10, 28, 18, 0, 0, 0, time.UTC)
	anonNew := strings.Repeat("a", 64)
	anonOld := strings.Repeat("b", 64)
	f := &fakeEngine{
		listed: map[string]string{
			"ps -aq --filter label=org.testcontainers=true":           "c1\nc2\n",
			"ps -aq --filter label=com.docker.compose.project":        "c3\nc1\n",
			"network ls -q --filter label=org.testcontainers=true":    "n1\n",
			"network ls -q --filter label=com.docker.compose.project": "",
			"volume ls -q --filter dangling=true":                     anonNew + "\n" + anonOld + "\npostgres-data\n",
		},
		inspect: map[string]string{
			"c1":    "c1 2026-10-28T18:05:00.5Z /nats-test",
			"c2":    "c2 2026-10-28T17:00:00Z /from-an-earlier-run",
			"c3":    "c3 2026-10-28 14:10:00.123 -0400 EDT compose-controller-1",
			"n1":    "n1 2026-10-28T18:01:00Z tc-net",
			anonNew: anonNew + " 2026-10-28T18:30:00Z " + anonNew,
			anonOld: anonOld + " 2026-10-27T18:30:00Z " + anonOld,
		},
	}
	res, err := cleanContainers(f.run, since)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(res.containers, " "); got != "compose-controller-1 nats-test" {
		t.Errorf("removed containers %q, want the two made since the pass began", got)
	}
	if got := strings.Join(res.networks, " "); got != "tc-net" {
		t.Errorf("removed networks %q, want tc-net", got)
	}
	if got := strings.Join(res.volumes, " "); got != anonNew {
		t.Errorf("removed volumes %q, want only the new anonymous one", got)
	}
	all := strings.Join(f.removed, "\n")
	for _, kept := range []string{"c2", anonOld, "postgres-data"} {
		if strings.Contains(all, kept) {
			t.Errorf("asked the engine to remove %s, which this pass did not make or which is named:\n%s", kept, all)
		}
	}
	if !strings.Contains(all, "rm -f -v") {
		t.Errorf("containers were not removed with their anonymous volumes (rm -f -v):\n%s", all)
	}
	var out bytes.Buffer
	printClean(&out, res, since)
	if !strings.Contains(out.String(), "removed 2 container(s), 1 network(s) and 1 anonymous volume(s)") {
		t.Errorf("printed %q", out.String())
	}
}

// TestCleanContainers_NothingToRemoveRemovesNothing checks an engine with no
// test objects is left exactly as it was.
func TestCleanContainers_NothingToRemoveRemovesNothing(t *testing.T) {
	f := &fakeEngine{listed: map[string]string{}}
	res, err := cleanContainers(f.run, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(f.removed) != 0 || len(res.containers)+len(res.networks)+len(res.volumes) != 0 {
		t.Errorf("removed %v from an engine with nothing to remove", f.removed)
	}
}

// TestEngineTime_ReadsDockerAndPodman checks the engine's clock is read in
// both forms docker info prints it in.
func TestEngineTime_ReadsDockerAndPodman(t *testing.T) {
	for _, out := range []string{"2026-10-28T22:13:09.123456789Z\n", "2026-10-28 18:13:09.123456 -0400 EDT\n"} {
		got, err := engineTime(func(...string) (string, error) { return out, nil })
		if err != nil {
			t.Errorf("%q: %v", out, err)
			continue
		}
		if got.UTC().Hour() != 22 || got.Minute() != 13 {
			t.Errorf("%q read as %v", out, got)
		}
	}
	if _, err := engineTime(func(...string) (string, error) { return "soon\n", nil }); err == nil {
		t.Error("an engine time that is not a time was accepted")
	}
}
