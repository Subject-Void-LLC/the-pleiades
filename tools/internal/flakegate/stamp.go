// A record's stamp: the commit pr-check began at and whether its working
// tree was clean then, so a merge can tell a record of this run's commit
// from one left by an earlier run, and a run over uncommitted code from one
// over a commit.
package flakegate

import "fmt"

// Stamp is where the run that wrote a record began: the commit HEAD named,
// and whether the working tree had no uncommitted changes. pr-check reads
// both once, as make parses the Makefile, and hands them to every pass and
// step it runs, so every record of one run carries the same stamp.
type Stamp struct {
	// Commit is the full commit hash, or empty for a record nobody stamped.
	Commit string
	// Clean is true when the tree had no uncommitted changes as the run
	// began.
	Clean bool
}

// ParseStamp reads a stamp from its two command-line values: commit, a
// full commit hash (40 or 64 lowercase hex digits), and clean, yes or no.
// Both empty is no stamp. One without the other is refused, since a commit
// whose tree may or may not have been clean says nothing a reader can use,
// and so is a value of either that is not well formed.
func ParseStamp(commit, clean string) (Stamp, error) {
	switch {
	case commit == "" && clean == "":
		return Stamp{}, nil
	case commit == "" || clean == "":
		return Stamp{}, fmt.Errorf("-commit and -clean go together: a record names the commit its run began at and whether its tree was clean then, or neither")
	case !IsCommitHash(commit):
		return Stamp{}, fmt.Errorf("-commit %q is not a full commit hash (40 or 64 lowercase hex digits)", commit)
	case clean != "yes" && clean != "no":
		return Stamp{}, fmt.Errorf("-clean is yes or no, not %q", clean)
	}
	return Stamp{Commit: commit, Clean: clean == "yes"}, nil
}

// IsCommitHash reports whether s is a full git object name in either hash
// a repository can use: 40 (SHA-1) or 64 (SHA-256) lowercase hex digits.
func IsCommitHash(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
