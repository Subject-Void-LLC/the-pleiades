// A bounded tail of a check's output: the last lines, never all of it.
package gate

import (
	"fmt"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

// Tail keeps the last lines written to it, at most maxLines lines and
// maxBytes bytes of them, counting what it dropped. It is an io.Writer, so
// a check's output streams through it in constant memory however much the
// check prints. It is safe for concurrent use.
type Tail struct {
	maxLines, maxBytes int

	mu      sync.Mutex
	lines   []string
	size    int
	partial []byte
	dropped int
}

// NewTail returns a Tail keeping at most maxLines lines and maxBytes bytes.
func NewTail(maxLines, maxBytes int) *Tail {
	return &Tail{maxLines: max(maxLines, 1), maxBytes: max(maxBytes, 1)}
}

// Write takes output in any chunks; a line is complete at its newline.
func (t *Tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	rest := p
	for len(rest) > 0 {
		i := indexByte(rest, '\n')
		if i < 0 {
			t.partial = appendBounded(t.partial, rest, t.maxBytes)
			break
		}
		t.partial = appendBounded(t.partial, rest[:i], t.maxBytes)
		t.push(string(t.partial))
		t.partial = t.partial[:0]
		rest = rest[i+1:]
	}
	return len(p), nil
}

// push adds one complete line, dropping the oldest past either bound.
func (t *Tail) push(line string) {
	line = strings.TrimSuffix(line, "\r")
	t.lines = append(t.lines, line)
	t.size += len(line) + 1
	for len(t.lines) > t.maxLines || (t.size > t.maxBytes && len(t.lines) > 1) {
		t.size -= len(t.lines[0]) + 1
		t.lines = t.lines[1:]
		t.dropped++
	}
}

// Lines returns the kept lines, an unfinished last line included and
// counted against the bound, escaped (Escape).
func (t *Tail) Lines() []string {
	lines, _ := t.snapshot()
	return lines
}

// snapshot is the kept lines, escaped, with an unfinished last line held
// to the same line bound as the rest, and how many lines were left out.
func (t *Tail) snapshot() ([]string, int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]string, 0, len(t.lines)+1)
	for _, l := range t.lines {
		out = append(out, Escape(l))
	}
	dropped := t.dropped
	if len(t.partial) > 0 {
		out = append(out, Escape(string(t.partial)))
		if len(out) > t.maxLines {
			out = out[1:]
			dropped++
		}
	}
	return out, dropped
}

// LastLine is the last non-empty kept line, escaped, or "".
func (t *Tail) LastLine() string {
	lines := t.Lines()
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			return strings.TrimSpace(lines[i])
		}
	}
	return ""
}

// String is the tail for a person: the kept lines, headed by how many
// earlier ones were left out (Omitted) when any were.
func (t *Tail) String() string {
	lines, dropped := t.snapshot()
	if dropped > 0 {
		lines = append([]string{Omitted(dropped)}, lines...)
	}
	return strings.Join(lines, "\n")
}

// Omitted is the line heading a tail that left n earlier lines out.
func Omitted(n int) string { return fmt.Sprintf("... %d earlier line(s) left out", n) }

// LastLines returns the last n of lines and how many were cut before them.
func LastLines(lines []string, n int) (kept []string, cut int) {
	if n < 0 {
		n = 0
	}
	if len(lines) <= n {
		return lines, 0
	}
	return lines[len(lines)-n:], len(lines) - n
}

// Escape makes text from a check safe to print: invalid UTF-8 and every
// control or format character (an escape sequence's ESC, a tab, a NUL, a
// bidirectional override) become \u escapes, so nothing in it acts on the
// terminal or log that shows it.
func Escape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && size == 1:
			fmt.Fprintf(&b, `\x%02x`, s[i])
		case unicode.IsControl(r) || unicode.Is(unicode.Cf, r):
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			b.WriteRune(r)
		}
		i += size
	}
	return b.String()
}

// appendBounded appends add to buf, keeping only the last max bytes, so
// one endless line is bounded too.
func appendBounded(buf, add []byte, max int) []byte {
	buf = append(buf, add...)
	if len(buf) > max {
		buf = append(buf[:0], buf[len(buf)-max:]...)
	}
	return buf
}

// indexByte is bytes.IndexByte without the import.
func indexByte(b []byte, c byte) int {
	for i, x := range b {
		if x == c {
			return i
		}
	}
	return -1
}
