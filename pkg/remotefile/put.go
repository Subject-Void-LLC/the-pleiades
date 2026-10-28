// Package remotefile: writing a file that ends up with the attributes a
// task asks for, or is not written at all.
package remotefile

import (
	"context"
	"fmt"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/farside"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
)

// AttributeRefusal is Put's answer when this login may not give the new
// file an owner or group the write needs. Nothing was written.
type AttributeRefusal struct {
	// Path is the file the write was for.
	Path string
	// Login is the account the session runs as, the owner a file it makes
	// starts with.
	Login string
	// Field is "owner" or "group".
	Field string
	// Value is the owner or group the file needed.
	Value string
}

// Error says what was refused and that nothing changed.
func (e *AttributeRefusal) Error() string {
	return fmt.Sprintf("put %s: this login (%s) may not give a file the %s %s, so nothing was written", e.Path, e.Login, e.Field, e.Value)
}

// Put writes content at filePath and leaves the file with want's
// attributes: every field want names, and, for a file that exists, the
// mode, owner and group before records for every field it leaves empty.
// before is what Stat found at filePath: absent, or a regular file.
//
// It never writes a file it cannot finish. A write that keeps an existing
// file's attributes is Replace's, which renames when this login can give a
// new file the old owner and group and otherwise copies the bytes into the
// existing file. Any other write (a new file, or one whose attributes the
// task changes) gives the new file its attributes beside the target first
// and renames it only when they all took: when this login may not give it
// the owner or group (a non-root login, or a group it is not in), the
// answer is an *AttributeRefusal and the target is as it was. The rename
// that used to come first left the new bytes owned by the login at mode
// 0600 whenever a chown after it was refused.
//
// An error with Touched unset means the file was not changed.
func Put(ctx context.Context, conn *remoteexec.Conn, filePath string, content []byte, before Info, want Attributes) (Replaced, error) {
	if before.Exists() && before.Kind != KindFile {
		return Replaced{}, fmt.Errorf("put %s: it is a %s, not a regular file", filePath, before.Kind)
	}
	target := putTarget(want, before)
	if before.Kind == KindFile && sameAttributes(target, before) {
		return Replace(ctx, conn, filePath, content, before)
	}

	tmp, cleanup, err := writeBeside(ctx, conn, filePath, content)
	if err != nil {
		if missing := missingDir(ctx, conn, filePath); missing != nil {
			return Replaced{}, missing
		}
		return Replaced{}, fmt.Errorf("put %s: %w", filePath, err)
	}
	made, err := Stat(ctx, conn, tmp)
	if err != nil {
		cleanup()
		return Replaced{}, fmt.Errorf("put %s: %w", filePath, err)
	}
	if _, err := Apply(ctx, conn, tmp, target, made); err != nil {
		cleanup()
		if refusal := putRefusal(filePath, made.Owner, target, made); refusal != nil {
			return Replaced{}, refusal
		}
		return Replaced{}, fmt.Errorf("put %s: %w", filePath, err)
	}
	got, err := Stat(ctx, conn, tmp)
	if err != nil {
		cleanup()
		return Replaced{}, fmt.Errorf("put %s: %w", filePath, err)
	}
	if refusal := putRefusal(filePath, made.Owner, target, got); refusal != nil {
		cleanup()
		return Replaced{}, refusal
	}
	if target.Mode != "" && NormalizeMode(got.Mode) != NormalizeMode(target.Mode) {
		cleanup()
		return Replaced{}, fmt.Errorf("put %s: the new file has mode %s, not %s, so nothing was written", filePath, got.Mode, target.Mode)
	}
	if err := run(ctx, conn, "mv -f -- "+remoteexec.QuoteArg(tmp)+" "+remoteexec.QuoteArg(filePath), "rename over it"); err != nil {
		cleanup()
		return Replaced{}, fmt.Errorf("put %s: %w", filePath, err)
	}
	return Replaced{Touched: true}, nil
}

// missingDir names filePath's directory as missing when it does not exist,
// and is nil otherwise (including when it cannot tell). A write into a
// directory that is not there used to be reported as one this login may
// not write, which sent the reader after permissions; a check meets it
// whenever an earlier task in the same check would have made the directory.
func missingDir(ctx context.Context, conn *remoteexec.Conn, filePath string) error {
	dir := path.Dir(filePath)
	info, err := Stat(ctx, conn, dir)
	if err != nil || info.Exists() {
		return nil
	}
	return fmt.Errorf("put %s: its directory %s does not exist, so nothing was written", filePath, dir)
}

