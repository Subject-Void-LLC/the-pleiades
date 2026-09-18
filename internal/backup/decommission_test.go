// Tests for what decommissioning says it removes and keeps.
package backup_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/backup"
	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
	"github.com/Subject-Void-LLC/the-pleiades/internal/keyregistry"
)

// TestPlanDecommission_NamesTheKeyAndTheNewestBackup covers the warning a
// person reads before typing the phrase: which key goes with .env, how many
// backups need it, and when the newest was taken.
func TestPlanDecommission_NamesTheKeyAndTheNewestBackup(t *testing.T) {
	key := testKey('m')
	short := keyregistry.Short(crypto.Fingerprint(key))
	d := newDirs(t, envKey(key))
	now := time.Date(2026, 9, 18, 18, 0, 0, 0, time.UTC)
	for _, n := range []backup.Name{
		backup.NewName(now.Add(-3*time.Hour), key, false),
		backup.NewName(now.Add(-30*time.Minute), key, true),
		backup.NewName(now.Add(-72*time.Hour), testKey('o'), false),
	} {
		if err := os.WriteFile(filepath.Join(d.backups, n.String()), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// Neither a stray file nor a directory with a backup's name is a backup.
	if err := os.WriteFile(filepath.Join(d.backups, "notes.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(d.backups, backup.NewName(now, key, false).String()), 0o700); err != nil {
		t.Fatal(err)
	}

	plan, err := backup.PlanDecommission(d.setup, "", d.backups, "./backups")
	if err != nil {
		t.Fatalf("PlanDecommission() error = %v", err)
	}
	if plan.Key != short || plan.Phrase() != "decommission "+short || len(plan.Backups) != 3 {
		t.Fatalf("plan = %+v, phrase %q", plan, plan.Phrase())
	}
	if !plan.Backups[0].Taken.Equal(now.Add(-30 * time.Minute)) {
		t.Fatalf("the newest backup is %v, want the one from 30 minutes ago", plan.Backups[0].Taken)
	}
	warning := plan.Warning(now)
	for _, want := range []string{
		"which holds master encryption key " + short,
		"the 3 backups in ./backups, the newest taken 2026-09-18 17:30 UTC (30 minutes ago) under key " + short,
		"2 of those backups need key " + short,
		"Anything done since the newest backup is gone for good",
	} {
		if !strings.Contains(warning, want) {
			t.Errorf("the warning does not say %q:\n%s", want, warning)
		}
	}
	if strings.Contains(warning, "THERE IS NO BACKUP") {
		t.Error("the warning says there is no backup, and there are three")
	}
}

// TestPlanDecommission_SaysWhenThereIsNothingToFallBackOn covers the two
// states that make the warning loudest: no backup at all, and no key.
func TestPlanDecommission_SaysWhenThereIsNothingToFallBackOn(t *testing.T) {
	d := newDirs(t)
	plan, err := backup.PlanDecommission(d.setup, "", d.backups, d.backups)
	if err != nil {
		t.Fatalf("PlanDecommission() error = %v", err)
	}
	if plan.Phrase() != "decommission" {
		t.Fatalf("Phrase() = %q with no key", plan.Phrase())
	}
	warning := plan.Warning(time.Now())
	for _, want := range []string{"THERE IS NO BACKUP", "which holds no master encryption key", "which holds no backups"} {
		if !strings.Contains(warning, want) {
			t.Errorf("the warning does not say %q:\n%s", want, warning)
		}
	}

	if _, err := backup.PlanDecommission(d.setup, "", filepath.Join(d.backups, "missing"), "missing"); err == nil {
		t.Fatal("a backup directory that cannot be read was taken as one holding no backups")
	}
}

// TestTakeAndRestore_RefuseBeforeReachingADatabase covers the refusals an
// operator meets before anything connects: a DSN that is not PostgreSQL, a
// backup directory that does not exist, an .env setup would not read, and a
// server that does not answer.
func TestTakeAndRestore_RefuseBeforeReachingADatabase(t *testing.T) {
	const unreachable = "postgres://pleiades:password@127.0.0.1:1/pleiades?sslmode=disable&connect_timeout=2"
	d := newDirs(t, envKey(testKey('z')))
	bad := newDirs(t, "MASTER_ENCRYPTION_KEY=\"quoted\"")
	cases := map[string]struct {
		opts backup.Options
		want string
	}{
		"sqlite":          {backup.Options{DSN: "sqlite://controller.db", SetupDir: d.setup, BackupDir: d.backups}, "does not name a PostgreSQL database"},
		"no backup dir":   {backup.Options{DSN: unreachable, SetupDir: d.setup, BackupDir: filepath.Join(d.backups, "missing")}, "cannot open the backup directory"},
		"unreadable .env": {backup.Options{DSN: unreachable, SetupDir: bad.setup, BackupDir: bad.backups}, "cannot be read safely"},
		"no server":       {backup.Options{DSN: unreachable, SetupDir: d.setup, BackupDir: d.backups}, "cannot reach the database"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := backup.Take(context.Background(), c.opts, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("Take() error = %v, want %q", err, c.want)
			}
		})
	}
	_, err := backup.Restore(context.Background(), backup.RestoreOptions{
		Options: backup.Options{DSN: unreachable, SetupDir: d.setup, BackupDir: d.backups}, ArchiveDir: d.backups, Archive: "x.dump",
	}, nil, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "cannot reach the database server") {
		t.Errorf("Restore() error = %v, want the unreachable server named", err)
	}
	if entries, _ := os.ReadDir(d.backups); len(entries) != 0 {
		t.Fatalf("a refusal before reaching a database left %d files", len(entries))
	}
}
