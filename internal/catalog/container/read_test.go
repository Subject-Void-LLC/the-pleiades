// container.info, list, logs, exec and recreate against this machine's
// real Docker engine.
package container_test

import (
	"strings"
	"testing"
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
	}

	rc = fresh()
	if _, err := call(t, h, rc, "container.exec", false, map[string]any{"name": name, "cmd": "echo hi; echo there >&2"}); err != nil || rc.stats["stdout"] != "hi\n" || rc.stats["stderr"] != "there\n" {
		t.Errorf("exec = %v, %v", rc.stats, err)
	}
	rc = fresh()
	if _, err := call(t, h, rc, "container.exec", false, map[string]any{"name": name, "cmd": "echo failing >&2; exit 3"}); err == nil || !strings.Contains(err.Error(), "exited 3: failing") || rc.stats["exit_code"] != 3 {
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
	_, err = call(t, h, rc, "container.run", false, shot(map[string]any{"command": []any{"/bin/sh", "-c", "echo nope >&2; exit 4"}}))
	if err == nil || !strings.Contains(err.Error(), "exited 4: nope") || rc.stats["exit_code"] != 4 || h.state(t, name) != "" {
		t.Errorf("a failing one-shot = %v, %v; state %q", err, rc.stats, h.state(t, name))
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
