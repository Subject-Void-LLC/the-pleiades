// Package fs implements the two "fs.*" Collection methods: mount and
// unmount.
//
// # No new pkg/ primitive
//
// Mount state is read with findmnt and changed with mount/umount, both
// plain SSH commands through pkg/remoteexec, the same tier pkg.apt.* and
// identity.user.* already ship at. fstab persistence reuses
// pkg/remotefile's existing Read/Write/Apply/Stat rather than a second
// way to edit a text file: internal/catalog/file/line already
// established "read the whole file, decide in Go, write the whole file
// back" as this codebase's answer to editing a config file, specifically
// because sed's dialects disagree with each other and cannot report
// whether they changed anything. fstab is exactly that kind of file, so
// it gets the same treatment here rather than a "sed -i" of its own.
//
// # The fstab path is a parameter, not a constant
//
// Ansible's own mount module accepts an fstab parameter, defaulting to
// /etc/fstab, and it is reused here rather than hardcoded for the same
// two reasons it exists there: a test has no business writing the real
// file (this package's own tests point it at a temp path), and an
// operator managing an alternate table has a way to name it.
//
// # Deliberately out of scope for this pass
//
// A path already mounted with a different src, fstype or opts than
// requested is refused rather than silently remounted: unlike a
// package's version, a live mount's options cannot be reconciled in
// place without unmounting first, and doing that without being asked is
// not this method's call to make. Bind mounts, network filesystems
// needing credentials, and mount namespaces are not addressed either.
package fs

