// Tests that the facts routes (device_facts.go) answer a store refusing as
// the controller is configured with a 503 and a fixed cause, through the
// real handler over the store the Controller serves (internal/facts/entstore,
// on a real SQLite file), and a real fault with a 500.
package api_test

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/enttest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts/entstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts/factstest"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	inventorytest "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
)

// unavailableKey is the one fact the device holds: a name the answer must
// never repeat.
const unavailableKey = "package.zq9secretpkg"

// TestDeviceFacts_AStoreRefusingAsConfiguredAnswers503: with no facts key,
// with the device's facts stored under another key, and with a device whose
// facts could not be re-keyed, every facts route answers 503 with the
// store's fixed cause, never the fact's name, a key's identity or the
// store's own text, and logs a warning rather than an error. The control:
// the store holding the right key serves the same rows.
func TestDeviceFacts_AStoreRefusingAsConfiguredAnswers503(t *testing.T) {
	ctx := context.Background()
	dsn := fmt.Sprintf("file:%s?_journal_mode=WAL&_busy_timeout=10000&_fk=1", filepath.Join(t.TempDir(), "facts.db"))
	client := enttest.Open(t, "sqlite3", dsn)
	t.Cleanup(func() { _ = client.Close() })
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	dev := client.Device.Create().SetName("web1").SetType("linux_server").SetProperties(map[string]any{"host": "10.0.0.1"}).SaveX(ctx)
	item := &inventorytest.Stub{StubID: pkginventory.DeviceID(dev.DeviceID), StubName: "web1"}
	stored := entstore.New(client, factstest.Keys(), factstest.Envelope())
	recordKey(t, stored, dev.DeviceID, unavailableKey)
	// The stored value's time, edited: it still opens, and its chain no
	// longer verifies, so a re-key from these keys is refused.
	if _, err := db.ExecContext(ctx, "UPDATE facts SET created_at = ?", time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	other, err := entstore.AtRestKeys(bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatal(err)
	}
	paths := []string{
		"/inventory/devices/web1/facts",
		"/inventory/devices/web1/facts/" + url.PathEscape(unavailableKey),
		"/inventory/devices/web1/fact-chain",
	}
	for _, c := range []struct {
		name   string
		reader api.FactReader
		cause  string
	}{
		{"no facts key", entstore.New(client, nil, factstest.Envelope()), "holds no facts key"},
		{"another facts key", entstore.New(client, other, factstest.Envelope()), "stored under another facts key"},
		{"a device that could not be re-keyed", entstore.New(client, other, factstest.Envelope(), entstore.WithPreviousKeys(factstest.Keys())), "could not be re-keyed"},
	} {
		t.Run(c.name, func(t *testing.T) {
			for _, path := range paths {
				var logs bytes.Buffer
				rr := factsGet(factsRouterLogging(t, &stubDeviceRepo{item: item}, c.reader, &logs), path)
				body := rr.Body.String()
				if rr.Code != http.StatusServiceUnavailable || !strings.Contains(body, "facts are unavailable") || !strings.Contains(body, c.cause) {
					t.Errorf("%s: %d %s; want 503 saying %q", path, rr.Code, body, c.cause)
				}
				for _, never := range []string{"zq9", factstest.Keys().ID(), other.ID(), "chain breaks", "stored row"} {
					if strings.Contains(body, never) {
						t.Errorf("%s: the answer holds %q: %s", path, never, body)
					}
				}
				if !strings.Contains(logs.String(), `"level":"WARN"`) || strings.Contains(logs.String(), `"level":"ERROR"`) {
					t.Errorf("%s: logged %s; want a warning and no error", path, logs.String())
				}
			}
		})
	}
	// The control: the keys the rows were stored under read them.
	if rr := factsGet(factsRouter(t, &stubDeviceRepo{item: item}, stored), paths[0]); rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), unavailableKey) {
		t.Errorf("the control failed: the right keys answer %d %s", rr.Code, rr.Body.String())
	}
	// A fault that is not configuration is still a 500, logged as an error.
	var logs bytes.Buffer
	broken := entstore.New(client, factstest.Keys(), factstest.Envelope())
	_ = db.Close()
	_ = client.Close()
	if rr := factsGet(factsRouterLogging(t, &stubDeviceRepo{item: item}, broken, &logs), paths[0]); rr.Code != http.StatusInternalServerError || !strings.Contains(logs.String(), `"level":"ERROR"`) {
		t.Errorf("a closed database: %d %s, logged %s; want a 500 logged as an error", rr.Code, rr.Body.String(), logs.String())
	}
}

