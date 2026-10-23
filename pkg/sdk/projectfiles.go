// The files of the project a run belongs to, which a method may read, and
// write, where that project is on the machine running it.
package sdk

import (
	"errors"
	"io/fs"
)

// ErrNoProjectFiles is what ProjectFiles and ProjectFileWriter answer where
// the run has no project on this machine: a Walk-tier Runner, which runs in
// a per-task container that cannot see the project the Controller holds.
var ErrNoProjectFiles = errors.New("this run has no project directory on the machine running the method: only the command line (pleiades run and adhoc) reads a project's files")

// MaxProjectFileWrite bounds one file a method writes into the project: a
// whole device configuration is tens of kilobytes, and 16 MiB is far past
// any text a method has a reason to keep there.
const MaxProjectFileWrite = 16 << 20

// ProjectFiles is implemented by a RunbookContext that can read files from
// the project directory the run belongs to: on the command line, the
// directory the runbook and its inventory live in. A method asks for a
// path inside it and gets those bytes and nothing else: the path is
// relative, stays inside the directory without passing through a symbolic
// link, never reaches the project's .pleiades directory (its credentials
// and master key), and names a regular file of at most max bytes.
type ProjectFiles interface {
	// ReadProjectFile returns the file name names, a slash-separated path
	// relative to the project directory, refusing everything the interface
	// does, and ErrNoProjectFiles where the run has no project here.
	ReadProjectFile(name string, max int64) ([]byte, error)
}

// ProjectFileWriter is implemented by a RunbookContext that can write a
// file into the project directory the run belongs to, under exactly the
// rules ProjectFiles reads by: the path is relative, stays inside the
// directory without passing through a symbolic link, never reaches the
// project's .pleiades directory, and names a regular file (or none yet).
// The data is at most MaxProjectFileWrite bytes.
//
// The write is atomic: the new content goes to a temporary file in the
// same directory, which is then renamed over the old one, so a reader sees
// the old file or the new one and never half of either. A missing parent
// directory is created, inside the project only.
type ProjectFileWriter interface {
	// WriteProjectFile makes the file name names hold data with mode perm
	// (permission bits only), and reports whether that changed anything:
	// false when the file already held exactly those bytes with exactly
	// that mode. With write false it changes nothing and reports whether a
	// write would, after the same refusals, so a check predicts from the
	// same read a run makes. It answers ErrNoProjectFiles where the run has
	// no project here.
	WriteProjectFile(name string, data []byte, perm fs.FileMode, write bool) (changed bool, err error)
}
