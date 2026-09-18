// Restoring a backup: the order of the steps, and what each must prove
// before the next one runs.
package backup

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"io/fs"

	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
	"github.com/Subject-Void-LLC/the-pleiades/internal/setup"
)

// RestoreOptions says what to restore, and where everything is.
type RestoreOptions struct {
	Options

	// ArchiveDir holds the backup to restore, and ArchiveDisplay is how
	// messages name it.
	ArchiveDir, ArchiveDisplay string

	// Archive is the backup's file name inside ArchiveDir.
	Archive string
}

// KeySource supplies the key a backup was taken under, when .env holds no
// key: from a terminal, or from standard input. label is the key the
// backup's name gives, "3f9a-c21b", or empty when the name gives none.
type KeySource func(label string) ([]byte, error)

// ErrNoKeySource is returned when .env holds no key and nothing was given to
// ask for one.
var ErrNoKeySource = errors.New("backup: .env holds no key, and there is no terminal to ask for the one this backup was taken under; pass it on standard input with --key-stdin")

// Restored is what a restore did.
type Restored struct {
	// SetAside is the backup a restore took of the database it replaced,
	// empty when that database held nothing.
	SetAside string

	// FailedJobs and EndedSessions count what the restore changed in the
	// restored database before it went live.
	FailedJobs, EndedSessions int

	// KeyFile names .env when the restore wrote the key into it.
	KeyFile string
}

// Restore replaces the database opts.DSN names with the backup
// opts.Archive, if and only if every check passes. Until the final swap,
// the live database is not touched, and any refusal leaves it exactly as it
// was.
func Restore(ctx context.Context, opts RestoreOptions, keySource KeySource, out io.Writer) (Restored, error) {
	t, err := parseTarget(opts.DSN)
	if err != nil {
		return Restored{}, err
	}
	setupDir, err := setup.OpenDir(opts.SetupDir, opts.SetupDisplay)
	if err != nil {
		return Restored{}, err
	}
	defer func() { _ = setupDir.Close() }()
	envKeys, err := readKeys(setupDir)
	if err != nil {
		return Restored{}, err
	}
	archives, err := openStore(opts.ArchiveDir, opts.ArchiveDisplay)
	if err != nil {
		return Restored{}, err
	}
	defer func() { _ = archives.root.Close() }()
	backups, err := openStore(opts.BackupDir, opts.BackupDisplay)
	if err != nil {
		return Restored{}, err
	}
	defer func() { _ = backups.root.Close() }()

	s, err := newScratch(ctx, t)
	if err != nil {
		return Restored{}, err
	}
	defer s.close()
	if err := s.refuseIfInUse(); err != nil {
		return Restored{}, err
	}

	// 1. The file: a regular file, a custom-format archive, of this schema.
	fmt.Fprintf(out, "Reading %s.\n", printablePath(archives.show(opts.Archive)))
	if err := archives.checkArchive(ctx, opts.Archive); err != nil {
		return Restored{}, err
	}
	name, named := ParseName(opts.Archive)

	// 2. The key: the one in .env, or the one the backup was taken under.
	keys := envKeys
	importing := keys.key == nil
	if importing {
		if keys, err = askForKey(keySource, name, named); err != nil {
			return Restored{}, err
		}
	}

	// 3. The scratch database: loaded as a role with no other rights,
	// counted, migrated, compared, and prepared.
	fmt.Fprintln(out, "Loading it into a scratch database, as a role that can reach nothing else.")
	if err := s.load(archives, opts.Archive); err != nil {
		return Restored{}, err
	}
	census, onRecord, err := s.count(keys)
	if err != nil {
		return Restored{}, err
	}
	if importing {
		if keys.version, err = tagOf(census, keys); err != nil {
			return Restored{}, fmt.Errorf("backup: nothing was changed: %w", err)
		}
	}
	if err := keys.readable(census, onRecord); err != nil {
		return Restored{}, fmt.Errorf("backup: nothing was changed, because restoring would leave values the controller cannot read: %w\n\n%s", err, keyAdvice(importing))
	}
	fmt.Fprintln(out, readableLine(census.Sealed(), keys.name))
	fmt.Fprintln(out, "Bringing it up to this version's schema, and comparing that schema with a fresh one.")
	failed, ended, err := s.prepare(opts.Archive)
	if err != nil {
		return Restored{}, err
	}

	// 4. The live database is set aside before anything replaces it.
	result := Restored{FailedJobs: failed, EndedSessions: ended}
	aside, err := take(ctx, t, envKeys, backups, opts.now(), true)
	switch {
	case errors.Is(err, ErrNothingToBackUp):
		// A database no controller has opened holds nothing to set aside.
	case err != nil:
		return Restored{}, fmt.Errorf("backup: nothing was changed, because the database being replaced could not be backed up first: %w", err)
	default:
		result.SetAside = backups.show(aside.name.String())
	}

	// 5. The key, written before the swap: if the swap then fails, .env
	// holds a key that opens nothing yet, and running the restore again
	// uses it.
	if importing {
		if result.KeyFile, err = setup.ImportKey(setupDir, keys.key, keys.version); err != nil {
			return Restored{}, fmt.Errorf("backup: nothing was changed, because the key could not be written: %w", err)
		}
	}

	// 6. The swap.
	if err := s.swap(); err != nil {
		return Restored{}, err
	}
	fmt.Fprintln(out, restoredSummary(t.database, archives.show(opts.Archive), name, named, census, keys, result))
	return result, nil
}

// askForKey gets the key a backup was taken under from keySource, and
// refuses early when the backup's name says a different one.
func askForKey(keySource KeySource, name Name, named bool) (keySet, error) {
	if keySource == nil {
		return keySet{}, ErrNoKeySource
	}
	label := ""
	if named && name.Key != "" {
		label = name.KeyLabel()
	}
	key, err := keySource(label)
	if err != nil {
		return keySet{}, err
	}
	if len(key) != 32 {
		return keySet{}, errors.New("backup: the key entered is not 32 bytes, so it is not a master encryption key")
	}
	if label != "" {
		got := crypto.Fingerprint(key)[:8]
		if subtle.ConstantTimeCompare([]byte(got), []byte(name.Key)) != 1 {
			return keySet{}, fmt.Errorf("backup: nothing was changed: the key entered has the fingerprint %s, and the backup's name says it was taken under %s", entered(key).short(), label)
		}
	}
	return entered(key), nil
}

// checkArchive refuses name unless it is a regular file this command could
// restore. It does not follow a symbolic link, for the reason setup does
// not: the file a link points at is not the one that was named.
func (s *store) checkArchive(ctx context.Context, name string) error {
	info, err := s.root.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("backup: there is no %s", printablePath(s.show(name)))
	}
	if err != nil {
		return fmt.Errorf("backup: cannot read %s: %w", printablePath(s.show(name)), err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("backup: %s is not a regular file", printablePath(s.show(name)))
	}
	f, err := s.root.Open(name)
	if err != nil {
		return fmt.Errorf("backup: cannot read %s: %w", printablePath(s.show(name)), err)
	}
	magic := make([]byte, 5)
	_, err = io.ReadFull(f, magic)
	_ = f.Close()
	if err != nil || string(magic) != "PGDMP" {
		return fmt.Errorf("%w: %s is not a PostgreSQL custom-format archive", ErrNotABackup, printablePath(s.show(name)))
	}
	return s.check(ctx, name)
}