// TestDeviceFacts_ARowThatDoesNotOpenAnswers503: a device whose stored row
// holds another row's payload, which does not open bound to its own token,
// answers its facts and history routes 503, naming the command that shows
// which row, and never the fact; its chain route still answers, with the
// break at that row.
func TestDeviceFacts_ARowThatDoesNotOpenAnswers503(t *testing.T) {
	ctx := context.Background()
	dsn := fmt.Sprintf("file:%s?_journal_mode=WAL&_busy_timeout=10000&_fk=1", filepath.Join(t.TempDir(), "facts.db"))
	client := enttest.Open(t, "sqlite3", dsn)
	t.Cleanup(func() { _ = client.Close() })
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	dev := client.Device.Create().SetName("web1").SetType("linux_server").SetProperties(map[string]any{"host": "10.0.0.1"}).SaveX(ctx)
	item := &inventorytest.Stub{StubID: pkginventory.DeviceID(dev.DeviceID), StubName: "web1"}
	store := entstore.New(client, factstest.Keys(), factstest.Envelope())
	recordKey(t, store, dev.DeviceID, unavailableKey)
	recordKey(t, store, dev.DeviceID, "package.zq9otherpkg")
	if _, err := db.ExecContext(ctx, "UPDATE facts SET payload = (SELECT payload FROM facts ORDER BY id LIMIT 1) WHERE id = (SELECT max(id) FROM facts)"); err != nil {
		t.Fatal(err)
	}
	// The history of the key whose row was overwritten: another key's rows
	// open, and its history is served.
	for _, path := range []string{"/inventory/devices/web1/facts", "/inventory/devices/web1/facts/package.zq9otherpkg"} {
		var logs bytes.Buffer
		rr := factsGet(factsRouterLogging(t, &stubDeviceRepo{item: item}, store, &logs), path)
		body := rr.Body.String()
		if rr.Code != http.StatusServiceUnavailable || !strings.Contains(body, "does not open as its own") || !strings.Contains(body, "controller facts inspect") || strings.Contains(body, "zq9") {
			t.Errorf("%s: %d %s; want 503 naming the inspect command and no fact", path, rr.Code, body)
		}
		if strings.Contains(logs.String(), `"level":"ERROR"`) {
			t.Errorf("%s: logged an error for a state that lasts until a repair: %s", path, logs.String())
		}
	}
	rr := factsGet(factsRouter(t, &stubDeviceRepo{item: item}, store), "/inventory/devices/web1/fact-chain")
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"break"`) {
		t.Errorf("the chain route: %d %s; want the break reported", rr.Code, rr.Body.String())
	}
}

// TestUnavailable_NamesOnlyStatesThatLast: the store's three configuration
// refusals and a stored row that does not open as the device's own, which
// lasts until a key is set right or the history is repaired, have a fixed
// cause, wrapped or not, and every other error has none.
func TestUnavailable_NamesOnlyStatesThatLast(t *testing.T) {
	for err, want := range map[error]bool{
		facts.ErrNoKeys: true,
		fmt.Errorf("facts: %w", entstore.ErrKeysChanged):            true,
		fmt.Errorf("%w: at row 3", entstore.ErrRekeyRefused):        true,
		fmt.Errorf("%w: it was edited", entstore.ErrNotThisDevices): true,
		entstore.ErrUnknownDevice:                                   false,
		fmt.Errorf("database is locked"):                            false,
	} {
		cause, ok := entstore.Unavailable(err)
		if ok != want || (cause == "") == want {
			t.Errorf("Unavailable(%v) = %q, %v; want a cause %v", err, cause, ok, want)
		}
		if strings.Contains(cause, err.Error()) {
			t.Errorf("Unavailable(%v) repeats the error's text: %q", err, cause)
		}
	}
}
