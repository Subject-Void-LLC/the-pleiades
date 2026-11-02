// Package wire: predicted state across a check (Phase 46a). A check runs each
// task against the device as it really is, so a task whose outcome depends on
// what an earlier task in the same check only predicted would be answered
// wrongly: a directory a check of its undo would find empty only once the
// file inside was removed, which the check only predicted. The engine keeps,
// per device, the paths earlier tasks predicted removed, and hands a method
// this snapshot of them; on the Walk tier it rides ChildRequest to the
// per-task and session child.
//
// Only removals are recorded. A claim that a path is gone can be made false
// only by a task that makes something, and the engine keeps predictions only
// while no task in the run could have (internal/engine's predicted.go), so a
// claim is never vouched past one. A second spelling of a path (a bind mount,
// a name a case-insensitive filesystem folds) can then only miss a record,
// which reads the device as it is, never vouch for a path wrongly.
//
// A path is keyed as the device resolves it: its physical path, together
// with the identity (device and inode numbers, "dev:ino") of each directory
// along it that exists.
package wire

import (
	"path"
	"strings"
	"sync"
)

// PredictedAbsent is the one state a path is predicted in: gone, removed by
// an earlier task's check.
const PredictedAbsent = "absent"

// Bounds on a snapshot, so a check that predicts a great many paths never
// grows a request without limit: past them the engine drops the device's
// predictions rather than send part of them.
const (
	// MaxPredictedPaths bounds how many paths, and how many directory
	// identities, one device's snapshot holds.
	MaxPredictedPaths = 4096
	// MaxPredictedPathBytes bounds one path's length.
	MaxPredictedPathBytes = 4096
	// maxDirID bounds one identity's length: two decimal 64-bit numbers.
	maxDirID = 41
)

// DirID is one directory's identity on the device: its physical path and the
// "dev:ino" the device reports for it.
type DirID struct {
	// Path is the directory's physical path.
	Path string `json:"path"`
	// ID is "<device>:<inode>", both decimal.
	ID string `json:"id"`
}

// Location is a path as the device resolves it: Path is physical (absolute,
// cleaned, every link above it resolved), and Dirs are the identities of the
// directories along it that exist, the path itself among them when it is a
// directory and not a link. pkg/remotefile's Locate reads one.
type Location struct {
	// Path is the physical path.
	Path string `json:"path"`
	// Dirs are the identities along Path.
	Dirs []DirID `json:"dirs,omitempty"`
}

// PredictedPath is one path an earlier task's check predicted, and how.
type PredictedPath struct {
	// Path is the physical path (Location.Path).
	Path string `json:"path"`
	// State is PredictedAbsent.
	State string `json:"state"`
	// Dirs are the identities along Path as the recording task read them.
	Dirs []DirID `json:"dirs,omitempty"`
}

// Predictions is one device's snapshot of what earlier tasks in a check
// predicted. A nil snapshot answers nothing: the method reads the device as it
// is, as before predictions existed. A non-nil one, even empty, says the run
// is a check keeping predictions, so a method should record its own.
type Predictions struct {
	// Paths are the predicted paths, each at most once. Their Dirs are left
	// empty here: every identity is in Dirs.
	Paths []PredictedPath `json:"paths,omitempty"`
	// Dirs are every directory identity the device's tasks have read, each
	// path and each identity at most once.
	Dirs []DirID `json:"dirs,omitempty"`

	once   sync.Once
	states map[string]string
	ids    map[string]string
	dirs   map[string]string
}

// ValidPredictedPath reports whether p is a path a method's prediction may
// name: a physical path within the length bound with no NUL, absent, whose
// identities are valid.
func ValidPredictedPath(p PredictedPath) bool {
	if p.State != PredictedAbsent {
		return false
	}
	return ValidLocation(Location{Path: p.Path, Dirs: p.Dirs})
}

