// Package inventory_test: neither store, nor the hosts.yaml backstop, takes
// a new device whose name would not show as itself (a newline, an escape, a
// direction override), while a device stored under such a name before the
// rule keeps being read, listed, saved, retired and written again
// (LESSONS 415).
//
// Every character outside printable ASCII is built from its code point in
// code, so this file holds none (FAILURE_PATTERNS 256, 834).
package inventory_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/enttest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/recordname"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// unshownNames are device names recordname.Check refuses for what they
// hold rather than for what they are: a newline that would print a line of
// its own, an escape sequence, a direction override, a zero width space, a
// byte order mark, a tag character, a byte that is not UTF-8, a no-break
// space and a zero width non-joiner.
func unshownNames() []string {
	return []string{
		"sw2\npleiades: approved",
		"sw\x1b[2J1",
		"abc" + string(rune(0x202E)) + "def",
		"web1" + string(rune(0x200B)),
		string(rune(0xFEFF)) + "web1",
		"web1" + string(rune(0xE0041)),
		"sw\xff1",
		"web" + string(rune(0x00A0)) + "1",
		"sw-" + string([]rune{0x0645, 0x06CC, 0x200C, 0x062E}),
	}
}

// TestRepositoryConformance_CreateRefusesANameThatWouldNotShowAsItself
// proves both stores refuse each name with the error that says why, and
// store nothing under it, while a name in a real script beside them, with
// no joiner, is created.
func TestRepositoryConformance_CreateRefusesANameThatWouldNotShowAsItself(t *testing.T) {
	for _, backend := range repositoryBackends() {
		t.Run(backend.name, func(t *testing.T) {
			repo := backend.newRepo(t)
			ctx := context.Background()
			for i, name := range unshownNames() {
				err := repo.Create(ctx, newConformanceItem(t, name, map[string]interface{}{"host": fmt.Sprintf("10.0.1.%d", i)}))
				if !errors.Is(err, recordname.ErrUnusable) {
					t.Fatalf("Create(%q) = %v, want an error matching recordname.ErrUnusable", name, err)
				}
				if !strings.Contains(err.Error(), "the name cannot be used") {
					t.Errorf("Create(%q) says %q, want the reason", name, err)
				}
				if _, err := repo.GetByName(ctx, name); !errors.Is(err, inventory.ErrItemNotFound) {
					t.Errorf("GetByName(%q) after a refused Create = %v, want ErrItemNotFound", name, err)
				}
			}
			persian := "sw-" + string([]rune{0x0645, 0x06CC, 0x062E})
			if err := repo.Create(ctx, newConformanceItem(t, persian, map[string]interface{}{"host": "10.0.1.99"})); err != nil {
				t.Fatalf("Create(%q) = %v, want nil", persian, err)
			}
		})
	}
}

// legacyBackend is a store holding one device under a name the rule now
// refuses, written the way a release before the rule could have written it.
type legacyBackend struct {
	name    string
	newRepo func(t *testing.T, legacy string) inventory.Repository
}

// legacyBackends seeds the ent store through the generated client, as a
// Create before the rule did, and the file store through hosts.yaml
// written by hand, the one way such a name reaches it now.
func legacyBackends() []legacyBackend {
	return []legacyBackend{
		{name: "ent", newRepo: func(t *testing.T, legacy string) inventory.Repository {
			t.Helper()
			client := enttest.Open(t, "sqlite3", fmt.Sprintf("file:%s?mode=memory&cache=shared&_fk=1", t.Name()))
			t.Cleanup(func() { _ = client.Close() })
			if _, err := client.Device.Create().SetName(legacy).SetType("linux_server").
				SetProperties(map[string]interface{}{"host": "10.0.2.1"}).Save(context.Background()); err != nil {
				t.Fatalf("seeding %q: %v", legacy, err)
			}
			return inventory.NewEntRepository(client, inventory.NewItemFactory())
		}},
		{name: "file", newRepo: func(t *testing.T, legacy string) inventory.Repository {
			t.Helper()
			path := filepath.Join(t.TempDir(), "hosts.yaml")
			// A YAML double-quoted scalar reads Go's escapes (\n, \x1b,
			// \u202e, \U000e0041) as the characters they stand for.
			doc := "hosts:\n  - id: legacy-1\n    name: " + strconv.Quote(legacy) + "\n    type: linux_server\n    properties:\n      host: 10.0.2.1\n"
			if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
				t.Fatal(err)
			}
			hosts, err := inventory.ReadHosts(path)
			if err != nil || len(hosts) != 1 || hosts[0].Name != legacy {
				t.Fatalf("the seeded file reads back as %+v, %v; want the one host %q", hosts, err, legacy)
			}
			return inventory.NewFileRepository(path, inventory.NewItemFactory())
		}},
	}
}

