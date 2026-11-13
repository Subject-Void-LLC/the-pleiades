// Tests for db.pg.dump into a directory reached through a link, as
// macOS's /tmp is, on a device container reached over SSH.
package pg_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/db/pg"
)

// TestDump_ALinkedDirectoryIsResolved: a dest whose directory is a link,
// or a link to a link, is written in the directory it resolves to, and
// that path is what the dump reports, records as its undo and reads back,
// in a run and in a check; a second dump to the same name through the link
// is refused; and a link at the last name inside the linked directory is
// still refused, with nothing written through it.
func TestDump_ALinkedDirectoryIsResolved(t *testing.T) {
	h := startDeviceHarness(t, "")
	ctx := context.Background()
	h.sql(t, "app", "CREATE TABLE orders (id int)", "INSERT INTO orders VALUES (1), (2)")
	h.dev.Root(t, "mkdir -p /srv/archives && chown "+pg.DeviceUser+" /srv/archives && "+
		"ln -s /srv/archives "+pg.DeviceHome+"/backups && ln -s "+pg.DeviceHome+"/backups "+pg.DeviceHome+"/deep && "+
		"ln -s /srv/archives/never.dump /srv/archives/dangling.dump && chown -h "+pg.DeviceUser+" "+pg.DeviceHome+"/backups "+pg.DeviceHome+"/deep")

	rc := h.fresh()
	if _, err := pg.Dump(ctx, rc, h.device, params(map[string]any{"connection": "admin", "database": "app", "dest": pg.DeviceHome + "/backups/app.dump"})); err != nil {
		t.Fatalf("a dump through a linked directory: %v", err)
	}
	const written = "/srv/archives/app.dump"
	if rc.stats["dest"] != written {
		t.Errorf("dest = %v, want %s", rc.stats["dest"], written)
	}
	inverse, _ := rc.stats["inverse"].(map[string]any)
	if inverse["fqcn"] != "file.remove" || inverse["params"].(map[string]any)["path"] != written {
		t.Errorf("the undo = %v, want file.remove of %s", rc.stats["inverse"], written)
	}
	if size, _ := rc.stats["size"].(int64); size == 0 || len(rc.stats["sha256"].(string)) != 64 {
		t.Errorf("the read back = %v, %v", rc.stats["size"], rc.stats["sha256"])
	}
	if out := h.dev.Root(t, "test -f "+written+" && test ! -L "+written+" && ls -A /srv/archives"); strings.TrimSpace(out) != "app.dump\ndangling.dump" {
		t.Errorf("/srv/archives holds %q, want the archive and the planted link", out)
	}

	rc = h.fresh()
	if _, err := pg.CheckDump(ctx, rc, h.device, params(map[string]any{"connection": "admin", "database": "app", "dest": pg.DeviceHome + "/deep/check.dump"})); err != nil {
		t.Fatalf("a check of a dump through a link to a link: %v", err)
	}
	if rc.stats["dest"] != "/srv/archives/check.dump" {
		t.Errorf("the check's dest = %v", rc.stats["dest"])
	}
	h.dev.Root(t, "test ! -e /srv/archives/check.dump")

	if _, err := pg.Dump(ctx, h.fresh(), h.device, params(map[string]any{"connection": "admin", "database": "app", "dest": pg.DeviceHome + "/deep/app.dump"})); err == nil ||
		!strings.Contains(err.Error(), pg.DeviceHome+"/deep/app.dump ("+written+") exists; a dump never replaces an archive") {
		t.Errorf("a second dump to the same name through a link = %v", err)
	}
	if _, err := pg.Dump(ctx, h.fresh(), h.device, params(map[string]any{"connection": "admin", "database": "app", "dest": pg.DeviceHome + "/backups/dangling.dump"})); err == nil || !strings.Contains(err.Error(), "never replaces an archive") {
		t.Errorf("a dump to a link inside the linked directory = %v", err)
	}
	h.dev.Root(t, "test ! -e /srv/archives/never.dump")
	if _, err := pg.Dump(ctx, h.fresh(), h.device, params(map[string]any{"connection": "admin", "database": "app", "dest": pg.DeviceHome + "/backups/missing/app.dump"})); err == nil || !strings.Contains(err.Error(), pg.DeviceHome+"/backups/missing is not a directory on the device") {
		t.Errorf("a dump into a directory that does not exist = %v", err)
	}
}