// PredictPut returns the refusal Put would make for the same arguments,
// or nil when Put would write, for a check that must refuse what a real
// run refuses. It only reads: the login's identity and whether it may
// write the directory and the file.
func PredictPut(ctx context.Context, conn *remoteexec.Conn, filePath string, before Info, want Attributes) error {
	if before.Exists() && before.Kind != KindFile {
		return fmt.Errorf("put %s: it is a %s, not a regular file", filePath, before.Kind)
	}
	who, err := whoAmI(ctx, conn, filePath)
	if err != nil {
		return fmt.Errorf("put %s: %w", filePath, err)
	}
	if !who.dirWritable {
		if err := missingDir(ctx, conn, filePath); err != nil {
			return err
		}
		return fmt.Errorf("put %s: writing beside it: this login (%s) may not write %s", filePath, who.login, path.Dir(filePath))
	}
	if who.root {
		return nil
	}
	dir, err := Stat(ctx, conn, path.Dir(filePath))
	if err != nil {
		return fmt.Errorf("put %s: %w", filePath, err)
	}
	newGroup := who.newFileGroup(dir)
	canGive := func(group string) bool { return group == newGroup || slices.Contains(who.groups, group) }
	target := putTarget(want, before)
	if before.Kind == KindFile && sameAttributes(target, before) {
		if target.Owner == who.login && canGive(target.Group) {
			return nil
		}
		if !who.fileWritable {
			return fmt.Errorf("replace %s: a new file would belong to %s rather than %s:%s, and this login may not write the file itself", filePath, who.login, before.Owner, before.Group)
		}
		return nil
	}
	switch {
	case target.Owner != "" && target.Owner != who.login:
		return &AttributeRefusal{Path: filePath, Login: who.login, Field: "owner", Value: target.Owner}
	case target.Group != "" && !canGive(target.Group):
		return &AttributeRefusal{Path: filePath, Login: who.login, Field: "group", Value: target.Group}
	}
	return nil
}

// putTarget is the attributes Put leaves: want, with an existing file's
// own values for every field want leaves empty.
func putTarget(want Attributes, before Info) Attributes {
	target := want
	if before.Kind != KindFile {
		return target
	}
	if target.Mode == "" {
		target.Mode = before.Mode
	}
	if target.Owner == "" {
		target.Owner = before.Owner
	}
	if target.Group == "" {
		target.Group = before.Group
	}
	return target
}

// sameAttributes reports whether target asks for exactly what before has.
func sameAttributes(target Attributes, before Info) bool {
	return NormalizeMode(target.Mode) == NormalizeMode(before.Mode) && target.Owner == before.Owner && target.Group == before.Group
}

// putRefusal names the owner or group target asks for that got lacks, or
// returns nil when it has both.
func putRefusal(filePath, login string, target Attributes, got Info) error {
	switch {
	case target.Owner != "" && got.Owner != target.Owner:
		return &AttributeRefusal{Path: filePath, Login: login, Field: "owner", Value: target.Owner}
	case target.Group != "" && got.Group != target.Group:
		return &AttributeRefusal{Path: filePath, Login: login, Field: "group", Value: target.Group}
	}
	return nil
}

// identity is who a session runs as, and what it may write near a path.
type identity struct {
	login        string
	groups       []string
	root         bool
	dirWritable  bool
	fileWritable bool
	// kernel is uname -s, and primaryGroup the login's own group: together
	// with the directory they decide the group a new file starts with.
	kernel       string
	primaryGroup string
}

// newFileGroup is the group a file this login makes in dir starts with,
// which Put's file beside the target keeps without a chgrp: dir's own on
// FreeBSD and macOS, and on Linux dir's own when dir is setgid, else the
// login's. On bsd-check a mode change in a wheel directory was refused
// here as a group the login could not give, while Put wrote it
// (2026-10-23).
func (w identity) newFileGroup(dir Info) string {
	if w.kernel != "Linux" {
		return dir.Group
	}
	if m, err := strconv.ParseUint(NormalizeMode(dir.Mode), 8, 32); err == nil && m&0o2000 != 0 {
		return dir.Group
	}
	return w.primaryGroup
}

// whoAmI reads the session's login, its groups, whether it is root, and
// whether it may write filePath's directory and filePath itself.
func whoAmI(ctx context.Context, conn *remoteexec.Conn, filePath string) (identity, error) {
	dir := remoteexec.QuoteArg(path.Dir(filePath))
	file := remoteexec.QuoteArg(filePath)
	cmd := "id -u; id -un; id -Gn; " +
		"if [ -w " + dir + " ]; then echo yes; else echo no; fi; " +
		"if [ -w " + file + " ]; then echo yes; else echo no; fi; " +
		"uname -s; id -gn"
	res, err := conn.Run(ctx, cmd)
	if err != nil {
		return identity{}, fmt.Errorf("reading this login: %w", err)
	}
	lines := strings.Split(strings.TrimRight(res.Stdout, "\n"), "\n")
	if res.ExitCode != 0 || len(lines) != 7 {
		return identity{}, fmt.Errorf("reading this login: exited %d: %s", res.ExitCode, farside.Output(res.Stderr))
	}
	return identity{
		root:         strings.TrimSpace(lines[0]) == "0",
		login:        strings.TrimSpace(lines[1]),
		groups:       strings.Fields(lines[2]),
		dirWritable:  lines[3] == "yes",
		fileWritable: lines[4] == "yes",
		kernel:       strings.TrimSpace(lines[5]),
		primaryGroup: strings.TrimSpace(lines[6]),
	}, nil
}
