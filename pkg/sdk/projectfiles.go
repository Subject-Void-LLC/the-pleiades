// The files of the project a run belongs to, which a method may read
// where that project is on the machine running it.
package sdk

import "errors"

// ErrNoProjectFiles is what ProjectFiles answers where the run has no
// project on this machine: a Walk-tier Runner, which runs in a per-task
// container that cannot see the project the Controller holds.
var ErrNoProjectFiles = errors.New("this run has no project directory on the machine running the method: only the command line (pleiades run and adhoc) reads a project's files")

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
