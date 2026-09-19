// Package archive implements the two "archive.*" Collection methods:
// create and extract.
//
// # No new pkg/ primitive
//
// Both talk to the target entirely through pkg/remoteexec (via
// sdk.Connect) and pkg/remotefile (for the destination's before/after
// state), the same tier pkg.apt.* and identity.user.* already ship at.
// The archive itself is built and read with tar; nothing here needed a
// shared primitive of its own.
//
// # Only tar, not zip
//
// community.general.archive/unarchive support zip as well as tar, but
// zip and unzip are not guaranteed present on a target the way tar is
// (tar is POSIX; zip is not on every minimal image this platform is
// likely to meet). Rather than depend on a binary that might not be
// there, or silently fall back and surprise somebody, this namespace
// only speaks tar this pass. archive.create's format param says so, and
// a zip archive is refused by name rather than attempted and failing
// with "command not found".
//
// # extract has no format param
//
// GNU tar auto-detects gzip compression on extraction regardless of
// whether -z is given, so archive.extract always runs plain "tar -xf"
// and lets tar decide. archive.create has no such luxury: creating an
// archive means choosing whether to compress it, so its own format
// param exists where extract's does not.
//
// # archive.extract's src is already on the device
//
// A forge scaffold first declared archive.extract against
// FileTransferCapable, implying a src transferred from the control
// node. Nothing in this codebase can do that:
// internal/catalog/file/copy.go explicitly refuses a src parameter for
// exactly this reason ("content only, never src"). Rather than invent a
// transfer primitive this pass, archive.extract is scoped to a src
// archive already present on the device (Ansible's own remote_src: true
// shape), and its capability is POSIXFileSystemCapable, matching
// archive.create.
//
// # Idempotency, stated plainly rather than attempted in full
//
// archive.create is idempotent on path already existing: it does not
// compare src against the archive's contents, only whether something is
// already there. archive.extract has no content to compare against
// without unpacking first, so it reuses Ansible's own creates idiom
// (shared by exec.command's own params, which is where this platform
// already borrowed it from) rather than inventing new vocabulary: name
// a path, and a run that finds it already there skips extracting.
// Naming no creates means every run re-extracts, the same honesty
// exec.command already has for a command with no built-in idempotency.
package archive

import (
	"context"
	"fmt"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remotefile"
)

// Archive formats archive.create can produce. Ansible's own archive
// module spells the compressed form "gz"; this spells it "tar.gz" since
// that is also the file extension a person actually names, and accepts
// "tgz" as the common shorthand.
const (
	formatTar   = "tar"
	formatTarGz = "tar.gz"
)

// normalizeFormat validates and canonicalizes the format param, refusing
// anything this namespace does not speak (most notably zip; see this
// package's own doc comment) before a connection is ever made.
func normalizeFormat(format string) (string, error) {
	switch format {
	case "", formatTarGz, "tgz":
		return formatTarGz, nil
	case formatTar:
		return formatTar, nil
	default:
		return "", fmt.Errorf("format %q is not supported: use %q or %q", format, formatTar, formatTarGz)
	}
}

// tarCreateArgs builds the tar invocation that writes path from src,
// compressed when format is tar.gz.
func tarCreateArgs(path string, src []string, format string) []string {
	flag := "-czf"
	if format == formatTar {
		flag = "-cf"
	}
	args := append([]string{"tar", flag, path}, src...)
	return args
}

// tarExtractArgs builds the tar invocation that unpacks src into dest.
// No compression flag: GNU tar auto-detects it on extraction regardless.
func tarExtractArgs(src, dest string) []string {
	return []string{"tar", "-xf", src, "-C", dest}
}

// runArchiveCmd runs one tar (or rm) invocation and treats a non-zero
// exit as a real error.
func runArchiveCmd(ctx context.Context, conn *remoteexec.Conn, argv []string) error {
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
// non-zero exit. Mirrors internal/catalog/fs's own copy; tar and rm
// almost always explain themselves on stderr.
func failureDetail(result remoteexec.Result) string {
	if detail := strings.TrimSpace(result.Stderr); detail != "" {
		return detail
	}
	if detail := strings.TrimSpace(result.Stdout); detail != "" {
		return detail
	}
	return "no output"
}

// removePaths deletes each of paths with rm -rf, for the remove param
// both methods share: archive.create's own meaning ("delete the sources
// once they are safely archived") and archive.extract's own meaning
// ("delete the archive once it is safely unpacked") are different
// English sentences over the same one mechanism.
func removePaths(ctx context.Context, conn *remoteexec.Conn, paths ...string) error {
	return runArchiveCmd(ctx, conn, append([]string{"rm", "-rf"}, paths...))
}

// needExisting is a check's reading of a path a real run's command needs:
// nil when path exists (and, when kind is set, is that kind), and
// otherwise the answer that this call cannot be checked, naming what is
// missing and why a check cannot settle it. what names the path's role,
// such as "src".
//
// Missing is not failure here. The real run's tar or mkdir would fail on
// it only if nothing earlier in the same run created it, and a check,
// which creates nothing, cannot tell whether something would have.
func needExisting(ctx context.Context, conn *remoteexec.Conn, path, what string, kind remotefile.Kind) error {
	info, err := remotefile.Stat(ctx, conn, path)
	if err != nil {
		return err
	}
	switch {
	case !info.Exists():
		return collection.CannotCheck(fmt.Sprintf("%s %s does not exist yet; a real run fails on it unless an earlier "+
			"task creates it, which a check cannot tell", what, path))
	case kind != "" && info.Kind != kind:
		return collection.CannotCheck(fmt.Sprintf("%s %s is a %s, not a %s; a real run fails on it unless an earlier "+
			"task replaces it, which a check cannot tell", what, path, info.Kind, kind))
	}
	return nil
}
