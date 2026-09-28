// Package remotefile reads and changes files on a device over an
// existing SSH connection, and is the single place this platform does
// that.
//
// It exists for the same reason pkg/remoteexec does. A Collection may
// import pkg/ and nothing else in this module, which internal/archtest
// enforces, so eleven file methods spread across three packages
// (internal/catalog/file and its line and block children) cannot share a
// helper unless that helper lives here. Without it each of them would
// carry its own idea of how to stat a path, how to compare a checksum
// and how to write a file without leaving a half-written one behind.
//
// # Everything is a shell command, and that is deliberate
//
// There is no SFTP client here. The transport a device is reachable over
// is an SSH exec channel (pkg/remoteexec), and every operation below is
// expressed as a POSIX command sent over it: stat, test, mktemp, mv,
// chmod, chown, sha256sum. Three reasons. It needs no second protocol
// and no second authentication path. It works against the same
// restricted and embedded targets that answer an exec request and run no
// SFTP subsystem. And it keeps one quoting boundary rather than two,
// which matters because a path is the most likely value in this whole
// namespace to arrive from a runbook variable.
//
// That makes this package the right tool for a file's metadata and for
// content small enough to be a module parameter. Moving a file's bytes
// in bulk, a firmware image or an archive, is a different job with a
// different port: pkg/filexfer, carried by SFTP (pkg/sftpxfer) or
// legacy SCP (pkg/scpxfer), which stream a file of any size and confine
// every path to the device's file_transfer_root.
//
// Every path is quoted with remoteexec.QuoteArg before it reaches the
// remote shell, so a path containing a space, a semicolon or a dollar
// sign is a path rather than syntax.
//
// # Reading before writing
//
// Every function that changes something reads first. That is not
// politeness, it is the whole idempotence and rollback contract: a
// method reports changed only when the state it found differs from the
// state it wants, and the inverse a method emits
// at run time is built from the values that read returned. Once a change is applied the
// prior state is gone, so the forward run is the only thing in a
// position to record it.
package remotefile

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
)

// Exit statuses this package assigns to its own probes, chosen outside
// the range the commands themselves use so a probe's answer cannot be
// confused with a failure to run it.
//
// POSIX test answers 0 for true and 1 for false, and a shell reserves
// 126 (cannot execute) and 127 (not found). 120 and up, below 126, is
// free.
const (
	// statusMissing means the probe ran and the path was not there, which
	// is an ANSWER rather than a failure.
	statusMissing = 120

	// statusUnreadable means a probe that lists a directory could not
	// list it, which is a failure: it says nothing about what is inside.
	statusUnreadable = 121

	// statusNotEmpty means the directory probe ran and found at least one
	// entry, which is an ANSWER rather than a failure.
	statusNotEmpty = 122
)

// Kind is what a path is, as far as this package needs to care.
type Kind string

const (
	// KindAbsent means nothing exists at the path.
	KindAbsent Kind = "absent"
	// KindFile is a regular file.
	KindFile Kind = "file"
	// KindDirectory is a directory.
	KindDirectory Kind = "directory"
	// KindSymlink is a symbolic link. A path is reported as a link even
	// when it points at a directory, because the thing at the path is the
	// link and that is what an inverse would have to restore.
	KindSymlink Kind = "symlink"
	// KindOther is anything else: a socket, a device node, a fifo. Named
	// rather than folded into KindFile so a method that would overwrite it
	// can refuse instead.
	KindOther Kind = "other"
)

// Info is what one path looks like on the device right now.
//
// It is the "before" half of a diff, and its field names are the keys a
// method records under diff.before, so an inverse reading the journal and
// a person reading --diff output see the same words.
type Info struct {
	// Kind is what is there, or KindAbsent.
	Kind Kind

	// Mode is the permission bits in octal, as text ("0644"), because
	// that is how a runbook writes them and how chmod takes them. Empty
	// when the path is absent.
	Mode string

	// Owner and Group are names rather than numeric ids, since a runbook
	// names them and a numeric id is not portable between devices.
	Owner string
	Group string

	// Target is where a symlink points. Empty for everything else.
	Target string

	// Size is the file size in bytes, and Mtime the modification time as
	// a Unix timestamp. Both are zero for an absent path.
	Size  int64
	Mtime int64
}

