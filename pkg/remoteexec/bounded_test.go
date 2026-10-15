// Tests for RunWithStdinBounded's output bound and for Excerpt.
package remoteexec

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// connTo returns a Conn to a fake server answering every command with
// handler's output.
func connTo(t *testing.T, handler func(cmd string) (string, string, int)) *Conn {
	t.Helper()
	fake, _ := newFakeSSHServer(t, handler)
	conn, err := newTestRunner(fake, Options{}).Connect(context.Background(), nil, testTarget, testAuth)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// TestRunWithStdinBounded_CutsOffAStreamPastItsBound: a command writing more
// than the bound to either stream fails with ErrOutputTooLarge, never returns
// a truncated result as if it were whole.
func TestRunWithStdinBounded_CutsOffAStreamPastItsBound(t *testing.T) {
	flood := strings.Repeat("x", 3<<20)
	for stream, handler := range map[string]func(string) (string, string, int){
		"stdout": func(string) (string, string, int) { return flood, "", 0 },
		"stderr": func(string) (string, string, int) { return "", flood, 1 },
	} {
		t.Run(stream, func(t *testing.T) {
			result, err := connTo(t, handler).RunWithStdinBounded(context.Background(), "flood", nil, 1<<20)
			if !errors.Is(err, ErrOutputTooLarge) {
				t.Fatalf("result = %d/%d bytes, err = %v; want ErrOutputTooLarge", len(result.Stdout), len(result.Stderr), err)
			}
			if result.Stdout != "" || result.Stderr != "" {
				t.Fatal("a cut-off command returned output")
			}
		})
	}
}

// TestRunWithStdinBounded_KeepsWhatFits returns output within the bound
// whole, with its exit status.
func TestRunWithStdinBounded_KeepsWhatFits(t *testing.T) {
	conn := connTo(t, func(string) (string, string, int) { return "out", "err", 3 })
	result, err := conn.RunWithStdinBounded(context.Background(), "x", nil, 1<<20)
	if err != nil || result.Stdout != "out" || result.Stderr != "err" || result.ExitCode != 3 {
		t.Fatalf("result = %+v, err = %v", result, err)
	}
}

// TestRunWithStdinBounded_RefusesNoBound: a bound of zero or less is a
// caller's mistake, not a request for RunWithStdin.
func TestRunWithStdinBounded_RefusesNoBound(t *testing.T) {
	conn := connTo(t, func(string) (string, string, int) { return "", "", 0 })
	for _, limit := range []int{0, -1} {
		if _, err := conn.RunWithStdinBounded(context.Background(), "x", nil, limit); err == nil {
			t.Fatalf("limit %d was accepted", limit)
		}
	}
}

// TestRunWithStdin_StaysUnbounded: RunWithStdin's contract is unchanged by
// sharing its body with the bounded form.
func TestRunWithStdin_StaysUnbounded(t *testing.T) {
	flood := strings.Repeat("x", 3<<20)
	result, err := connTo(t, func(string) (string, string, int) { return flood, "", 0 }).RunWithStdin(context.Background(), "x", nil)
	if err != nil || len(result.Stdout) != len(flood) {
		t.Fatalf("got %d bytes, err = %v; want %d", len(result.Stdout), err, len(flood))
	}
}

// TestExcerpt escapes what a terminal would act on and counts what it cut.
func TestExcerpt(t *testing.T) {
	for _, tc := range []struct {
		in   string
		max  int
		want string
	}{
		{"plain text", 1024, "plain text"},
		{"red \x1b[31mtext", 1024, `red \u001b[31mtext`},
		{"a\nb\tc\x00", 1024, `a\u000ab\u0009c\u0000`},
		{"bell\x07 and del\x7f", 1024, `bell\u0007 and del\u007f`},
		{"0123456789", 5, "01234... (5 more bytes)"},
	} {
		if got := Excerpt(tc.in, tc.max); got != tc.want {
			t.Errorf("Excerpt(%q, %d) = %q, want %q", tc.in, tc.max, got, tc.want)
		}
	}
}
