// Package facts: the Crawl CLI's fact store, a directory of files under the
// project's .pleiades/facts, one set per device.
//
// <device>.jsonl is append-only: one line per stored stable value, so a
// key's lines are its history. <device>.seen.jsonl is append-only too: one
// line per observation (file_seen.go). <device>.state.json holds each
// volatile key's one value (file_store.go). A key's current value is the one
// most recently confirmed.
//
// Every file is opened through an os.Root on the directory, so a link planted
// there cannot lead a write outside it; the directory is refused when it is
// itself a link, and made owner-only. Runs of the CLI share the project's run
// lock rather than exclude each other, so each device's files are written
// under a lock of their own (<device>.lock, flock or LockFileEx), and a line a
// crash left unfinished is ignored by readers and cut off by the next writer.
//
// Nothing in those files is plaintext (atrest.go): each line of the two
// append-only files, and the state file whole, is sealed under a key derived
// from the project's master key, bound to the device, the file and the line's
// place in it. A store with no key writes nothing and reads nothing.
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

// maxLineBytes bounds one history line read back: a value's bound plus room
// for the key, hash and times, as a sealed line holds them. maxSeenLineBytes
// bounds one observation line, which may list every key an observation may
// hold, each escaped as JSON may write it.
var (
	maxLineBytes     = SealedLineBytes(MaxValueBytes + 4<<10)
	maxSeenLineBytes = SealedLineBytes(MaxFactsPerObservation*(6*MaxKeyBytes+3) + 4<<10)
)

// deviceName is what a device's files are named after: its ID, a UUID or a
// name made of the characters inventory allows.
var deviceName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// ErrNoStore marks a read of a project whose fact directory does not exist:
// no run has stored a fact there yet.
var ErrNoStore = errors.New("facts: no fact store in this project yet")

// FileStore is the Crawl CLI's Store over a directory.
type FileStore struct {
	dir string
	// keys seals every line and keys every hash (atrest.go). Nil stores and
	// reads nothing (ErrNoKeys).
	keys *Keys
}

// NewFileStore returns a store over dir, made owner-only when missing or
// looser, and refused when it is a symbolic link. keys seals what it keeps;
// with none it keeps nothing.
func NewFileStore(dir string, keys *Keys) (*FileStore, error) {
	if info, err := os.Lstat(dir); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("facts: %s is a symbolic link, not a directory", dir)
	}
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return nil, fmt.Errorf("facts: %w", err)
	}
	if err := os.Chmod(dir, dirMode); err != nil { // #nosec G302 -- 0o700 is a directory permission (owner rwx), as the journal's
		return nil, fmt.Errorf("facts: %w", err)
	}
	return &FileStore{dir: dir, keys: keys}, nil
}

// OpenFileStore returns a store over dir for reading, creating nothing: a
// missing directory is ErrNoStore, and a link is refused. keys opens what it
// reads; with none every read is ErrNoKeys.
func OpenFileStore(dir string, keys *Keys) (*FileStore, error) {
	info, err := os.Lstat(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, ErrNoStore
	case err != nil:
		return nil, fmt.Errorf("facts: %w", err)
	case !info.IsDir():
		return nil, fmt.Errorf("facts: %s is not a directory", dir)
	}
	return &FileStore{dir: dir, keys: keys}, nil
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

// appendLines appends to device's history (appendTo).
func appendLines(r *os.Root, device string, complete int64, appended []byte) error {
	return appendTo(r, device+".jsonl", complete, appended)
}

// appendTo appends to the append-only file name, first cutting off an
// unfinished line a crash left past complete bytes.
func appendTo(r *os.Root, name string, complete int64, appended []byte) error {
	f, err := r.OpenFile(name, os.O_CREATE|os.O_RDWR, fileMode)
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
			return fmt.Errorf("facts: cutting an unfinished line off %s: %w", name, err)
		}
	}
	if _, err := f.WriteAt(appended, complete); err != nil {
		return fmt.Errorf("facts: %w", err)
	}
	// On disk before anything that names these lines is written, so a crash
	// cannot leave a later file ahead of this one.
	if err := f.Sync(); err != nil {
		return fmt.Errorf("facts: %w", err)
	}
	return nil
}

// openStored opens one stored line (without its newline) of device's file of
// the given role at offset. A line that reads as the JSON a build from before
// facts were sealed wrote is named as that, since no key opens it.
func openStored(keys *Keys, device, role string, offset int64, raw []byte) ([]byte, error) {
	plaintext, err := keys.OpenLine(device, role, offset, bytes.TrimSuffix(raw, []byte("\n")))
	if errors.Is(err, ErrSealed) && bytes.HasPrefix(raw, []byte("{")) {
		return nil, errWrittenBeforeSealing
	}
	return plaintext, err
}

// unsealedError is ErrUnsealed in other words: a line the CLI's store reads
// that was written before facts were sealed. errors.Is finds ErrUnsealed,
// and through it ErrSealed, so a caller can tell it from a line that does not
// open for any other reason.
type unsealedError struct{ msg string }

// Error implements error.
func (e *unsealedError) Error() string { return e.msg }

