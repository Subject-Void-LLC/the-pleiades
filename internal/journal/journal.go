// Package journal writes the run journal the execution engine produces.
//
// internal/engine defines what a journal entry is and declares the sink
// as a port (engine.Journal). This package is the Crawl tier's adapter
// for that port: an append-only JSON Lines file per run, under the
// project's own .pleiades directory, written 0o600 inside a 0o700
// directory.
//
// # Why there is nothing to encrypt here
//
// A journal entry holds no value that came back from a device, a
// credential store, a decrypted envelope, or an injector. It holds
// identifiers the platform generated, names it resolved through the
// collection registry, closed enums it read off its own control flow, a
// content digest, labels a runbook author wrote, and counts. That is the
// whole reason this file needs no key, no EnvelopeService and no
// crypto.Service, which matters most on exactly this tier: every
// value-carrying design has to answer what protects a Crawl journal at
// rest, and its honest options are nothing, or a new AES sidecar that
// invents a secret-at-rest surface for an audit feature.
//
// Encryption is in fact available here (internal/crypto is already
// linked into cmd/pleiades through internal/credential). It is simply
// not needed, which is a different and stronger statement.
//
// The file permissions are still tight. The entry names devices, task
// names, runbook ids and FQCNs, which is an operational picture worth
// keeping to the person whose project directory it is even though none
// of it is a secret.
package journal

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

const (
	// pleiadesDirName is the per-project directory the Crawl tier keeps
	// its own state in. internal/credential already owns two files there
	// (master.key and credentials.yaml) but declares the name
	// unexported, so this package declares its own rather than exporting
	// someone else's constant to reach it.
	pleiadesDirName = ".pleiades"

	// journalDirName is the subdirectory holding one file per run.
	journalDirName = "journal"

	// fileExtension marks the format. One JSON object per line, no
	// enclosing array, so a run that is killed partway through leaves
	// every completed level readable instead of one truncated document.
	fileExtension = ".jsonl"

	// dirMode is owner-only on the directory, and fileMode is owner-only
	// on the file.
	dirMode  = 0o700
	fileMode = 0o600
)

// FileStore is an engine.Journal that appends entries to one JSON Lines
// file per run.
//
// It satisfies the port's concurrency obligation with a mutex. A single
// Executor value is documented as safe to reuse and even to call Run on
// concurrently, and the sink is held on the Executor rather than per
// run, so two Record calls can be in flight at once carrying different
// RunIDs. O_APPEND alone is not enough for that: it makes each write
// land at the end of the file, but a JSON line here is several hundred
// bytes and nothing promises one write syscall carries all of it.
type FileStore struct {
	// dir is the resolved <root>/.pleiades/journal path, fixed at
	// construction. No part of it is ever derived from an entry.
	dir string

	// mu serializes Record calls. It is held across the whole call
	// rather than per file, because the cost is one short append and the
	// simpler rule is the one a reader can check.
	mu sync.Mutex
}

// NewFileStore creates the journal directory under root and returns a
// store that writes into it.
//
// root is the project directory, the same one `pleiades run --dir`
// names and the same one internal/credential resolves its own files
// against.
//
// It fails closed. A journal the operator cannot write is a journal that
// silently records nothing, and the moment to learn that is at startup
// rather than at the first level barrier of a run that is already
// changing devices.
func NewFileStore(root string) (*FileStore, error) {
	dir := filepath.Join(root, pleiadesDirName, journalDirName)
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return nil, fmt.Errorf("failed to create %s: %w", dir, err)
	}

	// os.MkdirAll applies its mode only to directories it actually
	// creates. A .pleiades left behind by an earlier run, by another
	// tool, or by a permissive umask keeps whatever mode it already had,
	// so both levels are set explicitly. This is the identical discipline
	// internal/credential's own save path follows, for the identical
	// reason, found by Phase W6's Schema/Injection Hardening audit
	// (FAILURE_PATTERNS.md #22).
	//
	// Note what this does in the other direction, because it is easy to
	// read as tightening only: it also RESTORES the owner write bit on a
	// directory somebody had made read-only. That is deliberate. These
	// two directories belong to this tool, 0o700 is the one mode they are
	// supposed to have, and a store that quietly accepted 0o500 would be
	// a store that cannot write its own journal. An operator who wants no
	// journal file should not get one by chmod.
	for _, path := range []string{filepath.Join(root, pleiadesDirName), dir} {
		if err := os.Chmod(path, dirMode); err != nil { // #nosec G302 -- 0o700 is a directory permission (owner rwx), not a file permission; gosec's 0600 threshold does not distinguish the two
			return nil, fmt.Errorf("failed to set permissions on %s: %w", path, err)
		}
	}

	// MkdirAll succeeding proves the directory exists, not that this
	// process can write into it: a read-only mount and a directory owned
	// by someone else both pass it. A real write is the only thing that
	// answers the question this constructor is being asked.
	probe := filepath.Join(dir, ".write-probe")
	if err := os.WriteFile(probe, nil, fileMode); err != nil {
		return nil, fmt.Errorf("failed to write into %s: %w", dir, err)
	}
	if err := os.Remove(probe); err != nil {
		return nil, fmt.Errorf("failed to clean up the write probe in %s: %w", dir, err)
	}

	return &FileStore{dir: dir}, nil
}

// Dir reports where this store writes, so a caller can tell an operator
// where to look.
func (s *FileStore) Dir() string {
	return s.dir
}
