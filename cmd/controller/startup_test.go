// Tests for the pieces a controller runs around its own start and stop: the
// startup handler that answers probes while the database migrates
// (startuphandler.go), the shutdown drain (drain.go), and `controller version`
// (version.go).
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestStartupHandler_AnswersAsAliveAndNotReadyUntilServed pins what the port
// says while the database is being migrated, and that it hands over to the
// router completely once there is one.
func TestStartupHandler_AnswersAsAliveAndNotReadyUntilServed(t *testing.T) {
	h := &startupHandler{}
	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec
	}

	if rec := get("/healthz"); rec.Code != http.StatusOK {
		t.Errorf("/healthz while starting = %d; want 200, the process is alive", rec.Code)
	}
	rec := get("/readyz")
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("/readyz while starting = %d; want 503", rec.Code)
	}
	var body struct {
		Status string            `json:"status"`
		Checks map[string]string `json:"checks"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Checks["startup"] != "failed" {
		t.Errorf("/readyz body while starting = %q (%v); want the startup check named as failed", rec.Body.String(), err)
	}
	if rec := get("/api/v1/devices"); rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") == "" {
		t.Errorf("an API call while starting = %d, Retry-After %q; want 503 with a retry hint", rec.Code, rec.Header().Get("Retry-After"))
	}

	h.serve(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	for _, path := range []string{"/healthz", "/readyz", "/api/v1/devices"} {
		if rec := get(path); rec.Code != http.StatusTeapot {
			t.Errorf("%s after the router was served = %d; want it answered by the router", path, rec.Code)
		}
	}
}

// TestNewDrain_ReadsAndBoundsTheSetting covers SHUTDOWN_DRAIN.
func TestNewDrain_ReadsAndBoundsTheSetting(t *testing.T) {
	tests := []struct {
		value   string
		want    time.Duration
		wantErr bool
	}{
		{"", defaultDrain, false},
		{"0s", 0, false},
		{"20s", 20 * time.Second, false},
		{"soon", 0, true},
		{"-1s", 0, true},
		{"2h", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			t.Setenv("SHUTDOWN_DRAIN", tt.value)
			d, err := newDrain()
			if (err != nil) != tt.wantErr {
				t.Fatalf("newDrain() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil && d.wait != tt.want {
				t.Errorf("drain = %s; want %s", d.wait, tt.want)
			}
		})
	}
}

// TestDrain_FailsReadinessForItsLength proves the drain stops being ready at
// once, and waits its length before returning.
func TestDrain_FailsReadinessForItsLength(t *testing.T) {
	d := &drain{wait: 200 * time.Millisecond}
	check := d.check()
	if err := check.Probe(context.Background()); err != nil {
		t.Fatalf("readiness before the drain = %v; want ready", err)
	}
	started := time.Now()
	d.begin(context.Background())
	if waited := time.Since(started); waited < 200*time.Millisecond {
		t.Errorf("the drain returned after %s; want its full 200ms", waited)
	}
	if err := check.Probe(context.Background()); err == nil || !strings.Contains(err.Error(), "shutting down") {
		t.Errorf("readiness during the drain = %v; want it failing", err)
	}
}

// TestRunVersion prints the build's version and succeeds.
func TestRunVersion(t *testing.T) {
	var out strings.Builder
	if code := runVersion(&out); code != 0 || strings.TrimSpace(out.String()) == "" {
		t.Errorf("runVersion() = %d, %q; want 0 and a version", code, out.String())
	}
}