import (
	"context"
	"fmt"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/farside"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/operand"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remotefile"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// Parameter names. path/src/fstype/opts are ansible.builtin.mount's own
// names; fstab is also ansible.builtin.mount's own name, spelled the
// same way for the same reason. persist is this package's own addition:
// ansible.builtin.mount folds "mount now" and "in fstab" into a single
// state enum (mounted/unmounted/present/absent) with no state meaning
// "mount now but leave fstab alone", which this codebase's two-FQCN
// split needs a name for.
const (
	paramPath    = "path"
	paramSrc     = "src"
	paramFSType  = "fstype"
	paramOpts    = "opts"
	paramPersist = "persist"
	paramFstab   = "fstab"
)

const (
	defaultOpts  = "defaults"
	defaultFstab = "/etc/fstab"
)

// statPath is the one stat this namespace names directly, mirroring
// identity.user.*'s statName; the rest of what changed lives in the
// before/after maps sdk.Diff already records.
const statPath = "path"

// fsState is everything this namespace cares about for one mountpoint:
// whether it is currently mounted, what it is mounted from and with, and
// whether an fstab entry for it exists. Both queryMount's findmnt call
// and the fstab read that produces persisted/fstabLine happen every time
// this is built, regardless of the task's own persist param, because the
// recorded diff describes what the device actually has, not what the
// task asked for.
type fsState struct {
	mounted bool
	source  string
	fstype  string
	options string

	persisted bool
	fstabLine string
}

func (s fsState) Map() map[string]any {
	return map[string]any{
		"mounted":   s.mounted,
		"source":    s.source,
		"fstype":    s.fstype,
		"options":   s.options,
		"persisted": s.persisted,
	}
}

// queryState reads a mountpoint's live mount state and its fstab entry,
// if any, in one call, so both create and remove paths in this package
// build their before/after diff from exactly one place.
func queryState(ctx context.Context, conn *remoteexec.Conn, fstabPath, path string) (fsState, error) {
	mount, err := queryMount(ctx, conn, path)
	if err != nil {
		return fsState{}, err
	}
	lines, _, err := fstabReadLines(ctx, conn, fstabPath)
	if err != nil {
		return fsState{}, err
	}
	st := fsState{mounted: mount.mounted, source: mount.source, fstype: mount.fstype, options: mount.options}
	if idx := fstabFind(lines, path); idx >= 0 {
		st.persisted = true
		st.fstabLine = lines[idx]
	}
	return st, nil
}

// mountFacts is what findmnt reports about a live mountpoint.
type mountFacts struct {
	mounted bool
	source  string
	fstype  string
	options string
}

// findmntNothingFound is findmnt's exit status when it has nothing to
// show, and also its status for any error it reports.
const findmntNothingFound = 1

// queryMount asks findmnt whether path is itself a mountpoint (not
// merely underneath one), using --mountpoint for an exact match.
//
// findmnt has no exit status of its own for "not a mountpoint": it exits
// 1 when it has nothing to show and 1 for every error it reports (its
// manual: "1 on any error"). What tells them apart is that it says nothing
// when it finds nothing and explains an error on stderr (measured with
// util-linux 2.38 on Debian 12: exit 1 and no output at all for a
// directory that is not a mountpoint and for a path that does not exist;
// exit 1 and "unrecognized option" or "unknown column" on stderr for an
// error). So only exit 1 with nothing written reads as "not mounted".
// Reading every non-zero exit that way, as this did, read a findmnt that
// was not installed (127) or could not answer as "not mounted" and went on
// to mount (FAILURE_PATTERNS 1174). The test is the emptiness of stderr,
// not its words, so no locale changes it.
func queryMount(ctx context.Context, conn *remoteexec.Conn, path string) (mountFacts, error) {
	result, err := conn.Run(ctx, remoteexec.QuoteCommand([]string{"findmnt", "-no", "SOURCE,FSTYPE,OPTIONS", "--mountpoint", path}))
	if err != nil {
		return mountFacts{}, err
	}
	if result.ExitCode != 0 {
		if result.ExitCode == findmntNothingFound && strings.TrimSpace(result.Stdout) == "" && strings.TrimSpace(result.Stderr) == "" {
			return mountFacts{}, nil
		}
		return mountFacts{}, fmt.Errorf("findmnt --mountpoint %s exited %d: %s", path, result.ExitCode, failureDetail(result))
	}
	fields := strings.Fields(strings.TrimRight(result.Stdout, "\n"))
	if len(fields) < 3 {
		return mountFacts{}, fmt.Errorf("findmnt --mountpoint %s: unexpected output %s", path, farside.Output(result.Stdout))
	}
	return mountFacts{mounted: true, source: fields[0], fstype: fields[1], options: fields[2]}, nil
}

// runMount runs mount and treats a non-zero exit as a real error.
//
// The source and the mountpoint follow a "--", so mount reads each as what
// it is: without it, path -a made util-linux mount run mount -a, mounting
// every entry in fstab (FAILURE_PATTERNS 1174). util-linux 2.38 and
// BusyBox 1.36 (Alpine's mount) both end their options there (measured).
// The fields are also held to operand.MountField before the connection
// (readFields), so a value beginning with "-" never gets this far.
func runMount(ctx context.Context, conn *remoteexec.Conn, src, path, fstype, opts string) error {
	return runFsCmd(ctx, conn, []string{"mount", "-t", fstype, "-o", opts, "--", src, path})
}

// runUnmount runs umount on path, after a "--" for runMount's reason.
func runUnmount(ctx context.Context, conn *remoteexec.Conn, path string) error {
	return runFsCmd(ctx, conn, []string{"umount", "--", path})
}

// runFsCmd runs one mutating command and treats a non-zero exit as a real
// error.
func runFsCmd(ctx context.Context, conn *remoteexec.Conn, argv []string) error {
	command := remoteexec.QuoteCommand(argv)
	result, err := conn.Run(ctx, command)
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("%s exited %d: %s", command, result.ExitCode, failureDetail(result))
	}
	return nil
}

// failureDetail picks the stream an operator should read after a
// non-zero exit. Mirrors identity/user/user.go's own copy; mount, umount
// and findmnt almost always explain themselves on stderr.
func failureDetail(result remoteexec.Result) string {
	if detail := strings.TrimSpace(result.Stderr); detail != "" {
		return detail
	}
	if detail := strings.TrimSpace(result.Stdout); detail != "" {
		return detail
	}
	return "no output"
}