// TestRepositoryConformance_AStoredNameTheRuleRefusesStillReads stores a
// device under each refused name the way a release before the rule could
// have, then reads it by name and in a listing, changes and saves it,
// creates another device beside it (which rewrites hosts.yaml in full) and
// retires it. Only a create is held to the rule, so none of these refuses.
func TestRepositoryConformance_AStoredNameTheRuleRefusesStillReads(t *testing.T) {
	for _, backend := range legacyBackends() {
		for i, legacy := range unshownNames() {
			if backend.name == "file" && !utf8.ValidString(legacy) {
				// A YAML document must be UTF-8, so a byte that is not
				// cannot be in hosts.yaml at all: no release stored one.
				continue
			}
			t.Run(fmt.Sprintf("%s/%d", backend.name, i), func(t *testing.T) {
				repo := backend.newRepo(t, legacy)
				ctx := context.Background()

				item, err := repo.GetByName(ctx, legacy)
				if err != nil {
					t.Fatalf("GetByName(%q) = %v, want the stored device", legacy, err)
				}
				it, err := repo.GetGroup(ctx, pkginventory.Selector{})
				if err != nil {
					t.Fatalf("GetGroup: %v", err)
				}
				listed := false
				for it.Next(ctx) {
					listed = listed || it.Item().Name() == legacy
				}
				_ = it.Close()
				if err := it.Error(); err != nil || !listed {
					t.Errorf("the listing does not hold %q (err %v)", legacy, err)
				}

				if err := item.AddInfo("kernel", "6.6.1", true); err != nil {
					t.Fatalf("AddInfo: %v", err)
				}
				if err := repo.Save(ctx, item); err != nil {
					t.Fatalf("Save(%q) = %v, want nil", legacy, err)
				}
				if err := repo.Create(ctx, newConformanceItem(t, "beside-legacy", map[string]interface{}{"host": "10.0.2.2"})); err != nil {
					t.Fatalf("Create beside %q = %v, want nil: a held name does not stop another write", legacy, err)
				}
				reloaded, err := repo.GetByName(ctx, legacy)
				if err != nil {
					t.Fatalf("GetByName(%q) after the writes = %v", legacy, err)
				}
				if kernel, _ := reloaded.Properties().String("kernel"); kernel != "6.6.1" {
					t.Errorf("the saved property reads %q, want 6.6.1", kernel)
				}
				if err := repo.Retire(ctx, legacy); err != nil {
					t.Fatalf("Retire(%q) = %v, want nil", legacy, err)
				}
			})
		}
	}
}

// TestEncodeHosts_RefusesANewHostThatWouldNotShowAsItself holds the
// hosts.yaml backstop to the same rule: each name is refused as a new host,
// while a file already holding one is written again with it beside a new
// host.
func TestEncodeHosts_RefusesANewHostThatWouldNotShowAsItself(t *testing.T) {
	ok := inventory.HostSpec{ID: "b", Name: "web-01", Type: "linux_server"}
	for _, name := range unshownNames() {
		if _, err := inventory.EncodeHosts(nil, []inventory.HostSpec{ok, {ID: "c", Name: name, Type: "linux_server"}}); !errors.Is(err, recordname.ErrUnusable) {
			t.Errorf("EncodeHosts with a new host %q = %v, want a refusal matching recordname.ErrUnusable", name, err)
		}
	}
	held := "sw2\npleiades: approved"
	original := []byte("hosts:\n  - id: a\n    name: " + strconv.Quote(held) + "\n    type: linux_server\n")
	out, err := inventory.EncodeHosts(original, []inventory.HostSpec{{ID: "a", Name: held, Type: "linux_server"}, ok})
	if err != nil {
		t.Fatalf("EncodeHosts over a file holding %q = %v, want nil", held, err)
	}
	hosts, err := inventory.ParseHosts(out)
	if err != nil || len(hosts) != 2 || hosts[0].Name != held {
		t.Errorf("the file written again reads %+v, %v; want %q kept beside web-01", hosts, err, held)
	}
}
