// container.info, list, logs, exec and recreate against this machine's
// real Docker engine.
package container_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/client"
)

// TestReadAndExec: info reports a container with its environment by name
// only, and an absent one as not existing; list finds it by glob; exec
// runs a command and fails on a non-zero exit after recording its output;
// logs returns what the container wrote.
func TestReadAndExec(t *testing.T) {
	h := newHarness(t)
	name := h.name(t)
	if _, err := call(t, h, fresh(), "container.run", false, map[string]any{
		"name": name, "image": h.nginx, "env": map[string]any{"TOKEN": "secret-sentinel"},
	}); err != nil {
		t.Fatal(err)
	}

	rc := fresh()
	if _, err := call(t, h, rc, "container.info", false, map[string]any{"name": name}); err != nil || rc.stats["exists"] != true || rc.stats["state"] != "running" {
		t.Fatalf("info = %v, %v", rc.stats, err)
	}
	if env, _ := rc.stats["env_names"].([]string); !contains(env, "TOKEN") {
		t.Errorf("env_names = %v", rc.stats["env_names"])
	}
	for k, v := range rc.stats {
		if s, ok := v.(string); ok && strings.Contains(s, "secret-sentinel") {
			t.Errorf("info's %s holds the environment value", k)
		}
	}
	// The whole container by default: what a real engine answers for a
	// running container with no network and nothing published.
	if pid, _ := rc.stats["pid"].(int); pid <= 0 || rc.stats["network"] != "none" || rc.stats["read_only"] != false {
		t.Errorf("info of a running container: pid %v, network %v, read_only %v", rc.stats["pid"], rc.stats["network"], rc.stats["read_only"])
	}
	if ports, ok := rc.stats["ports"].([]string); !ok || len(ports) != 0 {
		t.Errorf("ports = %#v, want an empty list", rc.stats["ports"])
	}
	if _, ok := rc.stats["networks"].([]any); !ok {
		t.Errorf("networks = %#v, want a list", rc.stats["networks"])
	}
	rc = fresh()
	if _, err := call(t, h, rc, "container.info", false, map[string]any{"name": name + "-absent"}); err != nil || rc.stats["exists"] != false {
		t.Errorf("info of an absent container = %v, %v", rc.stats, err)
	}

	rc = fresh()
	if _, err := call(t, h, rc, "container.list", false, map[string]any{"match": name}); err != nil {
		t.Fatal(err)
	}
	if list, _ := rc.stats["containers"].([]any); len(list) != 1 || list[0].(map[string]any)["name"] != name {
		t.Errorf("list = %v", rc.stats["containers"])
	} else if entry := list[0].(map[string]any); !strings.HasPrefix(entry["status"].(string), "Up") || entry["created"].(int64) <= 0 {
		t.Errorf("list entry = %v, want the engine's status line and creation time", entry)
	}

	rc = fresh()
	if _, err := call(t, h, rc, "container.exec", false, map[string]any{"name": name, "cmd": "echo hi; echo there >&2"}); err != nil || rc.stats["stdout"] != "hi\n" || rc.stats["stderr"] != "there\n" {
		t.Errorf("exec = %v, %v", rc.stats, err)
	}
	rc = fresh()
	// The command's stderr, two real lines from ls and the shell, reaches
	// the error as pkg/farside quotes it: one line, in quotes. ls's own
	// wording is the image's (BusyBox in the pinned nginx image, GNU in
	// others), so only its ends are held.
	if _, err := call(t, h, rc, "container.exec", false, map[string]any{"name": name, "cmd": "ls /no-such-dir; echo failing >&2; exit 3"}); err == nil ||
		!strings.Contains(err.Error(), `exited 3: "ls: `) || !strings.HasSuffix(err.Error(), `No such file or directory failing"`) || rc.stats["exit_code"] != 3 {
		t.Errorf("a failing exec = %v, %v", rc.stats, err)
	}

	rc = fresh()
	if _, err := call(t, h, rc, "container.logs", false, map[string]any{"name": name, "tail": 20}); err != nil {
		t.Fatalf("logs = %v", err)
	}
	if out := rc.stats["stdout"].(string) + rc.stats["stderr"].(string); !strings.Contains(out, "worker process") {
		t.Errorf("logs = %q", out)
	}
	if _, err := call(t, h, fresh(), "container.logs", false, map[string]any{"name": name, "tail": 0}); err == nil {
		t.Error("a tail of 0 was read")
	}
}