// fstabEntry is one line of an fstab, in the field order the file
// itself uses.
type fstabEntry struct {
	source, mountpoint, fstype, options string
}

// line renders the entry the way this package writes it: tab-separated,
// dump and pass both fixed at 0. Ansible's own mount module defaults
// dump and pass the same way when a task does not name them, and this
// namespace does not expose either as a parameter, so there is never a
// non-zero value to preserve.
func (e fstabEntry) line() string {
	return strings.Join([]string{e.source, e.mountpoint, e.fstype, e.options, "0", "0"}, "\t")
}

// check refuses an entry with a field fstab would not read back as that
// one field (operand.MountField): a space, a tab or a line break would add
// a field or a whole line to the file, so a source of
// "/dev/sdb1 /etc ext4 defaults 0 0\n/dev/sdc1" would mount something on
// /etc at the next boot. The methods refuse such a field before they
// connect; this holds for every caller of syncFstab and fstabWouldChange.
func (e fstabEntry) check() error {
	for _, field := range []struct{ key, value string }{
		{paramSrc, e.source}, {paramPath, e.mountpoint}, {paramFSType, e.fstype}, {paramOpts, e.options},
	} {
		if err := operand.MountField.Check(field.key, field.value); err != nil {
			return fmt.Errorf("writing an fstab entry: %w", err)
		}
		if field.value == "" {
			return fmt.Errorf("writing an fstab entry: %s is empty, which would leave the line short of a field", field.key)
		}
	}
	return nil
}

// fstabReadLines reads path's whole text and splits it into lines,
// reporting whether the file ended with a trailing newline the same way
// internal/catalog/file/line's lineSplit does, so a rewrite reproduces
// it exactly on a no-op.
func fstabReadLines(ctx context.Context, conn *remoteexec.Conn, path string) (lines []string, trailingNewline bool, err error) {
	info, err := remotefile.Stat(ctx, conn, path)
	if err != nil {
		return nil, false, err
	}
	if !info.Exists() {
		return nil, false, fmt.Errorf("%s does not exist", path)
	}
	content, _, err := remotefile.Read(ctx, conn, path)
	if err != nil {
		return nil, false, err
	}
	if content == "" {
		return nil, true, nil
	}
	trailingNewline = strings.HasSuffix(content, "\n")
	return strings.Split(strings.TrimSuffix(content, "\n"), "\n"), trailingNewline, nil
}

// fstabFind returns the index of the line in lines whose second
// whitespace-separated field (the mountpoint) equals mountpoint, or -1.
// A blank or comment line is skipped rather than mismatched against.
func fstabFind(lines []string, mountpoint string) int {
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		fields := strings.Fields(trimmed)
		if len(fields) >= 2 && fields[1] == mountpoint {
			return i
		}
	}
	return -1
}

// syncFstab makes sure fstabPath has exactly the entry given (when
// entry is non-nil) or no entry at all for mountpoint (when entry is
// nil), reading the whole file, deciding in Go, and writing the whole
// file back exactly once when something actually needs to change.
//
// It preserves the file's own mode, owner and group across the rewrite
// the same way internal/catalog/file/line.lineWrite does and for the
// identical reason: remotefile.Write replaces the path by renaming a
// freshly created temporary over it, and that temporary is 0600 owned
// by the connecting account, so a rewrite that skipped this would leave
// /etc/fstab unreadable by everyone else the moment this method touched
// it.
func syncFstab(ctx context.Context, conn *remoteexec.Conn, fstabPath, mountpoint string, entry *fstabEntry) (changed bool, err error) {
	if entry != nil {
		if err := entry.check(); err != nil {
			return false, err
		}
	}
	// info is captured here, before fstabReadLines below, purely for the
	// mode/owner/group Apply restores after a real write; fstabReadLines
	// is what actually enforces that fstabPath exists, so this does not
	// duplicate that check.
	info, err := remotefile.Stat(ctx, conn, fstabPath)
	if err != nil {
		return false, err
	}
	lines, trailingNewline, err := fstabReadLines(ctx, conn, fstabPath)
	if err != nil {
		return false, err
	}

	newLines, changes := fstabDecide(lines, mountpoint, entry)
	if !changes {
		return false, nil
	}

	newContent := strings.Join(newLines, "\n")
	if len(newLines) > 0 && trailingNewline {
		newContent += "\n"
	}

	// Put keeps fstab's mode, owner and group, and a write it cannot finish
	// leaves fstab as it was.
	put, err := remotefile.Put(ctx, conn, fstabPath, []byte(newContent), info, remotefile.Attributes{})
	if err != nil {
		return false, put.Failure(err, "fstab may already hold the new text, so fix the cause and re-run rather than expecting it untouched")
	}
	return true, nil
}

