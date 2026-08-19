package remotefile

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
)

// Checksum returns the SHA-256 of the file at the given path in hex, and
// reports whether the file was there at all.
//
// It is what decides whether a write is a change. Comparing content
// rather than comparing timestamps or sizes is the difference between a
// module that is idempotent and one that merely looks it: two files of
// the same length with different bytes are a change, and a file rewritten
// with identical bytes is not.
//
// The hash is computed ON THE DEVICE, which is the point. Pulling the
// file back to compare locally would move the whole file across the
// network to answer a question a 64-character answer settles, and for a
// large file that is the difference between a fast task and a slow one.
func Checksum(ctx context.Context, conn *remoteexec.Conn, filePath string) (string, bool, error) {
	quoted := remoteexec.QuoteArg(filePath)

	// sha256sum is coreutils and BusyBox; shasum -a 256 is the BSD and
	// macOS spelling. cut takes the hash off the front of either, since
	// both print "<hash>  <filename>".
	cmd := "if [ -f " + quoted + " ]; then " +
		"{ sha256sum " + quoted + " 2>/dev/null || shasum -a 256 " + quoted + "; } | cut -d' ' -f1; " +
		"else exit " + fmt.Sprint(statusMissing) + "; fi"

	result, err := conn.Run(ctx, cmd)
	if err != nil {
		return "", false, fmt.Errorf("checksum %s: %w", filePath, err)
	}
	if result.ExitCode == statusMissing {
		return "", false, nil
	}
	if result.ExitCode != 0 {
		return "", false, fmt.Errorf("checksum %s: exited %d: %s", filePath, result.ExitCode, firstLine(result.Stderr))
	}

	sum := strings.TrimSpace(result.Stdout)
	if len(sum) != sha256.Size*2 {
		return "", false, fmt.Errorf("checksum %s: unexpected output %q", filePath, sum)
	}
	return sum, true, nil
}