// TestRecreate: a container that matches is left alone; one that differs
// is replaced, the difference named and the old ID gone; force replaces
// one that matches; a check replaces nothing; and nothing records an undo.
func TestRecreate(t *testing.T) {
	h := newHarness(t)
	name := h.name(t)
	declared := map[string]any{"name": name, "image": h.nginx, "labels": map[string]any{"v": "1"}}
	if _, err := call(t, h, fresh(), "container.run", false, declared); err != nil {
		t.Fatal(err)
	}
	first := fresh()
	if res, err := call(t, h, first, "container.recreate", false, declared); err != nil || res.Changed {
		t.Fatalf("recreating a matching container = %+v, %v; want no change", res, err)
	}
	changed := map[string]any{"name": name, "image": h.nginx, "labels": map[string]any{"v": "2"}}
	if res, err := call(t, h, fresh(), "container.recreate", true, changed); err != nil || !res.Changed || h.state(t, name) != "running" {
		t.Fatalf("a check of recreate = %+v, %v", res, err)
	}
	rc := fresh()
	res, err := call(t, h, rc, "container.recreate", false, changed)
	if err != nil || !res.Changed || h.state(t, name) != "running" {
		t.Fatalf("recreate = %+v, %v; state %q", res, err, h.state(t, name))
	}
	if diffs, _ := rc.stats["differences"].([]string); len(diffs) != 1 || !strings.Contains(diffs[0], "labels: v") {
		t.Errorf("differences = %v", rc.stats["differences"])
	}
	if rc.stats["id"] == first.stats["id"] {
		t.Error("the container kept its ID")
	}
	if _, ok := rc.stats["inverse"]; ok {
		t.Errorf("recreate recorded an undo: %v", rc.stats["inverse"])
	}
	rc = fresh()
	if res, err := call(t, h, rc, "container.recreate", false, map[string]any{"name": name, "image": h.nginx, "labels": map[string]any{"v": "2"}, "force": true}); err != nil || !res.Changed {
		t.Errorf("a forced recreate = %+v, %v", res, err)
	}
}