// Exists reports whether anything is at the path.
func (i Info) Exists() bool { return i.Kind != KindAbsent }

// Map renders the Info as the map a diff stat records, omitting what does
// not apply so a reader is not shown an owner for a path that is not
// there.
//
// The keys here are the contract: a method's own RecordInverse call
// passes them on, so renaming one silently breaks a rollback that
// nothing tests yet. They are written once, here, rather than by each
// method. (This cited pkg/collection.Inverse.Captures until Phase 40
// corrected it; no such type or field exists.)
func (i Info) Map() map[string]any {
	m := map[string]any{
		keyExists: i.Exists(),
		keyKind:   string(i.Kind),
	}
	if !i.Exists() {
		return m
	}
	m[keyMode] = i.Mode
	m[keyOwner] = i.Owner
	m[keyGroup] = i.Group
	m[keySize] = i.Size
	m[keyMtime] = i.Mtime
	if i.Kind == KindSymlink {
		m[keyTarget] = i.Target
	}
	return m
}

// Stat reads what is at path, reporting KindAbsent rather than an error
// when nothing is.
//
// Absence is an answer, not a failure, and the distinction is
// load-bearing: a method that treated "not there" as an error could never
// create anything, and one that treated a failed probe as "not there"
// would happily overwrite whatever it could not see.
//
// It uses `stat` with a format string rather than parsing `ls`, because
// ls output is localized, column-aligned and ambiguous about names
// containing spaces. The -c format is GNU coreutils and BusyBox; the
// fallback below covers the BSD form. Its mode is %Mp%Lp, the setuid,
// setgid and sticky digit then the permission bits, which is what GNU's %a
// prints: BSD's %Lp alone drops the first digit, so a setgid directory read
// as 755 and a task asking for 0755 never cleared it (measured on FreeBSD
// 15.1, FAILURE_PATTERNS 375).
func Stat(ctx context.Context, conn *remoteexec.Conn, path string) (Info, error) {
	quoted := remoteexec.QuoteArg(path)

	// Test for existence with -e OR -L, because a symlink pointing at
	// nothing fails -e and is still very much there: a method that
	// concluded "absent" would then try to create over it and get EEXIST
	// from the far side, reporting a confusing error instead of the real
	// situation.
	cmd := "if [ -e " + quoted + " ] || [ -L " + quoted + " ]; then " +
		"stat -c '%f|%a|%U|%G|%s|%Y' " + quoted + " 2>/dev/null || " +
		"stat -f '%Xp|%Mp%Lp|%Su|%Sg|%z|%m' " + quoted + "; " +
		"else exit " + strconv.Itoa(statusMissing) + "; fi"

	result, err := conn.Run(ctx, cmd)
	if err != nil {
		return Info{}, fmt.Errorf("stat %s: %w", path, err)
	}
	if result.ExitCode == statusMissing {
		return Info{Kind: KindAbsent}, nil
	}
	if result.ExitCode != 0 {
		return Info{}, fmt.Errorf("stat %s: exited %d: %s", path, result.ExitCode, firstLine(result.Stderr))
	}

	info, err := parseStat(strings.TrimSpace(result.Stdout))
	if err != nil {
		return Info{}, fmt.Errorf("stat %s: %w", path, err)
	}

	// A symlink's target needs a second question; stat's format has no
	// field for it.
	if info.Kind == KindSymlink {
		target, err := conn.Run(ctx, "readlink "+quoted)
		if err != nil {
			return Info{}, fmt.Errorf("readlink %s: %w", path, err)
		}
		if target.ExitCode == 0 {
			info.Target = strings.TrimSpace(target.Stdout)
		}
	}
	return info, nil
}

