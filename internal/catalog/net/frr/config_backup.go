// Package frr: the backups net.frr.config keeps on the router, and how a
// restore reads one back.
package frr

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remotefile"
)

// backupDir returns the absolute path of the connecting account's backup
// directory, made (0700) unless this is a check.
func (s *session) backupDir(ctx context.Context, mode collection.Mode) (string, error) {
	script := `printf '%s' "$HOME/` + backupDir + `"`
	if mode != collection.ModeCheck {
		script = `umask 077 && mkdir -p "$HOME/` + backupDir + `" && ` + script
	}
	out, err := s.run(ctx, "sh", "-c", script)
	if err != nil {
		return "", err
	}
	dir := strings.TrimSpace(out)
	if !path.IsAbs(dir) || path.Clean(dir) != dir || path.Base(dir) != backupDir {
		return "", fmt.Errorf("%s: the backup directory %q is not an absolute path ending in %s", configFQCN, dir, backupDir)
	}
	return dir, nil
}

// writeBackup saves the running configuration, and frr.conf as it was
// when withConf, to a new backup in dir, and returns its path.
func (s *session) writeBackup(ctx context.Context, dir, running, conf string, withConf bool) (string, error) {
	suffix := make([]byte, 4)
	if _, err := rand.Read(suffix); err != nil {
		return "", err
	}
	name := fmt.Sprintf("pleiades-%s-%s.conf", time.Now().UTC().Format("20060102T150405Z"), hex.EncodeToString(suffix))
	backup := path.Join(dir, name)
	if err := s.writePrivate(ctx, backup, running); err != nil {
		return "", err
	}
	if withConf {
		if err := s.writePrivate(ctx, backup+savedSuffix, conf); err != nil {
			return "", err
		}
	}
	return backup, nil
}

// writePrivate writes content to a new file at p readable by the
// connecting account alone, never over one already there.
func (s *session) writePrivate(ctx context.Context, p, content string) error {
	cmd := remoteexec.QuoteCommand([]string{"sh", "-c", `umask 077 && set -C && cat > "$1"`, "sh", p})
	res, err := s.conn.RunWithStdin(ctx, cmd, strings.NewReader(content))
	if err != nil {
		return fmt.Errorf("%s: writing %s: %w", configFQCN, p, err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("%s: writing %s: exited %d: %s", configFQCN, p, res.ExitCode, firstLine(res.Stderr))
	}
	return nil
}

// readBackup reads the backup a restore names, which must be one this
// method made in dir, and refuses when the router no longer runs what the
// change left, unless force. It returns the configuration to restore and
// frr.conf as it was, when the change saved one.
func (s *session) readBackup(ctx context.Context, dir, backup string, force bool) (text, saved string, hasSaved bool, err error) {
	if path.Dir(backup) != dir || !backupName.MatchString(path.Base(backup)) {
		return "", "", false, fmt.Errorf("%s: restore must name a backup this method made, in %s, got %q", configFQCN, dir, backup)
	}
	read := func(p string, need bool) (string, bool, error) {
		info, err := remotefile.Stat(ctx, s.conn, p)
		if err != nil {
			return "", false, fmt.Errorf("%s: reading %s: %w", configFQCN, p, err)
		}
		if !info.Exists() && !need {
			return "", false, nil
		}
		b, info, err := remotefile.ReadText(ctx, s.conn, p, maxConfig)
		if err != nil {
			return "", false, fmt.Errorf("%s: reading %s: %w", configFQCN, p, err)
		}
		if info.Mode != "0600" {
			return "", false, fmt.Errorf("%s: %s has mode %s, not the 0600 this method gives a backup, so it is not trusted", configFQCN, p, info.Mode)
		}
		return string(b), true, nil
	}
	if text, _, err = read(backup, true); err != nil {
		return "", "", false, err
	}
	if saved, hasSaved, err = read(backup+savedSuffix, false); err != nil {
		return "", "", false, err
	}
	if force {
		return text, saved, hasSaved, nil
	}
	after, ok, err := read(backup+afterSuffix, false)
	if err != nil {
		return "", "", false, err
	}
	if !ok {
		return "", "", false, fmt.Errorf("%s: there is no record of what the change saved at %s left, so a change since cannot be ruled out; set force to restore anyway", configFQCN, backup)
	}
	running, err := s.runningConfig(ctx)
	if err != nil {
		return "", "", false, err
	}
	if strings.TrimSpace(running) != strings.TrimSpace(after) {
		return "", "", false, errors.New(configFQCN + ": the router no longer runs the configuration the change left (someone changed it since), so restoring would undo that too; set force to restore anyway")
	}
	return text, saved, hasSaved, nil
}
