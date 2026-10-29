// Checkpoints: a copy of a device's running configuration kept on the
// device, which IOS's configure replace returns to exactly.
package ios

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/iosconfig"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcli"
)

// runningConfig is the file system path IOS names the running
// configuration by, for show archive config differences.
const runningConfig = "system:running-config"

// checkpointPath is what a checkpoint path must look like: a file system
// prefix and a file name of plain characters, since it is written into
// commands the device runs.
var checkpointPath = regexp.MustCompile(`^[a-z][a-z0-9]*:[A-Za-z0-9._/-]+$`)

// mintedCheckpoint is the name newCheckpointNames gives a checkpoint, and
// the only kind of file net.ios.replace deletes: a replace pointed at
// nvram:startup-config or an image on flash: must never erase it.
var mintedCheckpoint = regexp.MustCompile(`^[a-z][a-z0-9]*:pleiades-[0-9a-f]{12}(-after)?\.cfg$`)

// errNoCheckpoint is what checkpointFileSystem and takeCheckpoint return
// when the device has nowhere to keep a checkpoint, or no configure
// replace to return to one with, so net.ios.config falls back to undo
// lines written from two reads of the configuration.
var errNoCheckpoint = errors.New("the device cannot keep a checkpoint")

// checkpointFileSystem returns the file system prefix a checkpoint is kept
// on: the device's default file system when it is a writable disk or
// flash, else the first such one show file systems lists.
func checkpointFileSystem(ctx context.Context, session iosSession) (string, error) {
	out, err := session.Command(ctx, "show file systems")
	if err != nil {
		return "", err
	}
	if netcli.IOS.ErrorPattern.MatchString(out) {
		return "", fmt.Errorf("%w: show file systems: %s", errNoCheckpoint, deviceRefusal(out))
	}
	return pickFileSystem(out)
}

// pickFileSystem reads show file systems' table: a row marked * is the
// default; each row's type, flags and prefixes are its last columns.
func pickFileSystem(out string) (string, error) {
	first := ""
	for line := range strings.SplitSeq(out, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		def := len(fields) > 0 && fields[0] == "*"
		if def {
			fields = fields[1:]
		}
		// Size, free, type, flags, then one or more prefixes; a network
		// or opaque file system shows "-" for its sizes.
		if len(fields) < 5 || (fields[2] != "disk" && fields[2] != "flash") || !strings.Contains(fields[3], "rw") {
			continue
		}
		prefix := fields[4]
		if !checkpointPath.MatchString(prefix+"x") || !strings.HasSuffix(prefix, ":") {
			continue
		}
		if def {
			return prefix, nil
		}
		if first == "" {
			first = prefix
		}
	}
	if first == "" {
		return "", fmt.Errorf("%w: no writable disk or flash file system", errNoCheckpoint)
	}
	return first, nil
}

// newCheckpointNames returns the paths of a change's two checkpoints on
// fileSystem: the configuration before it and after it.
func newCheckpointNames(fileSystem string) (before, after string, err error) {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	id := hex.EncodeToString(b)
	return fileSystem + "pleiades-" + id + ".cfg", fileSystem + "pleiades-" + id + "-after.cfg", nil
}

// destinationQuestion is the question copy asks before writing a file.
var destinationQuestion = regexp.MustCompile(`Destination filename \[[^\]]*\]\?`)

// overwriteQuestion is the question copy asks when the file is there
// already; a checkpoint never overwrites one.
var overwriteQuestion = regexp.MustCompile(`(?i)over ?write\?`)

// saveRunning copies the running configuration to path, refusing to
// overwrite a file already there.
func saveRunning(ctx context.Context, session iosSession, path string) error {
	res, err := session.Dialog(ctx, "copy running-config "+path, netcli.DialogOptions{Prompts: []netcli.Prompt{
		{Match: destinationQuestion, Answer: ""},
		{Match: overwriteQuestion, Refuse: true},
	}})
	if err != nil {
		return fmt.Errorf("saving the running configuration to %s: %w", path, err)
	}
	if netcli.IOS.ErrorPattern.MatchString(res.Output) {
		return fmt.Errorf("saving the running configuration to %s: %s", path, deviceRefusal(res.Output))
	}
	return nil
}

// differences returns the device's own contextual differences from the
// configuration in from to the one in to, masked, nil when there are none.
func differences(ctx context.Context, session iosSession, from, to string) ([]string, error) {
	out, err := session.Command(ctx, "show archive config differences "+from+" "+to)
	if err != nil {
		return nil, err
	}
	if netcli.IOS.ErrorPattern.MatchString(out) {
		return nil, fmt.Errorf("comparing %s with %s: %s", from, to, deviceRefusal(out))
	}
	lines, err := iosconfig.Differences(out)
	if err != nil {
		return nil, fmt.Errorf("comparing %s with %s: %w", from, to, err)
	}
	return maskDifferences(lines), nil
}

// maskDifferences masks the secrets in difference lines, through the same
// masking net.ios.parse gives show archive config differences.
func maskDifferences(lines []string) []string {
	if len(lines) == 0 {
		return lines
	}
	return strings.Split(maskDiff(strings.Join(lines, "\n")), "\n")
}

// deleteFile deletes path on the device without asking.
func deleteFile(ctx context.Context, session iosSession, path string) error {
	out, err := session.Command(ctx, "delete /force "+path)
	if err != nil {
		return fmt.Errorf("deleting %s: %w", path, err)
	}
	if netcli.IOS.ErrorPattern.MatchString(out) {
		return fmt.Errorf("deleting %s: %s", path, deviceRefusal(out))
	}
	return nil
}

// takeCheckpoint saves the running configuration to a new checkpoint and
// proves the device can compare against it, which is what configure
// replace needs too. A device that cannot is errNoCheckpoint, the file
// it may have written deleted.
func takeCheckpoint(ctx context.Context, session iosSession) (before, after string, err error) {
	fileSystem, err := checkpointFileSystem(ctx, session)
	if err != nil {
		return "", "", err
	}
	before, after, err = newCheckpointNames(fileSystem)
	if err != nil {
		return "", "", err
	}
	if err := saveRunning(ctx, session, before); err != nil {
		return "", "", fmt.Errorf("%w: %w", errNoCheckpoint, err)
	}
	if _, err := differences(ctx, session, before, runningConfig); err != nil {
		_ = deleteFile(ctx, session, before)
		return "", "", fmt.Errorf("%w: %w", errNoCheckpoint, err)
	}
	return before, after, nil
}
