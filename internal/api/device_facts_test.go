// Tests for a device's facts routes (device_facts.go).
package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/apispec"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/enttest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts/entstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	inventorytest "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"

	_ "github.com/mattn/go-sqlite3"
)

// factReader answers from fixed facts, by device ID, and says what it was
// asked.
type factReader struct {
	asked string
	err   error
}

func (r *factReader) Current(_ context.Context, device string) ([]facts.Entry, error) {
	r.asked = device
	return []facts.Entry{{Key: "os.kernel.release", Value: "6.1", Hash: "h", ObservedAt: time.Unix(0, 0).UTC(), LastSeenAt: time.Unix(60, 0).UTC()}}, r.err
}

func (r *factReader) History(_ context.Context, device, key string) ([]facts.Entry, error) {
	r.asked = device + "/" + key
	return nil, r.err
}

func (r *factReader) Verify(_ context.Context, device string) (facts.Verdict, error) {
	r.asked = device + " verify"
	return facts.Verdict{Values: 2, Head: "abc", Break: &facts.Break{Index: 1, Key: "os.kernel.release", Reason: "edited"}}, r.err
}

func factsRouter(t *testing.T, repo *stubDeviceRepo, reader api.FactReader) http.Handler {
	t.Helper()
	handler := api.NewDeviceHandler(repo, inventory.NewItemFactory(), slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if reader != nil {
		handler.WithFacts(reader)
	}
	router, err := api.NewRouter(api.RouterConfig{
		Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)), Auth: alwaysAuthenticated, Admission: &fakeAdmitter{}, HATEOAS: allowAllGenerator(t),
		Routes: []api.Route{
			{Method: apispec.ListDeviceFacts.Method, Pattern: apispec.ListDeviceFacts.Pattern, Scope: apispec.ListDeviceFacts.Scope, Rel: apispec.ListDeviceFacts.Rel, Handler: handler.Facts},
			{Method: apispec.GetDeviceFactHistory.Method, Pattern: apispec.GetDeviceFactHistory.Pattern, Scope: apispec.GetDeviceFactHistory.Scope, Rel: apispec.GetDeviceFactHistory.Rel, Handler: handler.FactHistory},
			{Method: apispec.VerifyDeviceFactChain.Method, Pattern: apispec.VerifyDeviceFactChain.Pattern, Scope: apispec.VerifyDeviceFactChain.Scope, Rel: apispec.VerifyDeviceFactChain.Rel, Handler: handler.FactChain},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return router
}

func factsGet(router http.Handler, path string) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, api.APIVersionPrefix+path, nil))
	return rr
}

