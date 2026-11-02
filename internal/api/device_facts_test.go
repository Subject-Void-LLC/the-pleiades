// Tests for a device's facts routes (device_facts.go).
package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/apispec"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	inventorytest "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
)

// factReader answers from fixed facts, by device ID, and says what it was
// asked.
type factReader struct {
	asked string
	err   error
}

func (r *factReader) Current(_ context.Context, device string) ([]facts.Entry, error) {
	r.asked = device
	return []facts.Entry{{Key: "ansible_kernel", Value: "6.1", Hash: "h", ObservedAt: time.Unix(0, 0).UTC(), LastSeenAt: time.Unix(60, 0).UTC()}}, r.err
}

func (r *factReader) History(_ context.Context, device, key string) ([]facts.Entry, error) {
	r.asked = device + "/" + key
	return nil, r.err
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
	rr = factsGet(router, "/inventory/devices/web1/facts/ansible_kernel")
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"facts":[]`) || reader.asked != string(item.ID())+"/ansible_kernel" {
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
