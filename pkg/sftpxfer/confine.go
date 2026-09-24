// Physical containment for SFTP: where the root and the parent directory
// really are, worked out on the client from LSTAT and READLINK.
package sftpxfer

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/filexfer"
)

// maxSymlinkHops bounds how many symlinks resolving one directory may
// follow, so a link loop on the device ends in a refusal rather than a
// transfer that walks forever. It is Linux's own MAXSYMLINKS.
const maxSymlinkHops = 40

// errSymlinkLoop is resolveDir's refusal when maxSymlinkHops is spent.
var errSymlinkLoop = errors.New("too many levels of symbolic links")

// target is where a transfer will actually operate once confinement has
// passed: the physically resolved parent directory and the final
// component's current state there.
type target struct {
	// path is the physical parent joined with the file's own name, the
	// only path the transfer touches from here on.
	path string
	// parent is the physically resolved parent directory.
	parent string
	// info is the final component's Lstat, nil when it does not exist.
	info fs.FileInfo
}

// confine works out where p's root and parent directory physically are,
// refuses a parent outside the root, then describes the final component
// without following it. It sends only LSTAT and READLINK requests, so a
// refused transfer has created, opened and written nothing on the
// device.
//
// It resolves symlinks itself rather than asking the server with
// REALPATH, and that is the point of it. REALPATH's answer is whatever
// the server chooses to canonicalize to, and not every server follows
// symlinks: pkg/sftp's own server answers it with filepath.Abs and a
// lexical clean (server.go), so a check that trusted REALPATH would pass
// exactly the symlink it exists to catch on any server built on that
// library. LSTAT and READLINK describe what the device's filesystem
// holds, which is the only answer a containment check can rely on.
func (cl *Client) confine(p filexfer.Path) (target, error) {
	root, err := cl.resolveDir("/", p.Root())
	if err != nil {
		return target{}, cl.missing(p, err, filexfer.ContainmentRootMissing)
	}
	// The parent is resolved from the PHYSICAL root, not the lexical
	// one, which is equivalent (the root's own components resolve to
	// root) and saves walking them twice.
	parent := root
	if rel := path.Dir(p.Rel()); rel != "." {
		parent, err = cl.resolveDir(root, rel)
		if err != nil {
			return target{}, cl.missing(p, err, filexfer.ContainmentParentMissing)
		}
	}
	if err := filexfer.Contained(p, root, parent); err != nil {
		return target{}, err
	}

	t := target{path: path.Join(parent, p.Base()), parent: parent}
	info, err := cl.c.Lstat(t.path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return t, nil
	case err != nil:
		return target{}, fmt.Errorf("sftpxfer: describe %q: %w", p.String(), err)
	}
	t.info = info
	return t, nil
}

// resolveDir resolves rel against the physical directory start, one
// component at a time: LSTAT sees each component without following it,
// a symlink is followed by reading its target with READLINK and walking
// that target's own components (from "/" when it is absolute), and every
// component that is not a symlink must be a directory. ".." in a link
// target climbs from the physical directory reached so far, which is
// POSIX's own rule. The result is an absolute, canonical path built
// here, never one the server supplied.
func (cl *Client) resolveDir(start, rel string) (string, error) {
	resolved := start
	pending := splitPath(rel)
	hops := 0
	for len(pending) > 0 {
		segment := pending[0]
		pending = pending[1:]
		switch segment {
		case "", ".":
			continue
		case "..":
			resolved = path.Dir(resolved)
			continue
		}

		next := path.Join(resolved, segment)
		info, err := cl.c.Lstat(next)
		if err != nil {
			return "", err
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			hops++
			if hops > maxSymlinkHops {
				return "", fmt.Errorf("resolve %q: %w", next, errSymlinkLoop)
			}
			link, err := cl.c.ReadLink(next)
			if err != nil {
				return "", err
			}
			if strings.HasPrefix(link, "/") {
				resolved = "/"
			}
			pending = append(splitPath(link), pending...)
			continue
		}
		if !info.IsDir() {
			return "", fmt.Errorf("resolve %q: not a directory: %w", next, fs.ErrNotExist)
		}
		resolved = next
	}
	return resolved, nil
}

// missing turns a resolution failure into the refusal a caller acts on:
// a missing root or parent directory is a ContainmentError naming which
// one, and anything else (a link loop, a server failure) is reported as
// the failure it is.
func (cl *Client) missing(p filexfer.Path, err error, which filexfer.Containment) error {
	if errors.Is(err, fs.ErrNotExist) {
		return &filexfer.ContainmentError{Path: p.String(), Reason: which}
	}
	return fmt.Errorf("sftpxfer: resolve %q: %w", p.String(), err)
}

// splitPath splits a slash-separated path into its components, dropping
// the empty one a leading slash produces.
func splitPath(p string) []string {
	return strings.Split(strings.TrimPrefix(p, "/"), "/")
}

// leafKind reports t's final component to filexfer.LeafKind.
func (t target) leafKind(p filexfer.Path, forGet bool) error {
	if t.info == nil {
		return filexfer.LeafKind(p, 0, false, forGet)
	}
	return filexfer.LeafKind(p, kindOf(t.info.Mode()), true, forGet)
}