// contains reports whether list holds s.
func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// TestRun_ToItsExit: a one-shot returns its exit code and output and is
// removed; an exit code not accepted fails the task after its output is
// recorded, and one accepted does not; a name in use is refused; a run
// past its timeout is killed; and a check of a one-shot is refused.
func TestRun_ToItsExit(t *testing.T) {
	h := newHarness(t)
	name := h.name(t)
	shot := func(extra map[string]any) map[string]any {
		p := map[string]any{"name": name, "image": h.nginx, "wait": true}
		for k, v := range extra {
			p[k] = v
		}
		return p
	}
	rc := fresh()
	res, err := call(t, h, rc, "container.run", false, shot(map[string]any{"command": []any{"/bin/sh", "-c", "echo out; echo err >&2"}}))
	if err != nil || !res.Changed || rc.stats["exit_code"] != 0 || rc.stats["stdout"] != "out\n" || rc.stats["stderr"] != "err\n" || h.state(t, name) != "" {
		t.Fatalf("a one-shot = %+v, %v, %v; state %q", res, err, rc.stats, h.state(t, name))
	}
	rc = fresh()
	_, err = call(t, h, rc, "container.run", false, shot(map[string]any{"command": []any{"/bin/sh", "-c", "cat /etc/no-such.conf; echo nope >&2; exit 4"}}))
	// The stderr of cat and the shell, quoted on one line by pkg/farside;
	// cat's wording is the image's, so only its ends are held.
	if err == nil || !strings.Contains(err.Error(), `exited 4 and was removed: "cat: `) || !strings.HasSuffix(err.Error(), `No such file or directory nope"`) ||
		rc.stats["exit_code"] != 4 || h.state(t, name) != "" {
		t.Errorf("a failing one-shot = %v, %v; state %q", err, rc.stats, h.state(t, name))
	}
	// remove: false keeps only a run that succeeded: a failed one leaves
	// nothing behind, since its undo is dropped with the failure.
	rc = fresh()
	_, err = call(t, h, rc, "container.run", false, shot(map[string]any{"command": []any{"/bin/sh", "-c", "exit 4"}, "remove": false}))
	if err == nil || !strings.Contains(err.Error(), "was removed") || h.state(t, name) != "" {
		t.Errorf("a failing one-shot kept by remove false = %v; state %q", err, h.state(t, name))
	}
	if fqcn, _ := inverseOf(rc); fqcn != "" {
		t.Errorf("a failed one-shot recorded %s as its undo", fqcn)
	}
	rc = fresh()
	if _, err := call(t, h, rc, "container.run", false, shot(map[string]any{"command": []any{"true"}, "remove": false})); err != nil || h.state(t, name) != "exited" {
		t.Fatalf("a succeeding one-shot kept by remove false = %v; state %q", err, h.state(t, name))
	}
	if fqcn, p := inverseOf(rc); fqcn != "container.remove" || p["id"] == nil {
		t.Errorf("the kept one-shot's undo = %s %v", fqcn, p)
	}
	if _, err := call(t, h, fresh(), "container.remove", false, map[string]any{"name": name}); err != nil {
		t.Fatal(err)
	}
	if _, err := call(t, h, fresh(), "container.run", false, shot(map[string]any{"command": []any{"/bin/sh", "-c", "exit 4"}, "accept_exit_codes": []any{0, 4}})); err != nil {
		t.Errorf("an accepted exit code = %v", err)
	}
	rc = fresh()
	_, err = call(t, h, rc, "container.run", false, shot(map[string]any{"command": []any{"sleep", "30"}, "wait_timeout": 1}))
	if err == nil || !strings.Contains(err.Error(), "was killed") || rc.stats["timed_out"] != true {
		t.Errorf("a one-shot past its timeout = %v, %v", err, rc.stats)
	}
	if _, err := call(t, h, fresh(), "container.run", false, map[string]any{"name": name, "image": h.nginx}); err != nil {
		t.Fatal(err)
	}
	if _, err := call(t, h, fresh(), "container.run", false, shot(nil)); err == nil || !strings.Contains(err.Error(), "makes a fresh one") {
		t.Errorf("a one-shot onto a name in use = %v", err)
	}
	if _, err := call(t, h, fresh(), "container.run", true, shot(nil)); err == nil || !strings.Contains(err.Error(), "known only once it has run") {
		t.Errorf("a check of a one-shot = %v", err)
	}
}

