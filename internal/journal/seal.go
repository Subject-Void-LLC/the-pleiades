// Package journal: the seal, a small file that says a run's journal is
// complete.
//
// A run's entries are written one level at a time, after the level's
// nodes have all returned. A journal that simply stops therefore reads
// the same whether the run ended there or the process died there, and the
// two mean opposite things to a rollback: an ended run changed nothing
// past its last record, while a killed one may have changed things in a
// level that never got written down. The seal is what tells them apart.
// It is written once, after Executor.Run returns, so it exists exactly
// when every level the run started was also recorded.
package journal

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// sealExtension names a run's seal beside its <run-id>.jsonl.
const sealExtension = ".end"

// Seal is what a run's seal file holds. Nothing in it is needed to trust
// the journal (its existence is the whole signal); the fields make a file
// an operator finds by hand say what it is.
type Seal struct {
	// RunID is the run this seal closes.
	RunID string `json:"run_id"`

	// SealedAt is when the run returned, in UTC.
	SealedAt time.Time `json:"sealed_at"`
}

// PathFor returns where runID's journal file is, or would be, written:
// the path an operator is told to look at after a run.
func (s *FileStore) PathFor(runID string) (string, error) {
	name, err := fileNameFor(runID)
	if err != nil {
		return "", err
	}
	return filepath.Join(s.dir, name), nil
}

// sealPathFor returns where runID's seal file is written.
func sealPathFor(dir, runID string) (string, error) {
	// fileNameFor is the one validation of a run id; its extension is
	// swapped rather than a second rule written for the seal.
	name, err := fileNameFor(runID)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name[:len(name)-len(fileExtension)]+sealExtension), nil
}

// Seal records that runID's journal is complete, at the moment at.
//
// A run is sealed once. A second seal for the same run is refused rather
// than overwritten, because two runs sharing an id would be two runs'
// entries in one file, and the seal is the last place that could notice.
func (s *FileStore) Seal(runID string, at time.Time) (err error) {
	path, err := sealPathFor(s.dir, runID)
	if err != nil {
		return err
	}
	body, err := json.Marshal(Seal{RunID: runID, SealedAt: at.UTC()})
	if err != nil {
		return fmt.Errorf("failed to encode the seal for run %s: %w", runID, err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// #nosec G304 -- path is s.dir, fixed at construction, joined with a
	// name fileNameFor restricted to [A-Za-z0-9-] and the fixed extension.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, fileMode)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("run %s is already sealed; a run id names exactly one run", runID)
		}
		return fmt.Errorf("failed to create the seal for run %s: %w", runID, err)
	}
	defer func() {
		closeErr := f.Close()
		if closeErr != nil && err == nil {
			err = fmt.Errorf("failed to close the seal for run %s: %w", runID, closeErr)
		}
	}()
	if _, err := f.Write(append(body, '\n')); err != nil {
		return fmt.Errorf("failed to write the seal for run %s: %w", runID, err)
	}
	// Synced for the same reason every journal append is: a seal still in
	// the page cache when the machine loses power would leave a complete
	// run reading as one that was cut off, which is the safe direction to
	// be wrong in, but still wrong.
	if err := f.Sync(); err != nil {
		return fmt.Errorf("failed to sync the seal for run %s: %w", runID, err)
	}
	return nil
}
