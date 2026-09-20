//go:build unix

// Package loader: reading and writing the approval list.
package loader

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// ReadApprovals returns every approval in dir's approval list, in file
// order, or none when there is no list yet.
//
// The list is held to the same rules as a program: a regular file, not a
// symlink, owned by this user or root and writable by nobody else. Its
// content is refused whole on any fault (an unknown field, a malformed
// entry, trailing data, another version), never read entry by entry,
// because a line that cannot be read might be the one that withdrew an
// approval.
func ReadApprovals(dir string) ([]Approval, error) {
	root, err := checkDir(dir)
	if err != nil {
		return nil, err
	}
	return readApprovals(root)
}

// readApprovals is ReadApprovals for a directory checkDir already
// resolved.
func readApprovals(root string) ([]Approval, error) {
	path := filepath.Join(root, ApprovalFile)
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("approval list %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("approval list %s is not a regular file (%s)", path, describeFileType(info.Mode()))
	}
	if err := checkOwnership(info); err != nil {
		return nil, fmt.Errorf("approval list %s %w", path, err)
	}

	f, err := os.Open(path) // #nosec G304 -- a fixed file name inside a directory checkDir vetted
	if err != nil {
		return nil, fmt.Errorf("approval list %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, maxApprovalFile+1))
	if err != nil {
		return nil, fmt.Errorf("approval list %s: %w", path, err)
	}
	if len(data) > maxApprovalFile {
		return nil, fmt.Errorf("approval list %s is over %d bytes", path, maxApprovalFile)
	}
	return parseApprovals(path, data)
}

// parseApprovals decodes an approval list's content, refusing it whole on
// any fault. path only names the file in errors.
func parseApprovals(path string, data []byte) ([]Approval, error) {
	var list approvalList
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&list); err != nil {
		return nil, fmt.Errorf("approval list %s is not valid, so no program in the directory is approved: %w", path, err)
	}
	if dec.More() {
		return nil, fmt.Errorf("approval list %s has data after its one document, so no program in the directory is approved", path)
	}
	if list.Version != approvalVersion {
		return nil, fmt.Errorf("approval list %s is version %d; this build reads version %d only", path, list.Version, approvalVersion)
	}
	for _, a := range list.Approvals {
		if err := a.validate(); err != nil {
			return nil, fmt.Errorf("approval list %s is refused whole, so no program in the directory is approved: %w", path, err)
		}
	}
	return list.Approvals, nil
}

// approvedDigests returns, for each program, every digest approved for
// it.
func approvedDigests(approvals []Approval) map[string][]string {
	byProgram := map[string][]string{}
	for _, a := range approvals {
		byProgram[a.Program] = append(byProgram[a.Program], a.Digest)
	}
	return byProgram
}

// checkApproved refuses a program whose digest is not approved, saying
// which of the two cases it is: never approved, or approved as another
// build.
func checkApproved(approved map[string][]string, path, digest string) error {
	name := filepath.Base(path)
	digests := approved[name]
	if slices.Contains(digests, digest) {
		return nil
	}
	if len(digests) == 0 {
		return fmt.Errorf("program %s (%s) is not approved to run; approve it with `pleiades collection approve %s` on the machine that holds this directory", path, digest, name)
	}
	return fmt.Errorf("program %s changed since it was approved (approved: %s; now: %s); if this build is meant to run, approve it with `pleiades collection approve %s`",
		path, strings.Join(digests, ", "), digest, name)
}

// Approve adds a to dir's approval list, creating the list if there is
// none. An approval already present (the same program and digest) is not
// added twice. The list is written whole to a temporary file beside it,
// readable and writable by this user only, and renamed over it, so a
// reader never sees half a list.
func Approve(dir string, a Approval) error {
	if err := a.validate(); err != nil {
		return err
	}
	root, err := checkDir(dir)
	if err != nil {
		return err
	}
	approvals, err := readApprovals(root)
	if err != nil {
		return err
	}
	if slices.ContainsFunc(approvals, func(e Approval) bool { return e.Program == a.Program && e.Digest == a.Digest }) {
		return nil
	}
	return writeApprovals(root, append(approvals, a))
}

// Revoke removes the approvals of program from dir's approval list:
// every one when digest is empty, and only that build's otherwise. It
// returns how many it removed.
func Revoke(dir, program, digest string) (int, error) {
	root, err := checkDir(dir)
	if err != nil {
		return 0, err
	}
	approvals, err := readApprovals(root)
	if err != nil {
		return 0, err
	}
	kept := slices.DeleteFunc(slices.Clone(approvals), func(e Approval) bool {
		return e.Program == program && (digest == "" || e.Digest == digest)
	})
	removed := len(approvals) - len(kept)
	if removed == 0 {
		return 0, nil
	}
	return removed, writeApprovals(root, kept)
}

// writeApprovals replaces root's approval list with approvals.
func writeApprovals(root string, approvals []Approval) error {
	if approvals == nil {
		approvals = []Approval{}
	}
	data, err := json.MarshalIndent(approvalList{Version: approvalVersion, Approvals: approvals}, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(root, ApprovalFile+".*.tmp")
	if err != nil {
		return fmt.Errorf("writing the approval list: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing the approval list: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing the approval list: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing the approval list: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("writing the approval list: %w", err)
	}
	if err := os.Rename(tmp.Name(), filepath.Join(root, ApprovalFile)); err != nil {
		return fmt.Errorf("writing the approval list: %w", err)
	}
	return nil
}
