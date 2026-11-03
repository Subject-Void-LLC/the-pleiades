// Package facts: the Crawl CLI's fact store, a directory of files under the
// project's .pleiades/facts, one set per device.
//
// <device>.jsonl is append-only: one line per stored stable value, so a
// key's lines are its history. <device>.state.json holds what changes on
// every observation (file_store.go). A key's current value is the one most
// recently confirmed.
//
// Every file is opened through an os.Root on the directory, so a link planted
// there cannot lead a write outside it; the directory is refused when it is
// itself a link, and made owner-only. Runs of the CLI share the project's run
// lock rather than exclude each other, so each device's files are written
// under a lock of their own (<device>.lock, flock or LockFileEx), and a line a
// crash left unfinished is ignored by readers and cut off by the next writer.
package facts

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"regexp"
	"sync/atomic"
	"time"
)

// File permissions: owner-only, as the journal's are.
const (
	dirMode  = 0o700
	fileMode = 0o600
)

// maxLineBytes bounds one line read back: a value's bound plus room for the
// key, hash and times.
const maxLineBytes = MaxValueBytes + 4<<10

// deviceName is what a device's files are named after: its ID, a UUID or a
// name made of the characters inventory allows.
var deviceName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// ErrNoStore marks a read of a project whose fact directory does not exist:
// no run has stored a fact there yet.
var ErrNoStore = errors.New("facts: no fact store in this project yet")

// FileStore is the Crawl CLI's Store over a directory.
type FileStore struct {
	dir string
}

// NewFileStore returns a store over dir, made owner-only when missing or
// looser, and refused when it is a symbolic link.
func NewFileStore(dir string) (*FileStore, error) {
	if info, err := os.Lstat(dir); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("facts: %s is a symbolic link, not a directory", dir)
	}
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return nil, fmt.Errorf("facts: %w", err)
	}
	if err := os.Chmod(dir, dirMode); err != nil { // #nosec G302 -- 0o700 is a directory permission (owner rwx), as the journal's
		return nil, fmt.Errorf("facts: %w", err)
	}
	return &FileStore{dir: dir}, nil
}

// OpenFileStore returns a store over dir for reading, creating nothing: a
// missing directory is ErrNoStore, and a link is refused.
func OpenFileStore(dir string) (*FileStore, error) {
	info, err := os.Lstat(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, ErrNoStore
	case err != nil:
		return nil, fmt.Errorf("facts: %w", err)
	case !info.IsDir():
		return nil, fmt.Errorf("facts: %s is not a directory", dir)
	}
	return &FileStore{dir: dir}, nil
}

// lockOpenAttempts bounds how often openLock tries again; lockOpenRetries
// counts the tries again, so a test can see whether a platform needed one.
const lockOpenAttempts = 20

var lockOpenRetries atomic.Int64

// openLock opens (creating it when missing) a device's lock file. On macOS,
// openat(2) with O_CREAT relative to a directory fd, which an os.Root opens
// through, returns "no such file or directory" now and then while another
// store creates the same file at the same moment, in a directory that still
// exists (reproduced on macOS 27, FAILURE_PATTERNS 842; an absolute open
// does not, but the os.Root is what keeps a planted link from leading a
// write outside the store). Such a failure is tried again, a bounded number
// of times and only while the directory itself is still there, so a
// directory that really is gone still fails at once.
func openLock(r *os.Root, name string) (*os.File, error) {
	for attempt := 1; ; attempt++ {
		f, err := r.OpenFile(name, os.O_CREATE|os.O_RDWR, fileMode)
		if err == nil || !errors.Is(err, fs.ErrNotExist) || attempt == lockOpenAttempts {
			return f, err
		}
		if !stillThere(r) {
			return nil, err
		}
		lockOpenRetries.Add(1)
		time.Sleep(time.Duration(attempt) * time.Millisecond)
	}
}

// stillThere reports whether r's directory is still at its path: a handle to
// a removed directory still answers a stat of ".", so the path is compared
// with the handle.
func stillThere(r *os.Root) bool {
	held, err := r.Stat(".")
	if err != nil {
		return false
	}
	named, err := os.Stat(r.Name())
	return err == nil && os.SameFile(held, named)
}

