// Package remotefile: where a path resolves on the device (Phase 46a), for a
// check that records or asks about a path as the device will meet it rather
// than as a runbook spelled it.
package remotefile

import (
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/farside"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// locateScript resolves $1, the parent of the path asked about, and $2, its
// last name. It walks up from $1 to the nearest directory that exists, keeping
// the names it passed (the tail, which a real run may yet make), resolves that
// directory's every link (cd -P, pwd -P), and prints the physical directory,
// the tail and whether the path itself is a directory and not a link, each
// NUL-terminated. Then it prints the "dev:ino" of the path when it is such a
// directory and of every directory from the physical one up to /, one line
// each, through GNU stat (busybox's too) or BSD stat, whichever answers. The
// flavor is chosen once, up front: a GNU stat that failed partway must not
// fall through to "stat -f", which GNU reads as filesystem status and whose
// output could pass for identities. It is one line, since the account's login
// shell parses the quoted command before /bin/sh runs it, and csh refuses a
// newline inside quotes.
const locateScript = `d=$1; name=$2; tail=; ` +
	`while [ ! -d "$d" ]; do [ "$d" = / ] && exit 3; tail=/${d##*/}$tail; d=${d%/*}; [ -n "$d" ] || d=/; done; ` +
	`cd -P -- "$d" || exit 3; phys=$(pwd -P) || exit 3; ` +
	`case $phys in /) base= ;; *) base=$phys ;; esac; ` +
	`set --; self=-; ` +
	`if [ -z "$tail" ] && [ -n "$name" ] && [ -d "$base/$name" ] && [ ! -L "$base/$name" ]; then self=d; set -- "$base/$name"; fi; ` +
	`printf '%s\0%s\0%s\0' "$phys" "$tail" "$self"; ` +
	`p=$phys; while :; do set -- "$@" "$p"; [ "$p" = / ] && break; p=${p%/*}; [ -n "$p" ] || p=/; done; ` +
	`if stat -L -c %d / >/dev/null 2>&1; then stat -L -c '%d:%i' -- "$@"; else stat -L -f '%d:%i' -- "$@"; fi`

// Locate reads where target, an absolute and cleaned path, resolves on the
// device: its physical path (each link above it resolved; a link at target
// itself is the link, as removing it removes the link), and the identity of
// each directory along it that exists, target among them when it is a
// directory and not a link. The part of target below the deepest directory
// that exists is kept as written, since only a real run makes it. It only
// reads, in one command run by /bin/sh (the login shell only passes it on).
//
// A path that is not absolute and cleaned is refused: what "a/../b" or a
// relative path names depends on links and the login's directory, and a
// check that cannot say exactly which path it means must not record one.
func Locate(ctx context.Context, conn *remoteexec.Conn, target string) (wire.Location, error) {
	if !path.IsAbs(target) || path.Clean(target) != target {
		return wire.Location{}, fmt.Errorf("locate %s: not an absolute, cleaned path", target)
	}
	parent, name := path.Dir(target), path.Base(target)
	if target == "/" {
		parent, name = "/", ""
	}
	result, err := conn.Run(ctx, remoteexec.QuoteCommand([]string{"sh", "-c", locateScript, "sh", parent, name}))
	if err != nil {
		return wire.Location{}, fmt.Errorf("locate %s: %w", target, err)
	}
	if result.ExitCode != 0 {
		return wire.Location{}, fmt.Errorf("locate %s: exited %d: %s", target, result.ExitCode, farside.Output(result.Stderr))
	}
	loc, err := parseLocation(result.Stdout, name)
	if err != nil {
		return wire.Location{}, fmt.Errorf("locate %s: %w", target, err)
	}
	return loc, nil
}

// parseLocation reads locateScript's output for the path whose last name is
// name.
func parseLocation(out, name string) (wire.Location, error) {
	fields := strings.SplitN(out, "\x00", 4)
	if len(fields) != 4 {
		return wire.Location{}, fmt.Errorf("unexpected output %q", out)
	}
	phys, tail, self, ids := fields[0], fields[1], fields[2], strings.Fields(fields[3])
	loc := wire.Location{Path: path.Join(phys, tail, name)}
	if !path.IsAbs(phys) || path.Clean(phys) != phys || (self != "-" && self != "d") {
		return wire.Location{}, fmt.Errorf("unexpected output %q", out)
	}
	var dirs []string
	if self == "d" {
		dirs = append(dirs, loc.Path)
	}
	for p := phys; ; p = path.Dir(p) {
		dirs = append(dirs, p)
		if p == "/" {
			break
		}
	}
	if len(ids) != len(dirs) {
		return wire.Location{}, fmt.Errorf("%d identities for %d directories", len(ids), len(dirs))
	}
	for i, dir := range dirs {
		loc.Dirs = append(loc.Dirs, wire.DirID{Path: dir, ID: ids[i]})
	}
	if !wire.ValidLocation(loc) {
		return wire.Location{}, fmt.Errorf("%q is not a location", loc.Path)
	}
	return loc, nil
}