// TestPrune_RemovesWhatItsCheckListed: a stopped container carrying the
// test's own label is listed by a check and removed by the run; a running
// one with the same label is left; a second run is no change.
func TestPrune_RemovesWhatItsCheckListed(t *testing.T) {
	h := newHarness(t)
	stopped, running := h.name(t), h.name(t)
	label := "pleiades.test=" + stopped
	for _, n := range []string{stopped, running} {
		if _, err := call(t, h, fresh(), "container.run", false, map[string]any{"name": n, "image": h.nginx, "labels": map[string]any{"pleiades.test": stopped}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := call(t, h, fresh(), "container.stop", false, map[string]any{"name": stopped, "timeout": 1}); err != nil {
		t.Fatal(err)
	}
	params := map[string]any{"what": "containers", "labels": []any{label}}
	rc := fresh()
	began := time.Now()
	if res, err := call(t, h, rc, "container.prune", true, params); err != nil || !res.Changed || h.state(t, stopped) != "exited" {
		// The listing is read again here, only on a failure, so a repeat of
		// FAILURE_PATTERNS 560 names what the engine said and when.
		t.Fatalf("a check of prune = %+v, %v; state %q; the engine's list: %s", res, err, h.state(t, stopped), h.listed(t, stopped, label, began))
	}
	if pruned, _ := rc.stats["pruned"].([]string); len(pruned) != 1 || pruned[0] != stopped {
		t.Errorf("the check listed %v", rc.stats["pruned"])
	}
	if res, err := call(t, h, fresh(), "container.prune", false, params); err != nil || !res.Changed || h.state(t, stopped) != "" || h.state(t, running) != "running" {
		t.Fatalf("prune = %+v, %v; stopped %q, running %q", res, err, h.state(t, stopped), h.state(t, running))
	}
	if res, err := call(t, h, fresh(), "container.prune", false, params); err != nil || res.Changed {
		t.Errorf("a second prune = %+v, %v; want no change", res, err)
	}
	if _, err := call(t, h, fresh(), "container.prune", false, map[string]any{"what": "everything"}); err == nil {
		t.Error("an unknown kind was pruned")
	}
}

// TestCopyInAndOut: a file put in is read back whole; a second put of the
// same bytes is no change; a check of new bytes writes nothing; a binary
// file is refused by copy_out; a relative path is refused.
func TestCopyInAndOut(t *testing.T) {
	h := newHarness(t)
	name := h.name(t)
	if _, err := call(t, h, fresh(), "container.run", false, map[string]any{"name": name, "image": h.nginx}); err != nil {
		t.Fatal(err)
	}
	put := map[string]any{"name": name, "dest": "/tmp/pleiades.txt", "content": "hello\n", "mode": "0600"}
	if res, err := call(t, h, fresh(), "container.copy_in", false, put); err != nil || !res.Changed {
		t.Fatalf("copy_in = %+v, %v", res, err)
	}
	rc := fresh()
	if _, err := call(t, h, rc, "container.copy_out", false, map[string]any{"name": name, "src": "/tmp/pleiades.txt"}); err != nil || rc.stats["content"] != "hello\n" {
		t.Fatalf("copy_out = %v, %v", rc.stats, err)
	}
	if res, err := call(t, h, fresh(), "container.copy_in", false, put); err != nil || res.Changed {
		t.Errorf("a second copy_in of the same bytes = %+v, %v; want no change", res, err)
	}
	put["content"] = "changed\n"
	if res, err := call(t, h, fresh(), "container.copy_in", true, put); err != nil || !res.Changed {
		t.Errorf("a check of new bytes = %+v, %v", res, err)
	}
	rc = fresh()
	if _, err := call(t, h, rc, "container.copy_out", false, map[string]any{"name": name, "src": "/tmp/pleiades.txt"}); err != nil || rc.stats["content"] != "hello\n" {
		t.Errorf("the check wrote the file: %v, %v", rc.stats, err)
	}
	put["content"] = "\x00\xff\xfe"
	if _, err := call(t, h, fresh(), "container.copy_in", false, put); err != nil {
		t.Fatal(err)
	}
	if _, err := call(t, h, fresh(), "container.copy_out", false, map[string]any{"name": name, "src": "/tmp/pleiades.txt"}); err == nil || !strings.Contains(err.Error(), "not text") {
		t.Errorf("copy_out of binary = %v", err)
	}
	if _, err := call(t, h, fresh(), "container.copy_in", false, map[string]any{"name": name, "dest": "tmp/x", "content": "x"}); err == nil {
		t.Error("a relative dest was written")
	}
}

// TestRun_PullNever: an image the engine holds under a local tag no
// registry knows runs with pull never, by its ID; one it does not hold is
// refused, never pulled.
func TestRun_PullNever(t *testing.T) {
	h := newHarness(t)
	name := h.name(t)
	local := "pleiades-local/" + name + ":1"
	if _, err := h.api.ImageTag(context.Background(), client.ImageTagOptions{Source: h.nginx, Target: local}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = h.api.ImageRemove(context.Background(), local, client.ImageRemoveOptions{}) })
	rc := fresh()
	if _, err := call(t, h, rc, "container.run", false, map[string]any{"name": name, "image": local, "pull": "never"}); err != nil || h.state(t, name) != "running" {
		t.Fatalf("a local image with pull never = %v; state %q", err, h.state(t, name))
	}
	if id, _ := rc.stats["image_id"].(string); !strings.HasPrefix(id, "sha256:") {
		t.Errorf("image_id = %v", rc.stats["image_id"])
	}
	if _, err := call(t, h, fresh(), "container.run", false, map[string]any{"name": h.name(t), "image": "pleiades-local/absent-" + name + ":1", "pull": "never"}); err == nil || !strings.Contains(err.Error(), "pull is never") {
		t.Errorf("an absent image with pull never = %v", err)
	}
}