// root opens the directory as an os.Root, and checks device's name.
func (s *FileStore) root(device string) (*os.Root, error) {
	if !deviceName.MatchString(device) {
		return nil, fmt.Errorf("%w: %q is not a device a fact file may be named after", ErrInvalid, device)
	}
	r, err := os.OpenRoot(s.dir)
	if err != nil {
		return nil, fmt.Errorf("facts: %w", err)
	}
	return r, nil
}

// appendLines appends to device's history, first cutting off an unfinished
// line a crash left past complete bytes.
func appendLines(r *os.Root, device string, complete int64, appended []byte) error {
	f, err := r.OpenFile(device+".jsonl", os.O_CREATE|os.O_RDWR, fileMode)
	if err != nil {
		return fmt.Errorf("facts: %w", err)
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("facts: %w", err)
	}
	if info.Size() > complete {
		if err := f.Truncate(complete); err != nil {
			return fmt.Errorf("facts: cutting an unfinished line off %s.jsonl: %w", device, err)
		}
	}
	if _, err := f.WriteAt(appended, complete); err != nil {
		return fmt.Errorf("facts: %w", err)
	}
	return nil
}

// readLines reads device's history, and how many bytes of it are whole
// lines. A missing file is an empty history; a final line with no newline
// (a write a crash cut short) is left out; any other line that does not
// decode is an error, never skipped.
func readLines(r *os.Root, device string) ([]line, int64, error) {
	f, err := r.Open(device + ".jsonl")
	if errors.Is(err, fs.ErrNotExist) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, fmt.Errorf("facts: %w", err)
	}
	defer func() { _ = f.Close() }()
	br := bufio.NewReaderSize(f, 64<<10)
	var lines []line
	var complete int64
	for n := 1; ; n++ {
		raw, err := br.ReadBytes('\n')
		if errors.Is(err, io.EOF) {
			return lines, complete, nil
		}
		if err != nil {
			return nil, 0, fmt.Errorf("facts: %s.jsonl: %w", device, err)
		}
		if len(raw) > maxLineBytes {
			return nil, 0, fmt.Errorf("facts: %s.jsonl line %d is longer than a stored fact can be", device, n)
		}
		var l line
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		if err := dec.Decode(&l); err != nil || !ValidKey(l.Key) || l.Hash == "" {
			return nil, 0, fmt.Errorf("facts: %s.jsonl line %d is not a stored fact", device, n)
		}
		lines = append(lines, l)
		complete += int64(len(raw))
	}
}

// readState reads device's state; a missing file is an empty one.
func readState(r *os.Root, device string) (state, error) {
	st := state{Seen: map[string]map[string]sighting{}, Volatile: map[string]volatileValue{}}
	f, err := r.Open(device + ".state.json")
	if errors.Is(err, fs.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return state{}, fmt.Errorf("facts: %w", err)
	}
	defer func() { _ = f.Close() }()
	dec := json.NewDecoder(f)
	dec.UseNumber()
	if err := dec.Decode(&st); err != nil {
		return state{}, fmt.Errorf("facts: %s.state.json: %w", device, err)
	}
	if st.Seen == nil {
		st.Seen = map[string]map[string]sighting{}
	}
	if st.Volatile == nil {
		st.Volatile = map[string]volatileValue{}
	}
	return st, nil
}

// writeState rewrites device's state through a temporary file of its own
// name and a rename, so a reader never meets half of one.
func writeState(r *os.Root, device string, st state) error {
	raw, err := json.Marshal(st)
	if err != nil {
		return fmt.Errorf("facts: %w", err)
	}
	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		return fmt.Errorf("facts: %w", err)
	}
	tmp := device + ".state." + hex.EncodeToString(suffix) + ".tmp"
	f, err := r.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, fileMode)
	if err != nil {
		return fmt.Errorf("facts: %w", err)
	}
	if _, err := f.Write(raw); err != nil {
		_ = f.Close()
		_ = r.Remove(tmp)
		return fmt.Errorf("facts: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = r.Remove(tmp)
		return fmt.Errorf("facts: %w", err)
	}
	if err := r.Rename(tmp, device+".state.json"); err != nil {
		_ = r.Remove(tmp)
		return fmt.Errorf("facts: %w", err)
	}
	return nil
}
