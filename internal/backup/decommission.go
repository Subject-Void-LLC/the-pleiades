// What decommissioning a compose deployment removes and keeps, and the
// phrase that confirms it.
package backup

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/setup"
)

// Decommission is what `make decom` is about to remove and what it keeps.
// This package removes nothing: the containers and volumes belong to Docker,
// which the command confirming this cannot reach, and removing them is the
// Makefile's step after this one says yes.
type Decommission struct {
	// EnvFile names .env, and Key is its key's short fingerprint, empty
	// when it holds none.
	EnvFile, Key string

	// BackupDir names the backup directory, and Backups lists the backups
	// in it, newest first.
	BackupDir string
	Backups   []Name
}

// PlanDecommission reads .env and the backup directory. A backup directory
// that cannot be read is an error, not "no backups": the warning is only
// worth anything if it is right about what would survive.
func PlanDecommission(setupDir, setupDisplay, backupDir, backupDisplay string) (Decommission, error) {
	dir, err := setup.OpenDir(setupDir, setupDisplay)
	if err != nil {
		return Decommission{}, err
	}
	defer func() { _ = dir.Close() }()
	keys, err := readKeys(dir)
	if err != nil {
		return Decommission{}, err
	}
	d := Decommission{EnvFile: dir.Show(setup.ComposeFile), BackupDir: backupDisplay}
	if keys.key != nil {
		d.Key = keys.short()
	}

	backups, err := openStore(backupDir, backupDisplay)
	if err != nil {
		return Decommission{}, err
	}
	defer func() { _ = backups.root.Close() }()
	listing, err := backups.root.Open(".")
	if err != nil {
		return Decommission{}, fmt.Errorf("backup: cannot list %s: %w", printablePath(backupDisplay), err)
	}
	entries, err := listing.ReadDir(-1)
	_ = listing.Close()
	if err != nil {
		return Decommission{}, fmt.Errorf("backup: cannot list %s: %w", printablePath(backupDisplay), err)
	}
	for _, e := range entries {
		if n, ok := ParseName(e.Name()); ok && e.Type().IsRegular() {
			d.Backups = append(d.Backups, n)
		}
	}
	sort.Slice(d.Backups, func(i, j int) bool { return d.Backups[i].Taken.After(d.Backups[j].Taken) })
	return d, nil
}

// Phrase is what a person types to confirm: "decommission 3f9a-c21b", the
// key's fingerprint, so the one moment they must see which key their
// backups need is the moment they confirm deleting it.
func (d Decommission) Phrase() string {
	if d.Key == "" {
		return "decommission"
	}
	return "decommission " + d.Key
}

// Warning says what is removed and what is kept, at time now.
func (d Decommission) Warning(now time.Time) string {
	lines := []string{
		"make decom removes this deployment:",
		"  - the database, and every user, credential, device, template, job and activity entry in it",
		"  - the broker's stored messages, and the controller's data volume",
		"  - the containers and the network",
	}
	if d.Key != "" {
		lines = append(lines, fmt.Sprintf("  - %s, which holds master encryption key %s and the JWT secret", printablePath(d.EnvFile), d.Key))
	} else {
		lines = append(lines, fmt.Sprintf("  - %s, which holds no master encryption key", printablePath(d.EnvFile)))
	}
	lines = append(lines, "", "It keeps the images, this checkout, and "+d.backupLine(now)+".", "")

	switch {
	case len(d.Backups) == 0:
		lines = append(lines, "THERE IS NO BACKUP. Once this runs, everything listed above is gone for good.",
			"Run `make backup` first if anything in this deployment is worth keeping.")
	default:
		lines = append(lines, "Anything done since the newest backup is gone for good once this runs.",
			"Run `make backup` first to keep it.")
	}
	if under := d.backupsUnderKey(); d.Key != "" && under > 0 {
		lines = append(lines, "",
			fmt.Sprintf("%d of those backups %s key %s to restore. Once .env is deleted, the only copies of that key",
				under, pick(under == 1, "needs", "need"), d.Key),
			"are the ones you kept somewhere else. Check that you have one before going on.")
	}
	return strings.Join(lines, "\n")
}

// backupLine describes the backups kept.
func (d Decommission) backupLine(now time.Time) string {
	if len(d.Backups) == 0 {
		return fmt.Sprintf("the backup directory %s, which holds no backups", printablePath(d.BackupDir))
	}
	newest := d.Backups[0]
	return fmt.Sprintf("the %d %s in %s, the newest taken %s (%s ago) under key %s",
		len(d.Backups), pick(len(d.Backups) == 1, "backup", "backups"), printablePath(d.BackupDir),
		newest.Taken.UTC().Format("2006-01-02 15:04 UTC"), age(now.Sub(newest.Taken)), newest.KeyLabel())
}

// backupsUnderKey counts the backups taken under .env's key.
func (d Decommission) backupsUnderKey() int {
	n := 0
	for _, b := range d.Backups {
		if b.KeyLabel() == d.Key {
			n++
		}
	}
	return n
}

// age renders a duration the way a person says it.
func age(d time.Duration) string {
	switch {
	case d < 2*time.Minute:
		return "moments"
	case d < 2*time.Hour:
		return fmt.Sprintf("%d minutes", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d hours", int(d.Hours()))
	default:
		return fmt.Sprintf("%d days", int(d.Hours()/24))
	}
}
