// Package journal: the project's record of the runs changing its devices
// right now, so a run can notice another one working on the same devices.
//
// It notices; it never stops anything. Two runs changing one network at
// once is sometimes the point (a failure injected while a change is being
// verified), so nothing here refuses a run or waits for one. A run that
// changes devices writes one small file naming its devices while it works
// (BeginActive) and removes it when it ends; another run reads those files
// to warn at its start, and reads the journals to warn at its end about a
// change another run made to its devices meanwhile (ChangesDuring).
//
// A record is held, not merely written: its run keeps an exclusive lock on
// a lock file beside it (the run lock's own lockFile) for as long as it
// works, and the lock goes with the process however it ends. A reader that
// can take the lock has found a record whose run is gone, which it ignores
// and, once it is old enough not to be one still being written, removes.
// A process id would say less: one is reused, and means nothing on another
// machine sharing the project directory. The lock is on a file of its own
// because a Windows lock is mandatory: it would keep a reader from the
// very bytes it locks.
package journal

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
)

const (
	// activeDirName is where the records live, under .pleiades/runs.
	activeDirName = "active"
	// runsDirName holds activeDirName.
	runsDirName = "runs"
	// activeExtension marks a record, and lockExtension the lock file its
	// run holds beside it.
	activeExtension = ".json"
	lockExtension   = ".lock"
	// maxActiveBytes bounds one record: a run's devices by id and name, a
	// few hundred bytes each.
	maxActiveBytes = 1 << 20
	// staleAfter is how old an unheld record must be before it is
	// removed: BeginActive locks the lock file first and then writes the
	// record, so a record this young may belong to a run that died in
	// that instant or is still writing it, rather than one long dead.
	staleAfter = time.Minute
)

// ActiveDevice is one device an active run works on.
type ActiveDevice struct {
	// ID is the device's inventory id, which a journal entry names.
	ID string `json:"id"`
	// Name is its inventory name, for a person reading a warning.
	Name string `json:"name"`
}

// ActiveRun is one run's record while it changes devices.
type ActiveRun struct {
	// ID names the record, minted by BeginActive. It is not the run id,
	// which the engine mints once the run starts.
	ID string `json:"id"`
	// PID is the process running it, and Host the machine.
	PID  int    `json:"pid"`
	Host string `json:"host"`
	// Runbook names what it runs: a runbook's path, or an ad-hoc call.
	Runbook string `json:"runbook"`
	// StartedAt is when it began, in UTC.
	StartedAt time.Time `json:"started_at"`
	// Devices are the devices its plan targets.
	Devices []ActiveDevice `json:"devices"`
}

// Active is a run's own record, held until End.
type Active struct {
	// path is the record, and lockPath the file whose held lock says the
	// run is alive, held open as lock.
	path, lockPath string
	lock           *os.File
	// ID is the record's id, which OtherActive skips.
	ID string
}

// activeDir is the record directory of the project at root.
func activeDir(root string) string {
	return filepath.Join(root, pleiadesDirName, runsDirName, activeDirName)
}

// BeginActive records run as active in the project at root and holds the
// record until End. run's ID, PID, Host and StartedAt are filled in here.
// The lock file is created and locked before the record is written, so a
// record never stands unheld while its run lives.
func BeginActive(root string, run ActiveRun) (*Active, error) {
	dir := activeDir(root)
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return nil, fmt.Errorf("failed to create %s: %w", dir, err)
	}
	for _, path := range []string{filepath.Join(root, pleiadesDirName, runsDirName), dir} {
		if err := os.Chmod(path, dirMode); err != nil { // #nosec G302 -- 0o700 is a directory permission (owner rwx), as NewFileStore sets
			return nil, fmt.Errorf("failed to set permissions on %s: %w", path, err)
		}
	}
	// crypto/rand's Read never fails (it ends the program first), and a
	// record of strings, numbers and a time always encodes.
	var id [16]byte
	_, _ = rand.Read(id[:])
	run.ID = hex.EncodeToString(id[:])
	run.PID = os.Getpid()
	run.Host, _ = os.Hostname()
	run.StartedAt = time.Now().UTC()
	body, _ := json.Marshal(run)
	a := &Active{path: filepath.Join(dir, run.ID+activeExtension), lockPath: filepath.Join(dir, run.ID+lockExtension), ID: run.ID}
	// #nosec G304 -- the project directory joined with this package's
	// fixed names and an id minted here.
	var err error
	a.lock, err = os.OpenFile(a.lockPath, os.O_CREATE|os.O_EXCL|os.O_RDWR, fileMode)
	if err != nil {
		return nil, fmt.Errorf("failed to record this run as active: %w", err)
	}
	// Locked, then written; on any failure End undoes whatever was done
	// (an unlock of a file never locked, or a removal of a record never
	// written, is no error).
	if err = lockFile(a.lock, true); err == nil {
		err = writeRecord(a.path, body)
	}
	if err != nil {
		_ = a.End()
		return nil, fmt.Errorf("failed to record this run as active: %w", err)
	}
	return a, nil
}

