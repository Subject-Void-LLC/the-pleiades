package main

import (
	"strings"
	"testing"
)

// TestPrintNodeStats covers what `run --verbose` prints for one
// successful task.
//
// Three properties, and the masking one is not optional. This prints
// values a task read off a device, and a task can register a value an
// earlier register_mask or secret_mask marked secret, so a printer that
// skipped the redaction would be a way to get a password onto a terminal
// through an ordinary, correct-looking runbook.
func TestPrintNodeStats(t *testing.T) {
	t.Run("sorted, one per line", func(t *testing.T) {
		out := captureStdout(t, func() {
			printNodeStats(map[string]any{"stdout": "hello", "rc": 0, "cmd": "echo hello"}, nil)
		})
		want := "    cmd: echo hello\n    rc: 0\n    stdout: hello\n"
		if out != want {
			t.Errorf("printNodeStats printed:\n%q\nwant:\n%q", out, want)
		}
	})

	t.Run("a multi-line value goes under its key", func(t *testing.T) {
		out := captureStdout(t, func() {
			printNodeStats(map[string]any{"stdout": "first\nsecond\n"}, nil)
		})
		want := "    stdout:\n      first\n      second\n"
		if out != want {
			t.Errorf("printNodeStats printed:\n%q\nwant:\n%q", out, want)
		}
	})

	t.Run("an empty value prints nothing", func(t *testing.T) {
		// stderr is empty on almost every successful task, and a run over
		// a large inventory printing "stderr:" once per device is noise
		// standing in for information.
		out := captureStdout(t, func() {
			printNodeStats(map[string]any{"stderr": "", "rc": 0}, nil)
		})
		if strings.Contains(out, "stderr") {
			t.Errorf("an empty value was printed:\n%q", out)
		}
		if !strings.Contains(out, "rc: 0") {
			t.Errorf("a non-empty sibling was dropped:\n%q", out)
		}
	})

	t.Run("secrets are masked", func(t *testing.T) {
		secret := "hunter2-the-real-one"
		out := captureStdout(t, func() {
			printNodeStats(map[string]any{"stdout": "connected with " + secret}, []string{secret})
		})
		if strings.Contains(out, secret) {
			t.Errorf("a registered secret reached the terminal:\n%q", out)
		}
		if !strings.Contains(out, "connected with") {
			t.Errorf("masking removed more than the secret:\n%q", out)
		}
	})

	t.Run("a multi-line value carrying a secret is masked too", func(t *testing.T) {
		// The multi-line branch splits the value AFTER redaction, and
		// that order is what this pins: splitting first would mask each
		// line separately and miss a secret that spans one.
		secret := "s3cr3t-token-value"
		out := captureStdout(t, func() {
			printNodeStats(map[string]any{"stdout": "line one\ntoken=" + secret + "\nline three"}, []string{secret})
		})
		if strings.Contains(out, secret) {
			t.Errorf("a secret survived on a multi-line value:\n%q", out)
		}
		if !strings.Contains(out, "line three") {
			t.Errorf("the later lines were dropped:\n%q", out)
		}
	})

	t.Run("no stats prints nothing", func(t *testing.T) {
		out := captureStdout(t, func() { printNodeStats(nil, nil) })
		if out != "" {
			t.Errorf("a task with no stats printed %q", out)
		}
	})
}

// TestPrintMetadata covers the other printer in run.go, the one that
// renders set_metadata results into the run report.
//
// It was at zero coverage, which is how a printer ends up: the end-to-end
// tests drive the built binary as a subprocess, and a subprocess's
// execution counts toward nothing. It shares a purpose and a masking
// requirement with printNodeStats and is tested here alongside it for
// that reason.
//
// The ordering assertions are the substance. This walks three nested
// maps, and Go randomizes map iteration, so a report that did not sort at
// every level would come out in a different order on every run. That is
// not a cosmetic problem for output somebody diffs between two runs to
// see what changed.
func TestPrintMetadata(t *testing.T) {
	t.Run("sorted by register, then device, then key", func(t *testing.T) {
		metadata := map[string]any{
			"second": map[string]any{
				"device-b": map[string]any{"zebra": 1, "alpha": 2},
			},
			"first": map[string]any{
				"device-b": map[string]any{"k": "vb"},
				"device-a": map[string]any{"k": "va"},
			},
		}
		out := captureStdout(t, func() { printMetadata(metadata, nil) })

		want := "  first:\n" +
			"    [device-a] k: va\n" +
			"    [device-b] k: vb\n" +
			"  second:\n" +
			"    [device-b] alpha: 2\n" +
			"    [device-b] zebra: 1\n"
		if out != want {
			t.Errorf("printMetadata printed:\n%q\nwant:\n%q", out, want)
		}
	})

	t.Run("a controller-side result has no device prefix", func(t *testing.T) {
		// The empty device id keys a task that ran against no device, and
		// printing "[] key: value" for it would invent a device.
		out := captureStdout(t, func() {
			printMetadata(map[string]any{"summary": map[string]any{"": map[string]any{"count": 3}}}, nil)
		})
		if strings.Contains(out, "[]") {
			t.Errorf("a controller-side result printed an empty device prefix:\n%q", out)
		}
		if !strings.Contains(out, "    count: 3") {
			t.Errorf("printMetadata printed:\n%q\nwant an unprefixed count", out)
		}
	})

	t.Run("secrets are masked", func(t *testing.T) {
		secret := "generated-password-9f2"
		out := captureStdout(t, func() {
			printMetadata(map[string]any{"r": map[string]any{"d": map[string]any{"pw": secret}}}, []string{secret})
		})
		if strings.Contains(out, secret) {
			t.Errorf("a registered secret reached the terminal through the metadata report:\n%q", out)
		}
	})

	t.Run("a value of the wrong shape is skipped rather than panicking", func(t *testing.T) {
		// Metadata arrives as map[string]any from the workflow context, so
		// its shape is a runtime fact rather than a compile-time one, and
		// a report that panicked would take down a run that had already
		// finished its work.
		out := captureStdout(t, func() {
			printMetadata(map[string]any{
				"notamap":    "a bare string",
				"innerwrong": map[string]any{"d": "not a stat map"},
				"good":       map[string]any{"d": map[string]any{"k": "v"}},
			}, nil)
		})
		if !strings.Contains(out, "k: v") {
			t.Errorf("a malformed sibling suppressed a well-formed entry:\n%q", out)
		}
	})
}

// TestPrintNodeStats_ListsAndMaps covers a stat holding a list of maps (a
// VirtualBox host's VMs) or a map (a diff): written as indented YAML, one
// entry to a line, rather than as one map[...] line.
func TestPrintNodeStats_ListsAndMaps(t *testing.T) {
	out := captureStdout(t, func() {
		printNodeStats(map[string]any{
			"vms":  []any{map[string]any{"name": "ubuntu-lab", "state": "running"}, map[string]any{"name": "base", "state": "poweroff"}},
			"keys": []string{"ssh-ed25519 AAAA", "ssh-rsa AAAA"},
			"diff": map[string]any{"before": map[string]any{"exists": false}},
		}, nil)
	})
	want := "    diff:\n      before:\n        exists: false\n" +
		"    keys:\n      - ssh-ed25519 AAAA\n      - ssh-rsa AAAA\n" +
		"    vms:\n      - name: ubuntu-lab\n        state: running\n      - name: base\n        state: poweroff\n"
	if out != want {
		t.Errorf("printed:\n%s\nwant:\n%s", out, want)
	}
}