// ValidLocation reports whether l is a location a method may ask or record
// with: an absolute, cleaned path within the length bound with no NUL, and
// identities each naming such a path at or above it and a "dev:ino".
func ValidLocation(l Location) bool {
	if !validPath(l.Path) {
		return false
	}
	for _, d := range l.Dirs {
		if !validPath(d.Path) || !validDirID(d.ID) || !within(l.Path, d.Path) {
			return false
		}
	}
	return true
}

// validPath reports whether p is absolute, cleaned, bounded and free of NUL.
func validPath(p string) bool {
	return len(p) > 0 && len(p) <= MaxPredictedPathBytes && !strings.ContainsRune(p, 0) &&
		path.IsAbs(p) && path.Clean(p) == p
}

// validDirID reports whether id is two decimal numbers joined by a colon.
func validDirID(id string) bool {
	dev, ino, ok := strings.Cut(id, ":")
	return ok && len(id) <= maxDirID && digits(dev) && digits(ino)
}

// digits reports whether s is one or more ASCII digits.
func digits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// within reports whether dir is p or a directory above it.
func within(p, dir string) bool {
	return p == dir || dir == "/" || strings.HasPrefix(p, dir+"/")
}

// index builds the snapshot's lookups once, for however many questions a
// method asks of it.
func (p *Predictions) index() {
	p.once.Do(func() {
		p.states = make(map[string]string, len(p.Paths))
		for _, pp := range p.Paths {
			p.states[pp.Path] = pp.State
		}
		p.ids = make(map[string]string, len(p.Dirs))
		p.dirs = make(map[string]string, len(p.Dirs))
		for _, d := range p.Dirs {
			p.ids[d.ID] = d.Path
			p.dirs[d.Path] = d.ID
		}
	})
}

// agrees reports whether every identity in l names the same directory the
// snapshot knows it as, and every directory the snapshot knows has the same
// identity in l. A disagreement is a second spelling of one directory (a bind
// mount, a folded name) or a directory replaced since, and either way the
// snapshot cannot vouch for l. A nil snapshot or an invalid location never
// agrees.
func (p *Predictions) agrees(l Location) bool {
	if p == nil || !ValidLocation(l) {
		return false
	}
	p.index()
	for _, d := range l.Dirs {
		if at, ok := p.ids[d.ID]; ok && at != d.Path {
			return false
		}
		if id, ok := p.dirs[d.Path]; ok && id != d.ID {
			return false
		}
	}
	return true
}

// Absent reports whether the snapshot vouches that l will be gone by the time
// the asking task runs: recorded absent itself, or under a directory recorded
// absent. Anything else (a nil snapshot, a location whose identities disagree
// with it, no record) answers false, so the method reads the device as it is.
func (p *Predictions) Absent(l Location) bool {
	if !p.agrees(l) {
		return false
	}
	_, ok := p.nearest(l.Path)
	return ok
}

// Vouches reports whether the snapshot can answer for l at all: it is not nil
// and every identity in l names the directory the snapshot knows it as.
func (p *Predictions) Vouches(l Location) bool {
	return p.agrees(l)
}

// nearest returns the state of the record nearest target on the way up: the
// path itself, then each directory above it. p is indexed.
func (p *Predictions) nearest(target string) (string, bool) {
	for at := target; ; at = path.Dir(at) {
		if state, ok := p.states[at]; ok {
			return state, true
		}
		if at == "/" {
			return "", false
		}
	}
}

// AbsentUnder reports whether the snapshot records any path strictly inside
// the directory l as absent: without one, no entry of l can be vouched gone,
// so a caller asking that of each entry need not list them. A nil snapshot or
// a location whose identities disagree with it answers false.
func (p *Predictions) AbsentUnder(l Location) bool {
	if !p.agrees(l) {
		return false
	}
	prefix := strings.TrimSuffix(l.Path, "/") + "/"
	for _, pp := range p.Paths {
		if strings.HasPrefix(pp.Path, prefix) {
			return true
		}
	}
	return false
}
