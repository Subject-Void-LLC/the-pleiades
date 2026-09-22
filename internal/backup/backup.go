// Package backup takes and restores backups of the PostgreSQL database a
// compose deployment keeps its work in.
//
// A backup is PostgreSQL's own custom-format archive, written by pg_dump
// and read back by pg_restore from the server's own release. What this
// package adds is what makes one safe to keep and a restore safe to run:
//
//   - A backup never holds the master key. Its name carries the key's short
//     fingerprint instead, because the file is useless without the key and
//     the moment someone needs to know which key is when they are holding
//     the file.
//   - A backup is written at mode 0600, under a temporary name, and only
//     takes its real name once pg_restore has read it back and it has
//     passed the same check a restore applies.
//   - A restore loads the file into a scratch database as a role with no
//     rights beyond it, and replaces the live database only after the key in
//     .env has been shown to read every sealed value in it, and its schema
//     has been shown to be exactly what this version's migrations make.
package backup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/setup"
)

// Options says where a backup's inputs and outputs are.
type Options struct {
	// DSN is the controller's DB_DSN.
	DSN string

	// SetupDir holds the compose env file, and SetupDisplay is how messages
	// name it when the command runs in a container.
	SetupDir, SetupDisplay string

	// BackupDir is where backups are written, and BackupDisplay how
	// messages name it.
	BackupDir, BackupDisplay string

	// Now is the clock names are taken from. Nil means time.Now.
	Now func() time.Time
}

// now reads the clock.
func (o Options) now() time.Time {
	if o.Now == nil {
		return time.Now()
	}
	return o.Now()
}

// ErrNothingToBackUp is returned for a database no controller has opened
// yet: it holds no schema, so there is nothing a restore could bring back.
var ErrNothingToBackUp = errors.New("backup: the database holds nothing yet; a controller creates its tables the first time it starts")

// Take backs up the database opts.DSN names into opts.BackupDir and returns
// the backup's name.
func Take(ctx context.Context, opts Options, out io.Writer) (Name, error) {
	t, err := parseTarget(opts.DSN)
	if err != nil {
		return Name{}, err
	}
	setupDir, err := setup.OpenDir(opts.SetupDir, opts.SetupDisplay)
	if err != nil {
		return Name{}, err
	}
	defer func() { _ = setupDir.Close() }()
	keys, err := readKeys(setupDir)
	if err != nil {
		return Name{}, err
	}
	dir, err := openStore(opts.BackupDir, opts.BackupDisplay)
	if err != nil {
		return Name{}, err
	}
	defer func() { _ = dir.root.Close() }()

	written, err := take(ctx, t, keys, dir, opts.now(), false)
	if err != nil {
		return Name{}, err
	}
	fmt.Fprintln(out, takenSummary(written, dir, keys))
	return written.name, nil
}

// store is the directory backups are written to, confined with os.Root the
// way setup confines .env: no name reaches outside it, through "..", an
// absolute path or a planted link.
type store struct {
	root    *os.Root
	display string
}

// openStore opens path as a store.
func openStore(path, display string) (*store, error) {
	if display == "" {
		display = path
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, fmt.Errorf("backup: cannot open the backup directory %s: %w", printablePath(display), err)
	}
	return &store{root: root, display: display}, nil
}

// show names a file in the store the way a message should.
func (s *store) show(name string) string { return filepath.Join(s.display, name) }

// taken is what one backup wrote.
type taken struct {
	name    Name
	size    int64
	census  crypto.Census
	version string

	// newer lists the migrations the database records that this build does
	// not know: a newer build migrated it (and this one may be serving it
	// inside the compatibility window, after a rollback, say). Such a backup
	// is taken, and said to need that newer build to restore.
	newer []string
}

// take writes one backup of t into dir under keys' name. Names are to the
// second, so a backup that finds its name taken, by one started in the same
// second, waits for the next second and takes that name instead, once.
func take(ctx context.Context, t target, keys keySet, dir *store, now time.Time, beforeRestore bool) (taken, error) {
	census, version, newer, err := countLive(ctx, t, keys)
	if err != nil {
		return taken{}, err
	}
	name := newName(now, keys.key, beforeRestore)
	for attempt := 0; ; attempt++ {
		size, err := dir.write(ctx, t, name.String(), len(newer) > 0)
		if errors.Is(err, fs.ErrExist) && attempt == 0 {
			time.Sleep(time.Until(now.Truncate(time.Second).Add(time.Second)))
			name = newName(now.Add(time.Second), keys.key, beforeRestore)
			continue
		}
		if err != nil {
			return taken{}, err
		}
		return taken{name: name, size: size, census: census, version: version, newer: newer}, nil
	}
}