// DirectoryEmpty reports whether the directory at dirPath has no entries.
// It only reads: nothing on the device changes.
//
// It exists for a check. file.remove refuses a directory that is not
// empty unless its task allows recursion, and a real run learns that from
// rmdir's own atomic refusal. A check cannot send rmdir, so it asks this
// instead, and can then predict the same refusal rather than claiming a
// removal the real run would refuse.
//
// ls -A is POSIX and lists everything except "." and "..", hidden entries
// included, which is exactly the set rmdir cares about. A directory ls
// cannot list (no read permission) is an error rather than "empty": being
// unable to see inside is not evidence that nothing is there. dirPath
// must name a directory; a regular file would be listed as itself and
// reported not empty.
func DirectoryEmpty(ctx context.Context, conn *remoteexec.Conn, dirPath string) (bool, error) {
	quoted := remoteexec.QuoteArg(dirPath)

	// The listing is captured rather than piped, because a pipeline's exit
	// status in POSIX sh is its last command's, and "ls failed" has to stay
	// distinguishable from "ls printed nothing".
	cmd := "entries=$(ls -A -- " + quoted + ") || exit " + strconv.Itoa(statusUnreadable) + "; " +
		"if [ -z \"$entries\" ]; then exit 0; fi; exit " + strconv.Itoa(statusNotEmpty)

	result, err := conn.Run(ctx, cmd)
	if err != nil {
		return false, fmt.Errorf("list %s: %w", dirPath, err)
	}
	switch result.ExitCode {
	case 0:
		return true, nil
	case statusNotEmpty:
		return false, nil
	default:
		return false, fmt.Errorf("list %s: exited %d: %s", dirPath, result.ExitCode, firstLine(result.Stderr))
	}
}

// parseStat turns one line of the stat format above into an Info.
//
// The first field is the raw mode in hex, whose high bits are the file
// type. Reading the type from there rather than asking `test -f` and
// `test -d` separately keeps the whole read to one round trip and, more
// importantly, keeps it ATOMIC in the only sense available here: three
// separate probes can disagree if something changes between them.
func parseStat(line string) (Info, error) {
	parts := strings.Split(line, "|")
	if len(parts) != 6 {
		return Info{}, fmt.Errorf("unexpected stat output %q", line)
	}

	raw, err := strconv.ParseUint(parts[0], 16, 64)
	if err != nil {
		return Info{}, fmt.Errorf("unexpected stat mode %q", parts[0])
	}

	info := Info{
		Kind:  kindFromRawMode(raw),
		Mode:  normalizeMode(parts[1]),
		Owner: parts[2],
		Group: parts[3],
	}
	if info.Size, err = strconv.ParseInt(parts[4], 10, 64); err != nil {
		return Info{}, fmt.Errorf("unexpected stat size %q", parts[4])
	}
	if info.Mtime, err = strconv.ParseInt(parts[5], 10, 64); err != nil {
		return Info{}, fmt.Errorf("unexpected stat mtime %q", parts[5])
	}
	return info, nil
}

// File type bits from the raw mode, which are POSIX and identical on
// every target this reaches.
const (
	typeMask    = 0xF000
	typeFile    = 0x8000
	typeDir     = 0x4000
	typeSymlink = 0xA000
)

func kindFromRawMode(raw uint64) Kind {
	switch raw & typeMask {
	case typeFile:
		return KindFile
	case typeDir:
		return KindDirectory
	case typeSymlink:
		return KindSymlink
	default:
		return KindOther
	}
}

// normalizeMode pads an octal permission string to four digits, so "644"
// and "0644" compare equal.
//
// Without this a method comparing the mode it wants against the mode it
// found reports a change on every single run, because stat prints 644 and
// a runbook almost always writes 0644. That is the classic shape of a
// module that is never idempotent, and it is invisible until somebody
// notices every run says changed.
func normalizeMode(mode string) string {
	mode = strings.TrimSpace(mode)
	if mode == "" {
		return ""
	}
	for len(mode) < 4 {
		mode = "0" + mode
	}
	return mode
}

// NormalizeMode exposes normalizeMode to the methods, which need it to
// compare a runbook's requested mode against what Stat reported.
func NormalizeMode(mode string) string { return normalizeMode(mode) }

// firstLine returns the first line of s, trimmed, for an error message
// that should not carry a page of output.
func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	if s == "" {
		return "no output"
	}
	return s
}
