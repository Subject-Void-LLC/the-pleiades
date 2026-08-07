package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
)

// fileWAL is a ResultWAL backed by a single append-only JSON-lines file.
// Chosen over an embedded database (SQLite, bbolt) to keep the Runner
// dependency-free and, per PLAN.md Section 16's own framing of this
// component, "stateless, lightweight": no new build dependency, no cgo
// requirement beyond what already exists elsewhere in this codebase.
//
// Append always opens the file fresh with O_APPEND|O_CREATE|O_WRONLY,
// writes one JSON line, and fsyncs before returning, rather than holding
// a single long-lived file handle open: Acknowledge below replaces the
// file's own underlying inode via os.Rename, and a handle opened before
// that rename would keep writing into the now-unlinked old inode,
// invisible to anything that opens the path again. Opening fresh on every
// Append avoids that class of bug entirely, at a cost (one open/close per
// Append) this WAL's usage pattern, one entry per finished job, never
// makes hot.
//
// Acknowledge rewrites the whole file, filtering out the acknowledged id,
// via the same atomic temp-file-then-os.Rename idiom
// internal/credential/file_store_save.go's atomicWriteCredentialsFile
// already establishes in this codebase: a reader (or a crash) never
// observes a partially rewritten file, only the complete old content or
// the complete new content. This is O(n) in the number of still-pending
// entries per Acknowledge call, acceptable given pending-entry counts are
// bounded by outage duration, not lifetime job volume.
type fileWAL struct {
	// mu serializes every Append/Acknowledge/Pending call against this
	// file, mirroring internal/runbook/dir_source.go's own single-mutex
	// idiom for the identical reason: cheap enough not to need
	// finer-grained locking for this WAL's actual call volume.
	mu   sync.Mutex
	path string
}

// NewFileWAL builds a ResultWAL backed by a JSON-lines file under dir. It
// fails closed at construction, the same shape
// internal/runbook.NewDirSource already uses for its own directory
// argument: an unwritable WAL directory is a startup-time fatal error the
// operator must fix, never a condition Append discovers lazily on the
// first job a Runner finishes.
func NewFileWAL(dir string) (ResultWAL, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("wal directory %q could not be created: %w", dir, err)
	}

	// os.MkdirAll succeeding only proves dir exists; it says nothing
	// about whether this process can actually write into it (e.g. a
	// read-only bind mount). A real write-then-remove proves that now,
	// at construction, rather than on the first Append call a Runner
	// makes after it has already started accepting work.
	probe := filepath.Join(dir, ".wal-write-probe")
	if err := os.WriteFile(probe, nil, 0o600); err != nil {
		return nil, fmt.Errorf("wal directory %q is not writable: %w", dir, err)
	}
	if err := os.Remove(probe); err != nil {
		return nil, fmt.Errorf("wal directory %q write probe could not be cleaned up: %w", dir, err)
	}

	return &fileWAL{path: filepath.Join(dir, "results.jsonl")}, nil
}

// Append implements ResultWAL.
func (w *fileWAL) Append(ctx context.Context, entry ResultEntry) (ResultEntry, error) {
	if err := ctx.Err(); err != nil {
		return ResultEntry{}, err
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	if entry.ID == "" {
		entry.ID = uuid.New().String()
	}
	if entry.RecordedAt.IsZero() {
		entry.RecordedAt = time.Now().UTC()
	}

	line, err := json.Marshal(entry)
	if err != nil {
		return ResultEntry{}, fmt.Errorf("failed to marshal wal entry: %w", err)
	}
	line = append(line, '\n')

	// #nosec G304 -- w.path is built once, in NewFileWAL, from a
	// caller-supplied directory joined with a fixed literal filename; no
	// part of it is derived from any value this package treats as
	// untrusted.
	f, err := os.OpenFile(w.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return ResultEntry{}, fmt.Errorf("failed to open wal file: %w", err)
	}
	defer f.Close()

	if _, err := f.Write(line); err != nil {
		return ResultEntry{}, fmt.Errorf("failed to append wal entry: %w", err)
	}
	// fsync before returning: this is the durability guarantee the WAL
	// exists for. Without it, a crash immediately after Append returns
	// could lose an entry the caller already believes is safely buffered.
	if err := f.Sync(); err != nil {
		return ResultEntry{}, fmt.Errorf("failed to fsync wal file: %w", err)
	}
	return entry, nil
}

// Pending implements ResultWAL.
func (w *fileWAL) Pending(ctx context.Context) ([]ResultEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	return w.readAllLocked()
}

// readAllLocked reads and decodes every entry currently in the WAL file.
// Callers must hold w.mu. A missing file (nothing appended yet) is not an
// error: it reports zero pending entries, the same as an existing but
// empty file.
func (w *fileWAL) readAllLocked() ([]ResultEntry, error) {
	// #nosec G304 -- see Append's identical justification; w.path is
	// fixed at construction, never caller-influenced per call.
	data, err := os.ReadFile(w.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read wal file: %w", err)
	}

	var entries []ResultEntry
	for _, line := range bytes.Split(data, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var entry ResultEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			return nil, fmt.Errorf("failed to decode wal entry: %w", err)
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// Acknowledge implements ResultWAL.
func (w *fileWAL) Acknowledge(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	entries, err := w.readAllLocked()
	if err != nil {
		return err
	}

	remaining := entries[:0]
	found := false
	for _, entry := range entries {
		if entry.ID == id {
			found = true
			continue
		}
		remaining = append(remaining, entry)
	}
	// id not found is not an error; see Acknowledge's own interface doc
	// comment (wal.go) for why this must be idempotent.
	if !found {
		return nil
	}

	var buf bytes.Buffer
	for _, entry := range remaining {
		line, err := json.Marshal(entry)
		if err != nil {
			return fmt.Errorf("failed to marshal wal entry: %w", err)
		}
		buf.Write(line)
		buf.WriteByte('\n')
	}
	return atomicWriteFile(w.path, buf.Bytes())
}

// Close implements ResultWAL. fileWAL holds no file handle open between
// calls (see this type's own doc comment for why), so there is nothing to
// release.
func (w *fileWAL) Close() error {
	return nil
}

// atomicWriteFile writes data to path by first writing a temp file in the
// same directory, then os.Rename-ing it over path. Rename is atomic on
// POSIX filesystems within one directory, so a reader (or a crash) never
// observes a partially written file: it sees either the complete old
// content or the complete new content, never a torn write. Mirrors
// internal/credential/file_store_save.go's own
// atomicWriteCredentialsFile; not reused directly across the package
// boundary since that helper is unexported there too, for the identical
// reason.
func atomicWriteFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("failed to create temp file for %s: %w", path, err)
	}
	tmpPath := tmp.Name()

	renamed := false
	defer func() {
		if !renamed {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("failed to write temp file for %s: %w", path, err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("failed to set permissions on temp file for %s: %w", path, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("failed to fsync temp file for %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("failed to close temp file for %s: %w", path, err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("failed to atomically replace %s: %w", path, err)
	}
	renamed = true
	return nil
}