// TestDeviceFacts_ReadsByDeviceIDAndAnswersEachOutcome: the facts are read by
// the device's ID, never its name, and each failure has its own status
// without the store's own text.
func TestDeviceFacts_ReadsByDeviceIDAndAnswersEachOutcome(t *testing.T) {
	item := &inventorytest.Stub{StubID: "8d1e3c2c-0670-40cc-a914-e1194ff5857e", StubName: "web1"}
	reader := &factReader{}
	router := factsRouter(t, &stubDeviceRepo{item: item}, reader)

	rr := factsGet(router, "/inventory/devices/web1/facts")
	var body struct {
		Device string        `json:"device"`
		Facts  []facts.Entry `json:"facts"`
		Links  []any         `json:"_links"`
	}
	if rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &body) != nil || body.Device != "web1" || len(body.Facts) != 1 || body.Links == nil {
		t.Fatalf("current: %d %s", rr.Code, rr.Body.String())
	}
	if reader.asked != string(item.ID()) {
		t.Errorf("the reader was asked for %q, want the device's ID", reader.asked)
	}
	rr = factsGet(router, "/inventory/devices/web1/facts/os.kernel.release")
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"facts":[]`) || reader.asked != string(item.ID())+"/os.kernel.release" {
		t.Errorf("history: %d %s (asked %q)", rr.Code, rr.Body.String(), reader.asked)
	}
	if rr := factsGet(router, "/inventory/devices/web1/facts/bad%20key"); rr.Code != http.StatusBadRequest || strings.Contains(rr.Body.String(), "bad key") {
		t.Errorf("a key that is not a fact name: %d %s", rr.Code, rr.Body.String())
	}
	// One past the handler's 253-byte device name limit (a DNS name's).
	long := strings.Repeat("d", 254)
	if rr := factsGet(router, "/inventory/devices/"+long+"/facts"); rr.Code != http.StatusBadRequest || strings.Contains(rr.Body.String(), long) {
		t.Errorf("a device name past the limit: %d, echoed: %v", rr.Code, strings.Contains(rr.Body.String(), long))
	}
	missing := factsRouter(t, &stubDeviceRepo{getErr: inventory.ErrItemNotFound}, reader)
	if rr := factsGet(missing, "/inventory/devices/nope/facts"); rr.Code != http.StatusNotFound {
		t.Errorf("a missing device: %d", rr.Code)
	}
	broken := factsRouter(t, &stubDeviceRepo{item: item}, &factReader{err: errors.New("pq: relation facts at db.internal:5432")})
	if rr := factsGet(broken, "/inventory/devices/web1/facts"); rr.Code != http.StatusInternalServerError || strings.Contains(rr.Body.String(), "db.internal") {
		t.Errorf("a broken store: %d %s", rr.Code, rr.Body.String())
	}
	if rr := factsGet(factsRouter(t, &stubDeviceRepo{item: item}, nil), "/inventory/devices/web1/facts"); rr.Code != http.StatusNotImplemented {
		t.Errorf("no reader: %d", rr.Code)
	}
}

// TestDeviceFactHistory_ReadsAKeyANewFactMayNotTake: a key stored before the
// name rule, holding the Hangul filler U+3164 (a printable letter, so well
// formed, which a new fact may no longer take), is still read back by its
// address: a reader holds a stored key to the shape every stored key has
// had, never to the rule a new one is held to (LESSONS 415).
func TestDeviceFactHistory_ReadsAKeyANewFactMayNotTake(t *testing.T) {
	key := "pkg.k" + string(rune(0x3164)) + "1"
	if facts.ValidKey(key) {
		t.Fatalf("ValidKey(%q) = true; the test needs a key a new fact may not take", key)
	}
	item := &inventorytest.Stub{StubID: "8d1e3c2c-0670-40cc-a914-e1194ff5857e", StubName: "web1"}
	reader := &factReader{}
	router := factsRouter(t, &stubDeviceRepo{item: item}, reader)
	rr := factsGet(router, "/inventory/devices/web1/facts/"+url.PathEscape(key))
	if rr.Code != http.StatusOK || reader.asked != string(item.ID())+"/"+key {
		t.Errorf("history of a stored key ValidKey refuses: %d %s (asked %q)", rr.Code, rr.Body.String(), reader.asked)
	}
}

// TestDeviceFactHistory_NamesBackOnlyAKeyThatShowsAsItself: the reader's
// key rule is frozen and reads a key holding a direction override or a line
// separator. No ValidKey under any Go release accepts such a character (a
// format character or a line separator), so a key holding one is stored only
// by a hand edit; the keys a writer of another toolchain can legitimately
// store that this one refuses hold a code point this one has not assigned.
// Asked for such a key that the store does not hold, the answer is an empty
// history that does not repeat the caller's text, since it would not show as
// itself (FAILURE_PATTERNS #72); asked for one the store holds, the stored
// values come back under the key as stored. Through the CLI's real file
// store here, and through the store the Controller serves in
// TestDeviceFactHistory_ReadsAStoredKeyFromTheControllersStore.
func TestDeviceFactHistory_NamesBackOnlyAKeyThatShowsAsItself(t *testing.T) {
	item := &inventorytest.Stub{StubID: "8d1e3c2c-0670-40cc-a914-e1194ff5857e", StubName: "web1"}
	store, err := facts.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	stored := "pkg.a" + string(rune(0x2028)) + "b"
	recordKey(t, store, string(item.ID()), stored)
	router := factsRouter(t, &stubDeviceRepo{item: item}, store)
	historyNamesBackOnlyWhatShows(t, router, stored)
}

// TestDeviceFactHistory_ReadsAStoredKeyFromTheControllersStore: the same
// route over the store the Controller serves (internal/facts/entstore, on a
// real SQLite file), with a stored key holding U+2028: its history reads, and
// a key holding a direction override that nothing stored is not named back.
func TestDeviceFactHistory_ReadsAStoredKeyFromTheControllersStore(t *testing.T) {
	dsn := fmt.Sprintf("file:%s?_journal_mode=WAL&_busy_timeout=10000&_fk=1", filepath.Join(t.TempDir(), "facts.db"))
	client := enttest.Open(t, "sqlite3", dsn)
	t.Cleanup(func() { _ = client.Close() })
	dev := client.Device.Create().SetName("web1").SetType("linux_server").SetProperties(map[string]any{"host": "10.0.0.1"}).SaveX(context.Background())
	item := &inventorytest.Stub{StubID: pkginventory.DeviceID(dev.DeviceID), StubName: "web1"}
	store := entstore.New(client)
	stored := "pkg.a" + string(rune(0x2028)) + "b"
	recordKey(t, store, dev.DeviceID, stored)
	router := factsRouter(t, &stubDeviceRepo{item: item}, store)
	historyNamesBackOnlyWhatShows(t, router, stored)
}

// recordKey stores one value under key on device, through store's Record,
// which checks no key, as a writer of another build or a hand edit leaves it.
func recordKey(t *testing.T, store facts.Store, device, key string) {
	t.Helper()
	hash, err := facts.Hash(key, "1")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Record(context.Background(), device, facts.Observation{
		Entries: []facts.Entry{{Key: key, Value: "1", Hash: hash}}, At: time.Now()}); err != nil {
		t.Fatal(err)
	}
}

// historyNamesBackOnlyWhatShows holds the history route over router to the
// rule: stored is read back under itself, a key nothing stored that would
// not show as itself is not named back, and an ordinary one is.
func historyNamesBackOnlyWhatShows(t *testing.T, router http.Handler, stored string) {
	t.Helper()
	for _, r := range []rune{0x202E, 0x2028, 0x200B} {
		asked := "pkg.a" + string(r) + "c"
		rr := factsGet(router, "/inventory/devices/web1/facts/"+url.PathEscape(asked))
		if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"facts":[]`) {
			t.Errorf("%U: history of a key nothing stored: %d %s", r, rr.Code, rr.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		// The self link names it percent-escaped (recordname.Segment), which
		// shows as itself; only the raw character or the key field would not.
		if _, named := body["key"]; named || strings.Contains(rr.Body.String(), string(r)) {
			t.Errorf("%U: the answer names the caller's key back: %s", r, rr.Body.String())
		}
	}
	rr := factsGet(router, "/inventory/devices/web1/facts/"+url.PathEscape(stored))
	var body struct {
		Key   string        `json:"key"`
		Facts []facts.Entry `json:"facts"`
	}
	if rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &body) != nil || len(body.Facts) != 1 || body.Facts[0].Key != stored || body.Key != stored {
		t.Errorf("history of a stored key: %d %s", rr.Code, rr.Body.String())
	}
	if rr := factsGet(router, "/inventory/devices/web1/facts/os.family"); rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"key":"os.family"`) {
		t.Errorf("history of an ordinary key nothing stored: %d %s", rr.Code, rr.Body.String())
	}
}

// TestDeviceFactChain_ReportsTheVerdictByDeviceID: the chain is checked by
// the device's ID, a break comes back in the body with a 200, and each
// failure has its own status without the store's own text.
func TestDeviceFactChain_ReportsTheVerdictByDeviceID(t *testing.T) {
	item := &inventorytest.Stub{StubID: "8d1e3c2c-0670-40cc-a914-e1194ff5857e", StubName: "web1"}
	reader := &factReader{}
	rr := factsGet(factsRouter(t, &stubDeviceRepo{item: item}, reader), "/inventory/devices/web1/fact-chain")
	var body struct {
		Device string       `json:"device"`
		Values int          `json:"values"`
		Head   string       `json:"head"`
		Break  *facts.Break `json:"break"`
		Links  []any        `json:"_links"`
	}
	if rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &body) != nil || body.Device != "web1" || body.Values != 2 ||
		body.Head != "abc" || body.Break == nil || body.Break.Index != 1 || body.Links == nil {
		t.Fatalf("chain: %d %s", rr.Code, rr.Body.String())
	}
	if reader.asked != string(item.ID())+" verify" {
		t.Errorf("the reader was asked for %q, want the device's ID", reader.asked)
	}
	if rr := factsGet(factsRouter(t, &stubDeviceRepo{getErr: inventory.ErrItemNotFound}, reader), "/inventory/devices/nope/fact-chain"); rr.Code != http.StatusNotFound {
		t.Errorf("a missing device: %d", rr.Code)
	}
	if rr := factsGet(factsRouter(t, &stubDeviceRepo{item: item}, nil), "/inventory/devices/web1/fact-chain"); rr.Code != http.StatusNotImplemented {
		t.Errorf("no reader: %d", rr.Code)
	}
	long := strings.Repeat("d", 254)
	if rr := factsGet(factsRouter(t, &stubDeviceRepo{item: item}, reader), "/inventory/devices/"+long+"/fact-chain"); rr.Code != http.StatusBadRequest {
		t.Errorf("a device name past the limit: %d", rr.Code)
	}
	broken := factsRouter(t, &stubDeviceRepo{item: item}, &factReader{err: errors.New("pq: relation facts at db.internal:5432")})
	if rr := factsGet(broken, "/inventory/devices/web1/fact-chain"); rr.Code != http.StatusInternalServerError || strings.Contains(rr.Body.String(), "db.internal") {
		t.Errorf("a broken store: %d %s", rr.Code, rr.Body.String())
	}
}