// ChecksumOf returns the SHA-256 of content in the same hex form
// Checksum reports, so a caller can decide whether a write would change
// anything without sending the content first.
func ChecksumOf(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

// Write replaces the file at filePath with content, atomically.
//
// ATOMIC MEANS SOMETHING SPECIFIC HERE, and it is why this is not simply
// a redirect. The content is written to a temporary file in the SAME
// DIRECTORY and then renamed over the target. rename(2) within one
// filesystem is atomic, so any reader of the path sees either the whole
// old file or the whole new one, never a partial write. A plain
// "cat > file" truncates first, so a reader arriving mid-transfer sees an
// empty or half-written file, and a connection dropped mid-transfer
// leaves it that way permanently. For a configuration file that a
// service reads on startup, that is the difference between a failed task
// and a failed service.
//
// The temporary lands in the target's own directory rather than /tmp
// because rename cannot cross a filesystem boundary, and /tmp is very
// often a different one.
//
// The content travels on the command's standard input rather than inside
// the command line. A command line is bounded (ARG_MAX), appears in the
// device's process list while it runs, and would have to be quoted; stdin
// has none of those properties, which matters most for the one case that
// combines all three, a rendered configuration file holding a secret.
func Write(ctx context.Context, conn *remoteexec.Conn, filePath string, content []byte) error {
	dir := path.Dir(filePath)
	quotedDir := remoteexec.QuoteArg(dir)
	quotedPath := remoteexec.QuoteArg(filePath)

	// One shell command so the temporary cannot be orphaned by a
	// connection dropped between two of them. The trap removes it on any
	// exit path that is not the successful rename.
	cmd := "set -e; " +
		"tmp=$(mktemp " + quotedDir + "/.pleiades.XXXXXX); " +
		"trap 'rm -f \"$tmp\"' EXIT; " +
		"cat > \"$tmp\"; " +
		"mv -f \"$tmp\" " + quotedPath + "; " +
		"trap - EXIT"

	result, err := conn.RunWithStdin(ctx, cmd, strings.NewReader(string(content)))
	if err != nil {
		return fmt.Errorf("write %s: %w", filePath, err)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("write %s: exited %d: %s", filePath, result.ExitCode, firstLine(result.Stderr))
	}
	return nil
}

// Read returns the contents of the file at filePath.
//
// It reports the same absent-is-not-an-error answer Stat and Checksum do,
// so a caller reading a file it may be about to create does not have to
// tell "empty" from "missing" by inspecting an error string.
func Read(ctx context.Context, conn *remoteexec.Conn, filePath string) (string, bool, error) {
	quoted := remoteexec.QuoteArg(filePath)
	cmd := "if [ -f " + quoted + " ]; then cat " + quoted + "; else exit " + fmt.Sprint(statusMissing) + "; fi"

	result, err := conn.Run(ctx, cmd)
	if err != nil {
		return "", false, fmt.Errorf("read %s: %w", filePath, err)
	}
	if result.ExitCode == statusMissing {
		return "", false, nil
	}
	if result.ExitCode != 0 {
		return "", false, fmt.Errorf("read %s: exited %d: %s", filePath, result.ExitCode, firstLine(result.Stderr))
	}
	return result.Stdout, true, nil
}

// Attributes are the ownership and permission bits a task can request.
// An empty field means "leave this alone", which is what lets a method
// change a mode without also having an opinion about the owner.
type Attributes struct {
	Mode  string
	Owner string
	Group string
}

// Empty reports whether this asks for nothing.
func (a Attributes) Empty() bool { return a.Mode == "" && a.Owner == "" && a.Group == "" }

// Apply sets whichever of mode, owner and group the Attributes name, and
// reports whether anything actually changed.
//
// It compares against before rather than applying unconditionally, which
// is what keeps a converged run from reporting a change. chmod and chown
// both succeed on a no-op, so an unconditional apply is invisible on the
// device and very visible in a run report that says changed forever.
func Apply(ctx context.Context, conn *remoteexec.Conn, filePath string, want Attributes, before Info) (bool, error) {
	quoted := remoteexec.QuoteArg(filePath)
	var changed bool

	// OWNERSHIP FIRST, MODE SECOND, and the order is load-bearing rather
	// than stylistic. Linux clears the setuid and setgid bits on a regular
	// file whenever its owner or group changes, which is a deliberate
	// kernel protection: a setuid binary that changed hands would run as
	// its new owner. So chmod followed by chown throws away the special
	// bit chmod just set, and the task reports success having not achieved
	// what it was asked for, then reports changed again on every later run
	// because it never converges.
	//
	// Ansible orders these the same way (module_utils/basic.py's
	// set_fs_attributes_if_different) for exactly this reason. Directories
	// are unaffected, since the kernel skips the bit-clearing for them,
	// which is why this was invisible in the directory method and visible
	// in the ones that touch regular files.
	switch {
	case want.Owner != "" && want.Group != "":
		// One chown for both, because two calls would leave the file owned
		// by the new user and the old group if the second failed. The colon
		// form is POSIX.
		if want.Owner != before.Owner || want.Group != before.Group {
			spec := want.Owner + ":" + want.Group
			if err := run(ctx, conn, "chown "+remoteexec.QuoteArg(spec)+" "+quoted, "chown "+filePath); err != nil {
				return changed, err
			}
			changed = true
		}
	case want.Owner != "":
		if want.Owner != before.Owner {
			if err := run(ctx, conn, "chown "+remoteexec.QuoteArg(want.Owner)+" "+quoted, "chown "+filePath); err != nil {
				return changed, err
			}
			changed = true
		}
	case want.Group != "":
		if want.Group != before.Group {
			if err := run(ctx, conn, "chgrp "+remoteexec.QuoteArg(want.Group)+" "+quoted, "chgrp "+filePath); err != nil {
				return changed, err
			}
			changed = true
		}
	}

	// The mode is applied last, so a special bit survives the ownership
	// change above.
	//
	// The comparison is against the mode found BEFORE any of this ran,
	// which is correct even though a chown may have just cleared a bit:
	// clearing it makes the device differ from what was requested, and the
	// chmod below is what puts it back. Comparing against a re-read would
	// reach the same conclusion at the cost of a round trip.
	if want.Mode != "" && NormalizeMode(want.Mode) != before.Mode {
		if err := run(ctx, conn, "chmod "+remoteexec.QuoteArg(want.Mode)+" "+quoted, "chmod "+filePath); err != nil {
			return changed, err
		}
		changed = true
	}

	return changed, nil
}

// Remove deletes the path, recursively when it is a directory, and
// reports whether there was anything to delete.
//
// The recursive flag is the caller's decision rather than this function's
// because "remove this directory and everything under it" is the single
// most destructive thing in this namespace, and it should be visible at
// the call site and in the runbook rather than implied.
func Remove(ctx context.Context, conn *remoteexec.Conn, filePath string, recursive bool) error {
	flags := "-f"
	if recursive {
		flags = "-rf"
	}
	return run(ctx, conn, "rm "+flags+" "+remoteexec.QuoteArg(filePath), "remove "+filePath)
}

// MakeDirectory creates the directory, and its parents when parents is
// true. It succeeds when the directory already exists, which is mkdir -p's
// own behavior and the reason the caller reads state first rather than
// relying on this to tell it whether anything changed.
func MakeDirectory(ctx context.Context, conn *remoteexec.Conn, dir string, parents bool) error {
	cmd := "mkdir "
	if parents {
		cmd += "-p "
	}
	return run(ctx, conn, cmd+remoteexec.QuoteArg(dir), "create directory "+dir)
}

// Symlink creates or replaces a symbolic link at linkPath pointing at
// target.
//
// -n matters and is easy to leave out. Without it, `ln -sf target link`
// where link is an existing symlink to a DIRECTORY creates the new link
// INSIDE that directory rather than replacing the link, so the task
// reports success and the link still points where it did. With -n the
// existing link is treated as the thing being replaced.
func Symlink(ctx context.Context, conn *remoteexec.Conn, target, linkPath string) error {
	cmd := "ln -sfn " + remoteexec.QuoteArg(target) + " " + remoteexec.QuoteArg(linkPath)
	return run(ctx, conn, cmd, "link "+linkPath)
}

// Touch creates an empty file, or updates the modification time of one
// that exists, which is exactly touch's own behavior.
func Touch(ctx context.Context, conn *remoteexec.Conn, filePath string) error {
	return run(ctx, conn, "touch "+remoteexec.QuoteArg(filePath), "touch "+filePath)
}

// run sends one command and turns a non-zero exit into an error carrying
// what the device said, so every caller above reports failures the same
// way instead of each inventing its own message.
func run(ctx context.Context, conn *remoteexec.Conn, cmd, what string) error {
	result, err := conn.Run(ctx, cmd)
	if err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("%s: exited %d: %s", what, result.ExitCode, firstLine(result.Stderr))
	}
	return nil
}
