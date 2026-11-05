//go:build !windows

// Tests for the detached start script's guards and the parsing of its
// answers: a command that reached the device cut short is never started, and
// a login shell's startup output never stands in for an exit status or a job
// file.
package remoteexec

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDetached_ACommandThatArrivesCutShortIsNeverStarted: the device's cat
// keeps only a prefix of what it is sent, as a channel that ends partway
// through the send leaves it. The prefix is a different command, so the start
// refuses it and nothing runs: the marker its whole would write, and its
// prefix would not, is absent, and so is any job directory.
func TestDetached_ACommandThatArrivesCutShortIsNeverStarted(t *testing.T) {
	conn, _, _, home := detachedConn(t)
	head, err := lookPathOutside("head")
	if err != nil {
		t.Skipf("no head on this machine: %v", err)
	}
	fake := t.TempDir()
	script := "#!/bin/sh\nexec " + head + " -c 12\n"
	if err := os.WriteFile(filepath.Join(fake, "cat"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fake+string(os.PathListSeparator)+os.Getenv("PATH"))
	marker := filepath.Join(home, "whole")
	_, err = conn.Run(WithDetach(context.Background()), "echo prefix; touch "+QuoteArg(marker))
	if err == nil || !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("Run of a command cut short = %v; want it refused as incomplete", err)
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatal("the whole command ran")
	}
	if left := jobsLeft(t, home); len(left) != 0 {
		t.Fatalf("job directories left behind: %v", left)
	}
}

// lookPathOutside finds name on PATH as an absolute path, before a test
// shadows it.
func lookPathOutside(name string) (string, error) {
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		path := filepath.Join(dir, name)
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path, nil
		}
	}
	return "", errors.New(name + " not found")
}

// TestReadJobFile_RefusesWhatItCannotRead: a missing file, and a file over
// the caller's bound, are errors and never a truncated read.
func TestReadJobFile_RefusesWhatItCannotRead(t *testing.T) {
	conn, _, _, home := detachedConn(t)
	if _, err := conn.readJobFile(context.Background(), QuoteArg(filepath.Join(home, "absent")), "x", 100); err == nil {
		t.Fatal("reading a missing job file succeeded")
	}
	big := filepath.Join(home, "big")
	if err := os.WriteFile(big, []byte(strings.Repeat("x", 200)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.readJobFile(context.Background(), QuoteArg(big), "x", 100); !errors.Is(err, ErrOutputTooLarge) {
		t.Fatalf("reading a job file over its bound = %v; want ErrOutputTooLarge", err)
	}
	got, err := conn.readJobFile(context.Background(), QuoteArg(big), "x", 200)
	if err != nil || got != strings.Repeat("x", 200) {
		t.Fatalf("reading a job file at its bound = %d bytes, %v", len(got), err)
	}
}

// TestPollRC_ReadsOnlyTheMarkedLine: the status comes from the marked last
// line after any startup output, and an answer with no such line, or a value
// that is not a number, is an error.
func TestPollRC_ReadsOnlyTheMarkedLine(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want int
		ok   bool
	}{
		{rcPrefix + "0\n", 0, true},
		{"welcome to this host\n" + rcPrefix + "3\n", 3, true},
		{"3\n", 0, false},
		{rcPrefix + "three\n", 0, false},
	} {
		got, err := pollRC(tc.in)
		if (err == nil) != tc.ok || (tc.ok && got != tc.want) {
			t.Errorf("pollRC(%q) = %d, %v; want %d, ok %v", tc.in, got, err, tc.want, tc.ok)
		}
	}
}

// TestAfterMarker_SkipsStartupOutput: the contents begin after the marker's
// own line, whether or not anything was printed ahead of it, and an answer
// with no marker line is refused.
func TestAfterMarker_SkipsStartupOutput(t *testing.T) {
	for _, tc := range []struct {
		in, want string
		ok       bool
	}{
		{"m\nbody\n", "body\n", true},
		{"banner\nm\nbody\nm\n", "body\nm\n", true},
		{"banner m\nbody", "", false},
	} {
		got, ok := afterMarker(tc.in, "m")
		if ok != tc.ok || got != tc.want {
			t.Errorf("afterMarker(%q) = %q, %v; want %q, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}
