package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/api"
	"github.com/SubjectVoidLLC/the-pleiades/internal/auth"
	"github.com/SubjectVoidLLC/the-pleiades/internal/inventory"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"errors"
)

// --- Mocks ---

type MockRepository struct {
	Count int
}

func (m *MockRepository) GetGroup(ctx context.Context, groupName string) (inventory.Iterator, error) {
	return &MockIterator{count: m.Count, current: 0}, nil
}

type MockIterator struct {
	count   int
	current int
}

func (i *MockIterator) Next(ctx context.Context) bool {
	if i.current < i.count {
		i.current++
		return true
	}
	return false
}

type MockDevice struct {
	Name  string
	Props map[string]interface{}
}

func (m *MockDevice) ID() string                       { return m.Name }
func (m *MockDevice) Properties() map[string]interface{} { return m.Props }
func (m *MockDevice) Tags() []string                     { return nil }
func (m *MockDevice) HasCapability(c string) bool        { return true }

func (i *MockIterator) Item() inventory.InventoryItem {
	return &MockDevice{
		Name:  "test-device",
		Props: map[string]interface{}{"ip": "10.0.0.1"},
	}
}

func (i *MockIterator) Error() error { return nil }
func (i *MockIterator) Close() error { return nil }

type MockAuthEvaluator struct {
	Allow bool
}

func (m *MockAuthEvaluator) ValidateToken(ctx context.Context, tokenStr string) (*auth.Identity, error) {
	return &auth.Identity{Subject: "user"}, nil
}

func (m *MockAuthEvaluator) CheckAccess(ctx context.Context, id *auth.Identity, requiredScopes ...string) error {
	if m.Allow {
		return nil
	}
	return errors.New("unauthorized")
}

type MockJetStream struct {
	jetstream.JetStream // embed interface to satisfy unimplemented methods
	Publishes int
}

func (m *MockJetStream) PublishMsg(ctx context.Context, msg *nats.Msg, opts ...jetstream.PublishOpt) (*jetstream.PubAck, error) {
	m.Publishes++
	return &jetstream.PubAck{}, nil
}

func TestDispatcher_ReleaseGate(t *testing.T) {
	repo := &MockRepository{Count: 10000} // 10,000 devices!
	eval := &MockAuthEvaluator{Allow: true}
	js := &MockJetStream{}

	dispatcher := api.NewDispatcher(repo, eval, js)

	req := httptest.NewRequest("POST", "/dispatch?group=routers&playbook=pb-1", nil)
	// Inject the mock identity that AuthMiddleware normally would
	ctx := context.WithValue(req.Context(), api.IdentityKeyForTest, &auth.Identity{Subject: "user"})
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	dispatcher.DispatchPlaybook(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %v", rr.Code)
	}

	var resp map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &resp)

	if int(resp["dispatched"].(float64)) != 10000 {
		t.Errorf("expected 10000 messages to be dispatched, got %v", resp["dispatched"])
	}

	if js.Publishes != 10000 {
		t.Errorf("expected JetStream PublishMsg to be called 10000 times, got %d", js.Publishes)
	}
}
