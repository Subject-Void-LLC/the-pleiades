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

// removableScript exits 0 when the login may remove $2, an entry of the
// directory $1, as far as the device's rules can say before trying: $1 is a
// directory it may write and search, and when $1 has the sticky bit, the
// login is root or owns $1 or $2 (POSIX find -user, since test -O is not in
// every sh). It exits 1 when it may not or cannot be told, anything else when
// it could not ask.
//
// test -w and -x answer through access(2) (the mode bits, an ACL, a read-only
// mount, root) in dash, bash and FreeBSD's sh. BusyBox's test reads the mode
// bits itself: for root it says writable to anything, and for anyone it does
// not see a read-only mount. Under BusyBox (the running shell's /proc/$$/exe,
// or sh --help naming it) a root login is therefore answered 1, and another
// login only when the mount holding $1 (the longest mount point above it in
// mountinfo, $3 or /proc/self/mountinfo) is read-write; an ACL it still cannot
// see. $1 must be a physical path, as Locate gives.
const removableScript = `d=$1; e=$2; mi=${3:-/proc/self/mountinfo}; bb=; ` +
	`case "$(readlink /proc/$$/exe 2>/dev/null)" in */busybox*) bb=1 ;; esac; ` +
	`case "$(sh --help 2>&1)" in *BusyBox*) bb=1 ;; esac; ` +
	`[ -d "$d" ] && [ -w "$d" ] && [ -x "$d" ] || exit 1; ` +
	`u=$(id -u) || exit 2; ` +
	`if [ -n "$bb" ]; then ` +
	`[ "$u" = 0 ] && exit 1; ` +
	`o=$(D=$d awk '{ d = ENVIRON["D"]; m = $5; gsub(/\\040/, " ", m); gsub(/\\134/, "\\", m); ` +
	`if ((d == m || m == "/" || index(d, m "/") == 1) && length(m) >= n) { n = length(m); o = $6 } } END { print o }' "$mi") || exit 2; ` +
	`case ",$o," in *,rw,*) ;; *) exit 1 ;; esac; fi; ` +
	`[ -k "$d" ] || exit 0; ` +
	`[ "$u" = 0 ] && exit 0; ` +
	`[ -n "$(find "$d" -prune -user "$u")" ] && exit 0; ` +
	`[ -n "$(find "$e" -prune -user "$u")" ] && exit 0; exit 1`

// Removable reports whether the login may remove target, an absolute path,
// as far as the device's rules can say before trying: the directory holding
// it is writable and searchable for the login and, when it has the sticky
// bit, the login is root or owns that directory or target; under a BusyBox
// shell, whose test cannot say for root or for a read-only mount, it answers
// false for root and reads the mount table for anyone else. It only reads, in one
// command run by /bin/sh. It cannot see what refuses a removal only when it is
// tried (an immutable flag, a busy mount point, an ACL that denies deleting
// while allowing writing, as macOS and ZFS can), so a caller leaning on its
// answer for a later task's prediction says so.
func Removable(ctx context.Context, conn *remoteexec.Conn, target string) (bool, error) {
	result, err := conn.Run(ctx, remoteexec.QuoteCommand([]string{"sh", "-c", removableScript, "sh", path.Dir(target), target}))
	if err != nil {
		return false, fmt.Errorf("ask whether %s may be removed: %w", target, err)
	}
	switch result.ExitCode {
	case 0:
		return true, nil
	case 1:
		return false, nil
	default:
		return false, fmt.Errorf("ask whether %s may be removed: exited %d: %s", target, result.ExitCode, farside.Output(result.Stderr))
	}
}
