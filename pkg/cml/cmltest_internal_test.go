// Helpers the internal tests share for driving cmltest's controller
// through ClientFor, the path a cml_controller device's client takes.
package cml

import (
	"context"
	"net/http"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/cml/cmltest"
)

// connectFor logs in to ctl through ClientFor, as a task or the import
// does, with ctl's own pinned certificate and login.
func connectFor(t *testing.T, ctl *cmltest.Controller) *Client {
	t.Helper()
	c, _, _, err := ClientFor(context.Background(), cmltest.NewDevice(t, ctl), "cml", cmltest.NewContext(ctl).InjectSecrets())
	if err != nil {
		t.Fatalf("ClientFor: %v", err)
	}
	return c
}

// onCall sets ctl's OnCall under its lock, as its other settings are set.
func onCall(ctl *cmltest.Controller, f func(w http.ResponseWriter, r *http.Request) bool) {
	ctl.Mu.Lock()
	defer ctl.Mu.Unlock()
	ctl.OnCall = f
}
