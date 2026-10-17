// What Gitleaks could not read. It logs a file or directory it could not
// open at warn level, skips it and exits as if it had read it, so a tree
// it could not read looks like a clean one unless its log is read.
package scan

import (
	"bytes"
	"strconv"
	"strings"
	"sync"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
)

// maxSkipLine bounds one line of the scanner's log as the watch reads it;
// the start of a line, which says what it is, is what is kept.
const maxSkipLine = 4096

// maxSkippedPath bounds one skipped path as an error names it.
const maxSkippedPath = 256

// skipWatch reads Gitleaks' standard error for what it skipped. At
// --log-level warn every skip it logs is one it could not help: those it
// makes by its rules (an allowlisted path, an empty or binary file, a
// symbolic link) are logged at debug level, measured against 8.30.0. It
// is safe for concurrent use.
type skipWatch struct {
	mu      sync.Mutex
	partial []byte
	count   int
	paths   []string
}

// Write takes the log in any chunks; a line is complete at its newline.
func (w *skipWatch) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	rest := p
	for len(rest) > 0 {
		i := bytes.IndexByte(rest, '\n')
		if i < 0 {
			w.keep(rest)
			break
		}
		w.keep(rest[:i])
		w.line(string(w.partial))
		w.partial = w.partial[:0]
		rest = rest[i+1:]
	}
	return len(p), nil
}

// keep adds b to the line being read, up to maxSkipLine bytes of it.
func (w *skipWatch) keep(b []byte) {
	if room := maxSkipLine - len(w.partial); room > 0 {
		w.partial = append(w.partial, b[:min(room, len(b))]...)
	}
}

// line counts one complete line that reports a skip, keeping its path
// while fewer than maxListed are kept.
func (w *skipWatch) line(l string) {
	if !strings.Contains(l, "skipping") && !strings.Contains(l, "permission denied") {
		return
	}
	w.count++
	if len(w.paths) < maxListed {
		w.paths = append(w.paths, skippedPath(l))
	}
}

// skipped is how many paths Gitleaks skipped and up to maxListed of them,
// each named on the device and bounded, an unfinished last line included.
func (w *skipWatch) skipped(t target) (int, []string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.partial) > 0 {
		w.line(string(w.partial))
		w.partial = w.partial[:0]
	}
	out := make([]string, len(w.paths))
	for i, p := range w.paths {
		out[i] = remoteexec.Excerpt(t.onDevice(p), maxSkippedPath)
	}
	return w.count, out
}

// skippedPath is the path field of a Gitleaks log line (path=/scan/x, or
// quoted when it holds a space), or the whole line when it has none.
func skippedPath(line string) string {
	_, field, found := strings.Cut(line, " path=")
	if !found {
		return line
	}
	if strings.HasPrefix(field, `"`) {
		if quoted, err := strconv.QuotedPrefix(field); err == nil {
			if p, err := strconv.Unquote(quoted); err == nil {
				return p
			}
		}
		return field
	}
	p, _, _ := strings.Cut(field, " ")
	return p
}
