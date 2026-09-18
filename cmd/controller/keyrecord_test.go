// Tests for recording the key a controller first runs with, through the same
// registry and activity stream the server uses.
package main

import (
	"bytes"
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/activityentry"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/encryptionkey"
)

// TestRecordKeyFirstUse_RecordsTheKeyOnceAcrossStarts runs the function the
// server calls at startup twice, as two starts or two replicas would, and
// requires one registry row and one activity entry naming the key only by
// fingerprint.
func TestRecordKeyFirstUse_RecordsTheKeyOnceAcrossStarts(t *testing.T) {
	key := []byte(strings.Repeat("f", 32))
	t.Setenv("MASTER_ENCRYPTION_KEY", crypto.EncodeKey(key))
	t.Setenv("MASTER_ENCRYPTION_KEY_VERSION", "v3")

	ctx := context.Background()
	client, err := ent.OpenDatabase(ctx, ent.Config{DSN: "sqlite://" + filepath.Join(t.TempDir(), "first-use.db")})
	if err != nil {
		t.Fatalf("OpenDatabase() error = %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	recordKeyFirstUse(ctx, client, logger)
	recordKeyFirstUse(ctx, client, logger)

	rows := client.EncryptionKey.Query().AllX(ctx)
	if len(rows) != 1 {
		t.Fatalf("%d registry rows after two starts, want one", len(rows))
	}
	row := rows[0]
	if row.Fingerprint != crypto.Fingerprint(key) || row.Version != "v3" ||
		row.Origin != encryptionkey.OriginFirstUse || row.Possession != encryptionkey.PossessionNotApplicable {
		t.Fatalf("registry row = %+v", row)
	}
	entries := client.ActivityEntry.Query().Where(activityentry.ObjectKindEQ("encryption key")).AllX(ctx)
	if len(entries) != 1 || entries[0].Actor != "controller" || !strings.Contains(entries[0].ObjectName, "first used by this database") {
		t.Fatalf("activity entries = %+v, want one, by the controller, saying first used", entries)
	}
	if strings.Contains(logs.String(), crypto.EncodeKey(key)) {
		t.Fatal("the key was logged")
	}
	if strings.Count(logs.String(), "recorded the master key as first used") != 1 {
		t.Fatalf("want exactly one log line recording the key:\n%s", logs.String())
	}
}