// fstabDecide is syncFstab's whole decision: the lines lines would become
// with exactly entry (or, when entry is nil, no entry) for mountpoint, and
// whether that differs from lines at all. A check asks it through
// fstabWouldChange, so the two cannot disagree about when fstab is
// rewritten.
func fstabDecide(lines []string, mountpoint string, entry *fstabEntry) ([]string, bool) {
	idx := fstabFind(lines, mountpoint)
	switch {
	case entry == nil && idx < 0:
		return lines, false
	case entry == nil:
		return append(append([]string{}, lines[:idx]...), lines[idx+1:]...), true
	case idx < 0:
		return append(append([]string{}, lines...), entry.line()), true
	case lines[idx] == entry.line():
		return lines, false
	default:
		newLines := append([]string{}, lines...)
		newLines[idx] = entry.line()
		return newLines, true
	}
}

// fstabWouldChange reads fstabPath and reports whether syncFstab would
// rewrite it for mountpoint and entry, writing nothing.
func fstabWouldChange(ctx context.Context, conn *remoteexec.Conn, fstabPath, mountpoint string, entry *fstabEntry) (bool, error) {
	if entry != nil {
		if err := entry.check(); err != nil {
			return false, err
		}
	}
	lines, _, err := fstabReadLines(ctx, conn, fstabPath)
	if err != nil {
		return false, err
	}
	_, changes := fstabDecide(lines, mountpoint, entry)
	return changes, nil
}

// predictState is a check's report for a mountpoint found as before: the
// same path stat and diff a real run records, with predicted as the
// after half when a real run would change something, and nothing undone,
// since nothing was done.
func predictState(rc sdk.RunbookContext, fqcn, path string, before fsState, changed bool, predicted map[string]any) (collection.Result, error) {
	after := before.Map()
	if changed {
		after = predicted
	}
	if err := rc.SetStat(statPath, path); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if err := sdk.RecordDiff(rc, sdk.Diff{Before: before.Map(), After: after}); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	return collection.Result{Changed: changed}, nil
}

// readField reads one required mount field, held to operand.MountField, the
// rule its parameter declares (collection.ParamFormatMountField), before
// anything connects: mount, umount and findmnt would read a value beginning
// with "-" as an option (path -a made mount run mount -a), and fstab would
// read a space or a line break in it as another field or line.
func readField(params map[string]any, key string) (string, error) {
	return sdk.RequiredOperandParamOf(params, key, operand.MountField)
}

// fstabParam reads the fstab path parameter, defaulting to /etc/fstab.
func fstabParam(params map[string]any) string {
	if v := sdk.StringParam(params, paramFstab); v != "" {
		return v
	}
	return defaultFstab
}

// recordState writes the mountpoint path and the before/after diff, the
// two things both methods in this namespace report regardless of which
// one ran.
func recordState(rc sdk.RunbookContext, path string, before, after fsState) error {
	if err := rc.SetStat(statPath, path); err != nil {
		return err
	}
	return sdk.RecordDiff(rc, sdk.Diff{Before: before.Map(), After: after.Map()})
}
