// This file holds the length bounds a saved project is held to, in one
// place, so a writer can refuse an over-long value before the store does.
//
// The database declares each bound (internal/ent/schema/project.go:
// MaxLen(253) on name, 1024 on description, 2048 on scm_url, 255 on
// scm_branch), and the store copies the name into the project's launchable
// row, whose own name is MaxLen(253) too. Nothing checked them in front of
// the store, so an over-long value reached ent's validator and answered 500
// "internal error" on create and on update (FAILURE_PATTERNS 1076).
// TestFieldBounds_AreTheDatabasesOwn holds these constants equal to the
// generated validators, so the two cannot drift apart.
package project

import "fmt"

// The longest value, in bytes, each project field can be saved with. Bytes,
// as the database measures, so a value of two-byte characters reaches the
// bound at half as many characters.
const (
	// MaxNameBytes is the longest name, the project row's bound and the
	// launchable row's alike.
	MaxNameBytes = 253
	// MaxDescriptionBytes is the longest description.
	MaxDescriptionBytes = 1024
	// MaxSCMURLBytes is the longest source URL.
	MaxSCMURLBytes = 2048
	// MaxSCMBranchBytes is the longest branch, tag or commit to check out.
	MaxSCMBranchBytes = 255
)

// LengthError is the refusal of a field longer than its bound. Its text
// names the field and the bound, and repeats nothing the caller sent, so a
// handler may answer it as it is.
type LengthError struct {
	// Field is the field's wire name, such as "scm_url".
	Field string
	// MaxBytes is the field's bound, and Bytes the length it was given.
	MaxBytes, Bytes int
}

// Error says which field is too long, by how much, and what the bound is.
func (e *LengthError) Error() string {
	return fmt.Sprintf("%s is %d bytes; a project's %s may be at most %d bytes",
		e.Field, e.Bytes, e.Field, e.MaxBytes)
}

// CheckLengths returns a *LengthError for the first of p's fields, in the
// order name, description, scm_url, scm_branch, that is longer than its
// bound, or nil. It measures the values as given, so a caller trims them
// first when the store would save them trimmed.
func CheckLengths(p Project) error {
	for _, field := range []struct {
		name  string
		value string
		max   int
	}{
		{"name", p.Name, MaxNameBytes},
		{"description", p.Description, MaxDescriptionBytes},
		{"scm_url", p.SCMURL, MaxSCMURLBytes},
		{"scm_branch", p.SCMBranch, MaxSCMBranchBytes},
	} {
		if len(field.value) > field.max {
			return &LengthError{Field: field.name, MaxBytes: field.max, Bytes: len(field.value)}
		}
	}
	return nil
}