// writeRecord writes a new record file at path, owner-only.
func writeRecord(path string, body []byte) error {
	// #nosec G304 -- see BeginActive.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, fileMode)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(body)
	return errors.Join(writeErr, f.Close())
}

// End releases the run's lock and removes its record and lock file. It is
// safe to call on nil, and a record already gone is not an error.
func (a *Active) End() error {
	if a == nil {
		return nil
	}
	removeErr := os.Remove(a.path)
	if errors.Is(removeErr, fs.ErrNotExist) {
		removeErr = nil
	}
	unlockErr := unlockFile(a.lock)
	closeErr := a.lock.Close()
	lockRemoveErr := os.Remove(a.lockPath)
	if errors.Is(lockRemoveErr, fs.ErrNotExist) {
		lockRemoveErr = nil
	}
	return errors.Join(removeErr, unlockErr, closeErr, lockRemoveErr)
}

// OtherActive returns every live run in the project at root other than
// the record named self whose devices include one of deviceIDs, oldest
// first. A record whose run is gone is skipped, and removed once it is
// older than staleAfter; one that cannot be read safely (a link, a file
// others may write, one too large, one not yet written) is skipped.
func OtherActive(root, self string, deviceIDs []string) ([]ActiveRun, error) {
	dir := activeDir(root)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to list %s: %w", dir, err)
	}
	var live []ActiveRun
	for _, e := range entries {
		if id, ok := strings.CutSuffix(e.Name(), lockExtension); ok && id != self {
			removeOrphanLock(dir, id)
			continue
		}
		id, ok := strings.CutSuffix(e.Name(), activeExtension)
		if !ok || id == self || !e.Type().IsRegular() {
			continue
		}
		run, ok := readActive(dir, id)
		if ok && sharesDevice(run, deviceIDs) {
			live = append(live, run)
		}
	}
	sort.SliceStable(live, func(i, j int) bool { return live[i].StartedAt.Before(live[j].StartedAt) })
	return live, nil
}

// readActive reads the record id in dir, reporting false for one whose
// run is gone (removing it and its lock file when old enough) or that
// cannot be read safely.
func readActive(dir, id string) (ActiveRun, bool) {
	path := filepath.Join(dir, id+activeExtension)
	info, err := os.Lstat(path)
	// othersMayWrite is the Unix group and world write bits, and nothing on
	// a system whose modes are not Unix ones (reader_other.go), where every
	// writable file reads back as 0666 and the directory's ACL decides.
	if err != nil || !info.Mode().IsRegular() || othersMayWrite(info) || info.Size() > maxActiveBytes {
		return ActiveRun{}, false
	}
	if activeLocks && !heldByLiveRun(filepath.Join(dir, id+lockExtension)) {
		if time.Since(info.ModTime()) > staleAfter {
			_ = os.Remove(path)
			_ = os.Remove(filepath.Join(dir, id+lockExtension))
		}
		return ActiveRun{}, false
	}
	// #nosec G304 -- a name listed in this package's own directory, checked
	// above to be a regular file.
	body, err := os.ReadFile(path)
	if err != nil || len(body) > maxActiveBytes {
		return ActiveRun{}, false
	}
	var run ActiveRun
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&run); err != nil || run.ID != id {
		return ActiveRun{}, false
	}
	return run, true
}

