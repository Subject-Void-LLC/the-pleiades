// Package remotefile: replacing a file's content while it stays what it
// was, its owner, group and mode included.
package remotefile

import (
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
)

// Replaced says how Replace wrote a file.
type Replaced struct {
	// InPlace is true when the new bytes were copied into the existing
	// file rather than renamed over it, because a new file could not be
	// given the old one's owner and group by this login.
	InPlace bool

	// Touched is true once the file's bytes may differ from what they
	// were, which an error after that point does not undo: a caller that
	// must put a file back keeps it on its list whenever Touched is set.
	Touched bool
}

// Replace writes content over the regular file at filePath and leaves the
// file what it was: the mode, owner and group before (as Stat found them)
// records.
//
// Write renames a new file over the path, and a new file belongs to the
// login that made it, so a file whose owner or group the login cannot
// give away (a project file owned by root's group, edited by the account
// that deploys it) would come back owned by someone else, and chown would
// then be refused. Replace puts the whole new content beside the file
// first, gives it the old mode and, where the login may set them, the old
// owner and group, and renames it over the file only when it then matches;
// otherwise it copies the bytes into the existing file, which keeps
// everything the file was (its owner, group, mode, links and labels) at
// the cost of the rename's atomicity: the copy runs on the device from a
// file already complete there, so a dropped session cannot leave half of
// it, but a reader at that instant could see the file part written.
//
// An error with Touched unset means the file was not changed.
func Replace(ctx context.Context, conn *remoteexec.Conn, filePath string, content []byte, before Info) (Replaced, error) {
	if before.Kind != KindFile {
		return Replaced{}, fmt.Errorf("replace %s: it is a %s, not a regular file", filePath, before.Kind)
	}
	dir := path.Dir(filePath)
	res, err := conn.RunWithStdin(ctx, "set -e; tmp=$(mktemp "+remoteexec.QuoteArg(dir)+"/.pleiades.XXXXXX); cat > \"$tmp\"; printf '%s' \"$tmp\"", strings.NewReader(string(content)))
	if err != nil {
		return Replaced{}, fmt.Errorf("replace %s: %w", filePath, err)
	}
	tmp := res.Stdout
	if res.ExitCode != 0 || !strings.HasPrefix(tmp, dir+"/.pleiades.") || strings.ContainsAny(tmp, "\n\r") {
		return Replaced{}, fmt.Errorf("replace %s: writing beside it: exited %d: %s", filePath, res.ExitCode, firstLine(res.Stderr))
	}
	cleanup := func() { _, _ = conn.Run(ctx, "rm -f -- "+remoteexec.QuoteArg(tmp)) }

	if err := settle(ctx, conn, tmp, before); err != nil {
		cleanup()
		return Replaced{}, fmt.Errorf("replace %s: %w", filePath, err)
	}
	now, err := Stat(ctx, conn, tmp)
	if err != nil {
		cleanup()
		return Replaced{}, fmt.Errorf("replace %s: %w", filePath, err)
	}
	if now.Owner == before.Owner && now.Group == before.Group && NormalizeMode(now.Mode) == NormalizeMode(before.Mode) {
		if err := run(ctx, conn, "mv -f -- "+remoteexec.QuoteArg(tmp)+" "+remoteexec.QuoteArg(filePath), "rename over it"); err != nil {
			cleanup()
			return Replaced{}, fmt.Errorf("replace %s: %w", filePath, err)
		}
		return Replaced{Touched: true}, nil
	}

	// The test is apart from the copy so a file this login may not write
	// is refused before the shell truncates anything.
	quoted := remoteexec.QuoteArg(filePath)
	if err := run(ctx, conn, "[ -w "+quoted+" ]", "test write permission"); err != nil {
		cleanup()
		return Replaced{}, fmt.Errorf("replace %s: a new file would belong to %s:%s rather than %s:%s, and this login may not write the file itself", filePath, now.Owner, now.Group, before.Owner, before.Group)
	}
	err = run(ctx, conn, "cat -- "+remoteexec.QuoteArg(tmp)+" > "+quoted, "copy")
	cleanup()
	if err != nil {
		return Replaced{InPlace: true, Touched: true}, fmt.Errorf("replace %s in place: %w", filePath, err)
	}
	return Replaced{InPlace: true, Touched: true}, nil
}

// settle gives tmp before's mode, and its owner and group as far as this
// login may: chgrp to a group the login is in, and chown only as root.
// A refused chown or chgrp is not an error here; Replace reads what it
// got and copies in place when that is not before.
func settle(ctx context.Context, conn *remoteexec.Conn, tmp string, before Info) error {
	quoted := remoteexec.QuoteArg(tmp)
	if err := run(ctx, conn, "chmod "+remoteexec.QuoteArg(NormalizeMode(before.Mode))+" "+quoted, "chmod"); err != nil {
		return err
	}
	now, err := Stat(ctx, conn, tmp)
	if err != nil {
		return err
	}
	switch {
	case now.Owner != before.Owner:
		_, err = conn.Run(ctx, "chown -- "+remoteexec.QuoteArg(before.Owner+":"+before.Group)+" "+quoted+" 2>/dev/null")
	case now.Group != before.Group:
		_, err = conn.Run(ctx, "chgrp -- "+remoteexec.QuoteArg(before.Group)+" "+quoted+" 2>/dev/null")
	}
	if err != nil {
		return err
	}
	// chown and chgrp may clear setuid and setgid, so the mode goes on
	// again once they have run.
	return run(ctx, conn, "chmod "+remoteexec.QuoteArg(NormalizeMode(before.Mode))+" "+quoted, "chmod")
}
