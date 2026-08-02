package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/SubjectVoidLLC/the-pleiades/internal/auth"
	"github.com/SubjectVoidLLC/the-pleiades/internal/inventory"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// Dispatcher coordinates the execution of Playbooks against an inventory group.
type Dispatcher struct {
	repo inventory.Repository
	auth auth.Evaluator
	js   jetstream.JetStream
}

// NewDispatcher creates a new task dispatcher.
func NewDispatcher(repo inventory.Repository, evaluator auth.Evaluator, js jetstream.JetStream) *Dispatcher {
	return &Dispatcher{
		repo: repo,
		auth: evaluator,
		js:   js,
	}
}

// DispatchPayload is the structure of the message sent to NATS.
type DispatchPayload struct {
	PlaybookID string `json:"playbook_id"`
	DeviceName string `json:"device_name"`
	DeviceIP   string `json:"device_ip"`
}

// DispatchPlaybook executes a playbook against an inventory group.
// It iterates through the group, verifies authorization per device, and emits a NATS message.
func (d *Dispatcher) DispatchPlaybook(w http.ResponseWriter, r *http.Request) {
	// 1. Extract Identity from Context
	id, ok := r.Context().Value(identityKey).(*auth.Identity)
	if !ok || id == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	groupName := r.URL.Query().Get("group")
	playbookID := r.URL.Query().Get("playbook")

	if groupName == "" || playbookID == "" {
		http.Error(w, "Missing 'group' or 'playbook' query parameter", http.StatusBadRequest)
		return
	}

	// 2. Obtain an Iterator for the target group
	iter, err := d.repo.GetGroup(r.Context(), groupName)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to query inventory: %v", err), http.StatusInternalServerError)
		return
	}
	defer iter.Close()

	var dispatchedCount int
	var errCount int

	// 3. The NATS Push Loop
	for iter.Next(r.Context()) {
		device := iter.Item()
		
		// 4. Capability Check: Ensure the user is allowed to run playbooks
		// In a full implementation, we might check `playbook:execute:<device_type>`
		if err := d.auth.CheckAccess(r.Context(), id, "playbook:execute"); err != nil {
			errCount++
			continue // Skip unauthorized devices instead of failing the whole batch
		}

		// 5. Build and Publish NATS Payload
		payload := DispatchPayload{
			PlaybookID: playbookID,
			DeviceName: device.ID(),
			DeviceIP:   device.Properties()["ip"].(string),
		}

		data, _ := json.Marshal(payload)
		
		msg := &nats.Msg{
			Subject: "playbooks.dispatch",
			Data:    data,
		}

		// Use background context for publishing so we don't tie it to the HTTP request context completely
		if _, err := d.js.PublishMsg(context.Background(), msg); err != nil {
			errCount++
			continue
		}
		
		dispatchedCount++
	}

	if err := iter.Error(); err != nil {
		http.Error(w, fmt.Sprintf("Iterator failed during stream: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":     "dispatched",
		"dispatched": dispatchedCount,
		"failed":     errCount,
	})
}