// removeOrphanLock removes the lock file id in dir when no record stands
// beside it, nobody holds it, and it is older than staleAfter: what a run
// that died between locking and writing its record leaves.
func removeOrphanLock(dir, id string) {
	lockPath := filepath.Join(dir, id+lockExtension)
	if _, err := os.Lstat(filepath.Join(dir, id+activeExtension)); !errors.Is(err, fs.ErrNotExist) {
		return
	}
	info, err := os.Lstat(lockPath)
	if err != nil || time.Since(info.ModTime()) <= staleAfter || !activeLocks || heldByLiveRun(lockPath) {
		return
	}
	_ = os.Remove(lockPath)
}

// heldByLiveRun reports whether the lock file at path is locked by
// another process, which is the run that wrote the record beside it. A
// lock file that is missing, a link, or lockable is no live run's.
func heldByLiveRun(path string) bool {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	// #nosec G304 -- see readActive.
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	err = lockFile(f, true)
	if err == nil {
		_ = unlockFile(f)
		return false
	}
	return errors.Is(err, errLockHeld)
}

// sharesDevice reports whether run works on one of deviceIDs.
func sharesDevice(run ActiveRun, deviceIDs []string) bool {
	for _, d := range run.Devices {
		if slices.Contains(deviceIDs, d.ID) {
			return true
		}
	}
	return false
}

// ConcurrentChange is another run's change to some of a run's devices,
// journaled while that run worked.
type ConcurrentChange struct {
	// RunID names the other run, as pleiades journal show takes it.
	RunID string
	// Runbook is its runbook's id.
	Runbook string
	// DeviceIDs are the shared devices it changed, sorted.
	DeviceIDs []string
	// Changes counts its changed tasks on them.
	Changes int
	// RollbackOf is set when it was a rollback, naming the run it undid.
	RollbackOf string
}

// ChangesDuring reads every journal in the project at root other than
// self's and returns each run that journaled a change to one of deviceIDs
// finishing between since and until, oldest first. A journal that cannot
// be read (one still being written can end mid-line) is read once more,
// and if it still cannot be, named in unread rather than failing the rest.
func ChangesDuring(root, self string, deviceIDs []string, since, until time.Time) (changes []ConcurrentChange, unread []string, err error) {
	dir := filepath.Join(root, pleiadesDirName, journalDirName)
	names, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("failed to list %s: %w", dir, err)
	}
	type found struct {
		change ConcurrentChange
		first  time.Time
	}
	var all []found
	for _, e := range names {
		runID, ok := strings.CutSuffix(e.Name(), fileExtension)
		if !ok || runID == self {
			continue
		}
		run, err := ReadRun(root, runID)
		if err != nil {
			time.Sleep(200 * time.Millisecond)
			if run, err = ReadRun(root, runID); err != nil {
				unread = append(unread, runID)
				continue
			}
		}
		var f found
		devices := map[string]bool{}
		for _, entry := range run.Entries {
			if !(entry.ActionChanged || entry.Outcome == engine.OutcomeChanged) || !slices.Contains(deviceIDs, entry.DeviceID) {
				continue
			}
			if entry.FinishedAt.Before(since) || entry.FinishedAt.After(until) {
				continue
			}
			if f.change.Changes == 0 || entry.FinishedAt.Before(f.first) {
				f.first = entry.FinishedAt
			}
			f.change.Changes++
			f.change.Runbook, f.change.RollbackOf = entry.DAGID, entry.RollbackOf
			devices[entry.DeviceID] = true
		}
		if f.change.Changes == 0 {
			continue
		}
		f.change.RunID = runID
		for d := range devices {
			f.change.DeviceIDs = append(f.change.DeviceIDs, d)
		}
		sort.Strings(f.change.DeviceIDs)
		all = append(all, f)
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].first.Before(all[j].first) })
	for _, f := range all {
		changes = append(changes, f.change)
	}
	return changes, unread, nil
}
