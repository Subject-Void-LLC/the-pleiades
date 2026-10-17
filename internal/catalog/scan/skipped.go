// What a scanner could not read. Gitleaks logs a file or directory it
// could not open at warn level, and OSV-Scanner a lockfile it could not
// read or extract, and each skips it and exits as if it had read it, so a
// tree it could not read looks like a clean one unless its log is read.
package scan

import (
	"bytes"
	"strconv"
	"strings"
	"sync"
)

// maxSkipLine bounds one line of the scanner's log as the watch reads it;
// the start of a line, which says what it is, is what is kept.
const maxSkipLine = 4096

// skipWatch reads a scanner's standard error for what it skipped, each
// line judged by skips. It is safe for concurrent use.
type skipWatch struct {
	// skips reports whether a line of the log is a skip, and the path it
	// names, as the scanner saw it.
	skips func(line string) (string, bool)

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
	p, ok := w.skips(l)
	if !ok {
		return
	}
	w.count++
	if len(w.paths) < maxListed {
		w.paths = append(w.paths, p)
	}
}

// skipped is how many paths the scanner skipped and up to maxListed of
// them, each named on the device by onDevice and bounded, an unfinished
// last line included.
func (w *skipWatch) skipped(onDevice func(reported string) string) (int, []string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.partial) > 0 {
		w.line(string(w.partial))
		w.partial = w.partial[:0]
	}
	out := make([]string, len(w.paths))
	for i, p := range w.paths {
		out[i] = reported(onDevice(p))
	}
	return w.count, out
}

// gitleaksSkip judges a line of Gitleaks' log. At --log-level warn every
// skip it logs is one it could not help: those it makes by its rules (an
// allowlisted path, an empty or binary file, a symbolic link) are logged
// at debug level, measured against 8.30.0.
func gitleaksSkip(line string) (string, bool) {
	if !strings.Contains(line, "skipping") && !strings.Contains(line, "permission denied") {
		return "", false
	}
	return skippedPath(line), true
}

// osvExtractionError opens each line in which OSV-Scanner says it could
// not read or extract a file, which it then leaves out and goes on.
const osvExtractionError = "Error during extraction: "

// osvSkip judges a line of OSV-Scanner's log: "Error during extraction:
// (extracting as <extractor>) <path>: <why>", the path relative to the
// container's root, measured against 2.6.0. A line of another shape that
// opens the same way is a skip too, named by the line itself.
func osvSkip(line string) (string, bool) {
	rest, ok := strings.CutPrefix(line, osvExtractionError)
	if !ok {
		return "", false
	}
	if strings.HasPrefix(rest, "(") {
		_, after, found := strings.Cut(rest, ") ")
		if !found {
			return line, true
		}
		rest = after
	}
	p, _, found := strings.Cut(rest, ": ")
	if !found || p == "" {
		return line, true
	}
	return "/" + strings.TrimPrefix(p, "/"), true
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
