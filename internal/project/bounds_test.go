// Package project_test's bounds coverage: the length limits a writer checks
// before the store are the database's own, and CheckLengths names the first
// field over its limit.
package project_test

import (
	"errors"
	"strings"
	"testing"

	// The ent package's init installs the generated validators this test
	// calls; without it they are nil.
	_ "github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	entlaunchable "github.com/Subject-Void-LLC/the-pleiades/internal/ent/launchable"
	entproject "github.com/Subject-Void-LLC/the-pleiades/internal/ent/project"
	"github.com/Subject-Void-LLC/the-pleiades/internal/project"
)

// TestFieldBounds_AreTheDatabasesOwn holds each project bound equal to the
// generated validator of the column it guards: the bound itself is accepted
// and one byte more is refused. A bound looser than the column answers 500
// at the insert (FAILURE_PATTERNS 1076); one tighter refuses a value the
// database would store.
func TestFieldBounds_AreTheDatabasesOwn(t *testing.T) {
	for _, tc := range []struct {
		field     string
		max       int
		validator func(string) error
	}{
		{"name", project.MaxNameBytes, entproject.NameValidator},
		// The store copies the name into the launchable row in the same
		// transaction, so its column bounds the name as well.
		{"launchable name", project.MaxNameBytes, entlaunchable.NameValidator},
		{"description", project.MaxDescriptionBytes, entproject.DescriptionValidator},
		{"scm_url", project.MaxSCMURLBytes, entproject.ScmURLValidator},
		{"scm_branch", project.MaxSCMBranchBytes, entproject.ScmBranchValidator},
	} {
		t.Run(tc.field, func(t *testing.T) {
			if tc.validator == nil {
				t.Fatal("the generated validator is nil, so nothing installed it")
			}
			if err := tc.validator(strings.Repeat("n", tc.max)); err != nil {
				t.Errorf("the database refuses a %s of the bound, %d bytes: %v", tc.field, tc.max, err)
			}
			if err := tc.validator(strings.Repeat("n", tc.max+1)); err == nil {
				t.Errorf("the database accepts a %s of %d bytes, so the bound %d is tighter than it", tc.field, tc.max+1, tc.max)
			}
		})
	}
}

// TestCheckLengths_NamesTheFirstFieldOverItsBound covers each field at its
// bound and one byte past it, and that the bound is in bytes.
func TestCheckLengths_NamesTheFirstFieldOverItsBound(t *testing.T) {
	within := project.Project{
		Name:        strings.Repeat("n", project.MaxNameBytes),
		Description: strings.Repeat("d", project.MaxDescriptionBytes),
		SCMURL:      strings.Repeat("u", project.MaxSCMURLBytes),
		SCMBranch:   strings.Repeat("b", project.MaxSCMBranchBytes),
	}
	if err := project.CheckLengths(within); err != nil {
		t.Fatalf("every field at its bound was refused: %v", err)
	}

	for _, tc := range []struct {
		field string
		max   int
		over  func(*project.Project)
	}{
		{"name", project.MaxNameBytes, func(p *project.Project) { p.Name += "n" }},
		{"description", project.MaxDescriptionBytes, func(p *project.Project) { p.Description += "d" }},
		{"scm_url", project.MaxSCMURLBytes, func(p *project.Project) { p.SCMURL += "u" }},
		{"scm_branch", project.MaxSCMBranchBytes, func(p *project.Project) { p.SCMBranch += "b" }},
		// 127 two-byte characters are 254 bytes.
		{"name", project.MaxNameBytes, func(p *project.Project) { p.Name = strings.Repeat("\xc3\xa9", 127) }},
	} {
		t.Run(tc.field, func(t *testing.T) {
			p := within
			tc.over(&p)
			err := project.CheckLengths(p)
			var lengthErr *project.LengthError
			if !errors.As(err, &lengthErr) {
				t.Fatalf("CheckLengths = %v, want a *LengthError", err)
			}
			if lengthErr.Field != tc.field || lengthErr.MaxBytes != tc.max || lengthErr.Bytes != tc.max+1 {
				t.Errorf("LengthError = %+v, want field %s, bound %d, %d bytes", lengthErr, tc.field, tc.max, tc.max+1)
			}
			if msg := err.Error(); !strings.Contains(msg, tc.field) || !strings.Contains(msg, "at most") {
				t.Errorf("the message %q does not name the field and its bound", msg)
			}
		})
	}
}