// Unwrap is ErrUnsealed.
func (e *unsealedError) Unwrap() error { return ErrUnsealed }

// errWrittenBeforeSealing is openStored's refusal of a plaintext line,
// naming what to remove.
var errWrittenBeforeSealing error = &unsealedError{msg: ErrSealed.Error() + ": it was written before stored facts were sealed; remove the device's files under .pleiades/facts to start a sealed history"}

// readLines reads device's history, and how many bytes of it are whole
// lines (readJSONL). Each line must open under keys as this device's line
// at its place: one that does not is an error, never skipped.
func readLines(r *os.Root, keys *Keys, device string) ([]line, int64, error) {
	var lines []line
	complete, err := readJSONL(r, device+".jsonl", maxLineBytes, func(n int, offset int64, sealed []byte) error {
		raw, err := openStored(keys, device, RoleValues, offset, sealed)
		if err != nil {
			return fmt.Errorf("facts: %s.jsonl line %d: %w", device, n, err)
		}
		var l line
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		// The Collection identity is read as written, never held to
		// CheckCollection: the rule reads the first-party group list, which
		// a later release may grow, and a line its chain already covers must
		// keep reading and verifying under it. The chain finds an edited one.
		// The key likewise: wellFormedKey, which reads no unicode table,
		// never ValidKey, which grows and moves with the toolchain.
		if err := dec.Decode(&l); err != nil || !wellFormedKey(l.Key) || l.Hash == "" || l.Seq < 0 {
			return fmt.Errorf("facts: %s.jsonl line %d is not a stored fact", device, n)
		}
		lines = append(lines, l)
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	return lines, complete, nil
}

// readJSONL hands each whole line of the append-only file name to each, with
// the byte offset it starts at, and returns how many bytes are whole lines. A missing file has none; a final
// line with no newline (a write a crash cut short) is left out; a line longer
// than any stored line can be, or one each refuses, is an error, never
// skipped.
func readJSONL(r *os.Root, name string, maxBytes int, each func(n int, offset int64, raw []byte) error) (int64, error) {
	f, err := r.Open(name)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("facts: %w", err)
	}
	defer func() { _ = f.Close() }()
	br := bufio.NewReaderSize(f, 64<<10)
	var complete int64
	for n := 1; ; n++ {
		raw, err := br.ReadBytes('\n')
		if errors.Is(err, io.EOF) {
			return complete, nil
		}
		if err != nil {
			return 0, fmt.Errorf("facts: %s: %w", name, err)
		}
		if len(raw) > maxBytes {
			return 0, fmt.Errorf("facts: %s line %d is longer than a stored line can be", name, n)
		}
		if err := each(n, complete, raw); err != nil {
			return 0, err
		}
		complete += int64(len(raw))
	}
}

// readState reads device's state; a missing file is an empty one, and so is
// a file of nothing but NUL bytes, or none. writeState never writes either
// (an empty state encodes as an object), so such a file is what a crash left
// of a rename that reached the disk before the data did, empty on most
// filesystems and zero-filled on XFS, on a store written before writeState
// synced (FAILURE_PATTERNS 922). Refusing it refused the device's
// facts forever; what it held is the volatile values, which the next run
// observes again, while the history itself is in <device>.jsonl.
//
// What is there otherwise must open under keys as this device's state: a
// state copied from another device, or edited, is an error.
func readState(r *os.Root, keys *Keys, device string) (state, error) {
	st := state{Volatile: map[string]volatileValue{}}
	f, err := r.Open(device + ".state.json")
	if errors.Is(err, fs.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return state{}, fmt.Errorf("facts: %w", err)
	}
	defer func() { _ = f.Close() }()
	raw, err := io.ReadAll(f)
	if err != nil {
		return state{}, fmt.Errorf("facts: %s.state.json: %w", device, err)
	}
	if len(bytes.Trim(raw, "\x00")) == 0 {
		return st, nil
	}
	if raw, err = openStored(keys, device, RoleState, 0, raw); err != nil {
		return state{}, fmt.Errorf("facts: %s.state.json: %w", device, err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&st); err != nil {
		return state{}, fmt.Errorf("facts: %s.state.json: %w", device, err)
	}
	if st.Volatile == nil {
		st.Volatile = map[string]volatileValue{}
	}
	return st, nil
}

// writeState rewrites device's state through a temporary file of its own
// name and a rename, so a reader never meets half of one. The temporary file
// is synced before the rename: without it a crash could leave the rename on
// disk and the data not, an empty state file (FAILURE_PATTERNS 922, a repeat
// of 566). The state is sealed whole under keys, as this device's.
func writeState(r *os.Root, keys *Keys, device string, st state) error {
	plain, err := json.Marshal(st)
	if err != nil {
		return fmt.Errorf("facts: %w", err)
	}
	raw, err := keys.SealLine(device, RoleState, 0, plain)
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
	if err := f.Sync(); err != nil {
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
	// The rename itself, on disk. Best effort: the state is in place and
	// whole either way, and a crash before this reaches the disk leaves the
	// previous state, which is whole too.
	syncDir(r)
	return nil
}