// write dumps t into a new file named final, and returns its size. It
// returns an error wrapping fs.ErrExist when final is already taken. newer
// says the live database holds a newer build's tables, which the read-back
// check then accepts (see check).
func (s *store) write(ctx context.Context, t target, final string, newer bool) (int64, error) {
	partial := "." + final + ".partial"
	f, err := s.root.OpenFile(partial, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return 0, fmt.Errorf("backup: cannot create a file in %s: %w", printablePath(s.display), err)
	}
	// Whatever happens below, the partial file does not outlive this call.
	defer func() { _ = s.root.Remove(partial) }()

	err = tools{}.dump(ctx, t, f)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return 0, fmt.Errorf("backup: the database could not be dumped, and no backup was written: %w", err)
	}
	if err := s.check(ctx, partial, newer); err != nil {
		return 0, fmt.Errorf("backup: what pg_dump wrote does not read back as a backup this command could restore, so it was deleted: %w", err)
	}
	info, err := s.root.Stat(partial)
	if err != nil {
		return 0, fmt.Errorf("backup: %w", err)
	}
	// Linking rather than renaming refuses to replace a file already under
	// the final name.
	if err := s.root.Link(partial, final); err != nil {
		if _, statErr := s.root.Lstat(final); errors.Is(err, fs.ErrExist) || statErr == nil {
			return 0, fmt.Errorf("backup: %s already exists: %w", printablePath(s.show(final)), fs.ErrExist)
		}
		// Some bind mounts refuse hard links; the name was just checked
		// free, and it is unique to the second.
		if err := s.root.Rename(partial, final); err != nil {
			return 0, fmt.Errorf("backup: naming the backup: %w", err)
		}
	}
	s.sync()
	return info.Size(), nil
}

// countLive reads the live database's migration history and counts what
// keys open in it. A database with no history is ErrNothingToBackUp. It also
// returns the migrations a newer build recorded that this one does not know.
//
// Such a database is still backed up. A dump does not depend on the build
// that takes it, and refusing would leave an operator with no backup at the
// moment they reached for one; restoring is where the build has to match,
// and restore refuses a history it does not know.
func countLive(ctx context.Context, t target, keys keySet) (crypto.Census, string, []string, error) {
	db, err := ent.OpenExisting(ctx, t.dsn())
	if err != nil {
		return crypto.Census{}, "", nil, fmt.Errorf("backup: cannot reach the database: %w", err)
	}
	defer func() { _ = db.Close() }()
	version, err := latestMigration(ctx, db)
	if err != nil {
		return crypto.Census{}, "", nil, err
	}
	if version == "" {
		return crypto.Census{}, "", nil, ErrNothingToBackUp
	}
	plan, err := db.SchemaPlan(ctx)
	if err != nil {
		return crypto.Census{}, "", nil, fmt.Errorf("backup: reading the migration history: %w", err)
	}
	census, err := crypto.TakeCensus(ctx, db, keys.candidates())
	if err != nil {
		return crypto.Census{}, "", nil, fmt.Errorf("backup: counting what the database holds: %w", err)
	}
	return census, version, plan.Unknown, nil
}

// latestMigration is the newest migration db records, or empty when it
// records none.
func latestMigration(ctx context.Context, db *ent.ExistingDatabase) (string, error) {
	versions, err := db.MigrationHistory(ctx)
	if err != nil {
		return "", fmt.Errorf("backup: reading the migration history: %w", err)
	}
	if len(versions) == 0 {
		return "", nil
	}
	sort.Strings(versions)
	return versions[len(versions)-1], nil
}

// check reads name back through pg_restore and refuses it unless it is a
// custom-format archive of this schema, or, when newer is set, of this
// schema plus tables a newer build created.
func (s *store) check(ctx context.Context, name string, newer bool) error {
	f, err := s.root.Open(name)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	listing, err := tools{}.list(ctx, f)
	if err != nil {
		return err
	}
	toc, err := ParseTOC(listing)
	if err != nil {
		return err
	}
	known := knownTables()
	if newer {
		// The live database holds tables a newer build created. They are
		// that build's to know, not this one's, so
		// each table the archive names is accepted as a table; every other
		// refusal (another schema, a kind this schema never creates, no
		// history table) stands, and restoring the file is still that newer
		// build's job, which says so when this one is asked.
		for _, e := range toc.Entries {
			if e.Kind == "TABLE" && e.Namespace == "public" {
				known[e.Tag] = true
			}
		}
	}
	return toc.Check(known)
}

// sync flushes the directory entry for a new name to disk. A failure is
// ignored: the file itself was synced, and a directory some filesystems
// cannot sync is still a directory holding it.
func (s *store) sync() {
	if d, err := s.root.Open("."); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
}
