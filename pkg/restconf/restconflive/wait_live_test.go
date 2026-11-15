// Package restconflive: wait.restconf against rousette: a ready server is
// ready on the first try, and a wrong password ends the wait at once.
package restconflive

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/wait"
)

// badLogin is liveRecorder's run context with a password rousette refuses.
type badLogin struct{ *liveRecorder }

// InjectSecrets implements sdk.RunbookContext.
func (badLogin) InjectSecrets() map[string]string {
	return map[string]string{"username": notconfUser, "password": "not-the-password"}
}

func TestLive_WaitRestconf(t *testing.T) {
	s := start(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	rc := &liveRecorder{stats: map[string]any{}}
	if _, err := wait.Restconf(ctx, rc, liveDevice(s), map[string]any{"timeout": 60, "sleep": 1}); err != nil {
		t.Fatalf("a ready rousette: %v", err)
	}
	if rc.stats["root"] != "/restconf" || rc.stats["tries"] != 1 {
		t.Errorf("stats %v, want the root on the first try", rc.stats)
	}
	_, err := wait.Restconf(ctx, badLogin{&liveRecorder{stats: map[string]any{}}}, liveDevice(s), map[string]any{"timeout": 60, "sleep": 1})
	if err == nil || !strings.Contains(err.Error(), "stopped at try 1") || !strings.Contains(err.Error(), "access-denied") {
		t.Errorf("a wrong password: %v", err)
	}
}
