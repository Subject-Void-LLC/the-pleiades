// Package loader: the approval list's format, which says which build of
// which program a person has agreed to let Pleiades run.
package loader

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/termsafe"
)

// ApprovalFile is the approval list's name inside a collections
// directory. It starts with "." so Load never mistakes it for a program.
const ApprovalFile = ".pleiades-approvals.json"

// approvalVersion is the only version of the approval list's format this
// build reads. Phase 43's registry lockfile is meant to grow out of this
// file, adding fields (an OCI reference per entry); an added optional
// field keeps the version, and anything an older reader could not
// survive changes it.
const approvalVersion = 1

// maxApprovalFile bounds how much of the approval list is read. A real
// one holds a line or two per program.
const maxApprovalFile = 1 << 20

// digestPattern is the one digest form Load pins and this file records.
var digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// Approval is one approved build of one program: the person who ran
// `pleiades collection approve` agreed that the program with this file
// name, whose bytes hash to this digest, may run. A program may have
// several, so a rolling upgrade can approve the new build before any
// machine runs it and keep the old one approved until none does.
type Approval struct {
	// Program is the program's file name in the collections directory.
	Program string `json:"program"`

	// Digest is the approved build's SHA-256 digest, "sha256:<hex>".
	Digest string `json:"digest"`

	// ApprovedBy is the local account that approved it. It is a record of
	// who did it, not an authentication: anyone who can write this file
	// can write any name here, exactly as they could replace a program.
	ApprovedBy string `json:"approved_by"`

	// ApprovedAt is when, in RFC 3339, UTC.
	ApprovedAt string `json:"approved_at"`
}

// approvalList is the file's whole content.
type approvalList struct {
	Version   int        `json:"version"`
	Approvals []Approval `json:"approvals"`
}

// validate refuses an approval that could not have been written by
// Approve: a program name that is not a plain file name, a digest in any
// other form, or a missing account or time.
func (a Approval) validate() error {
	switch {
	case a.Program == "" || a.Program == "." || a.Program == ".." || strings.ContainsAny(a.Program, "/\x00") || strings.HasPrefix(a.Program, "."):
		return fmt.Errorf("program %q is not the file name of a program in the directory", a.Program)
	case !digestPattern.MatchString(a.Digest):
		return fmt.Errorf("digest %q for program %q is not sha256:<64 lowercase hex digits>", a.Digest, a.Program)
	case a.ApprovedBy == "":
		return fmt.Errorf("the approval of %q (%s) names no account", a.Program, a.Digest)
	case termsafe.Check(a.ApprovedBy) != nil || strings.ContainsAny(a.ApprovedBy, "\n\t"):
		return fmt.Errorf("the approval of %q (%s) names an account holding a character a terminal acts on", a.Program, a.Digest)
	}
	if _, err := time.Parse(time.RFC3339, a.ApprovedAt); err != nil {
		return fmt.Errorf("the approval of %q (%s) has an unreadable time %q", a.Program, a.Digest, a.ApprovedAt)
	}
	return nil
}
