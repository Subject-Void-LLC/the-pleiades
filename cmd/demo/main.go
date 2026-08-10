package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// streamMockJob publishes numEvents fabricated wire.JobEvent values to
// topology.LogSubject(jobID), purely so this demo binary's own SSE log
// viewer has something to render. This is a UI-scaffolding fixture, not
// a production code path: it used to live as internal/ansible
// (ReceptorAdapter.StreamMockJob), an exported API inside a package
// Phase 17 (Legacy Ansible Adapter) turned into a real production
// adapter (internal/adapters/legacy). A mock event generator has no
// business staying importable from a production package once that
// package does real work, so it moved here, unexported, where this
// binary's own doc comment already establishes the "demo binary, not a
// second unguarded entry point" convention (FAILURE_PATTERNS.md #67).
func streamMockJob(ctx context.Context, bus event.Bus, jobID string, numEvents int) error {
	hosts := []string{"router-1", "router-2", "switch-a", "core-fw-1"}
	tasks := []string{"Gathering Facts", "Ensure configuration is present", "Write memory", "Verify OSPF adjacencies"}
	statuses := []string{"ok", "ok", "changed", "failed"}

	publish := func(evt wire.JobEvent) error {
		wrapped, err := event.WrapPayload(uuid.New().String(), "job.log", evt)
		if err != nil {
			return err
		}
		return bus.Publish(ctx, topology.LogSubject(jobID), *wrapped)
	}

	for i := 0; i < numEvents; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		evt := wire.JobEvent{Status: statuses[i%len(statuses)], Host: hosts[i%len(hosts)], Task: tasks[i%len(tasks)]}
		evt.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
		evt.EventData.Message = fmt.Sprintf("executed task %d successfully", i)
		if i%10 == 0 {
			evt.Status = "failed"
			evt.EventData.Message = "connection timed out"
		}
		if err := publish(evt); err != nil {
			return fmt.Errorf("failed to publish event %d for job %s: %w", i, jobID, err)
		}
		time.Sleep(1 * time.Millisecond)
	}

	eof := wire.JobEvent{Status: "task.completed"}
	eof.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	eof.EventData.Message = "mock job stream complete"
	if err := publish(eof); err != nil {
		return fmt.Errorf("failed to publish EOF event for job %s: %w", jobID, err)
	}
	return nil
}

// demoSecretLen matches auth.NewStaticKeyProvider's own HS256 minimum
// (RFC 7518 SS3.2), so this binary's own secret is never itself the
// reason a real code path would have rejected it.
const demoSecretLen = 32

// mintDemoToken builds a fresh, random HMAC secret, the real
// auth.Evaluator that verifies tokens signed against it, and one signed
// admin token this process's own operator can use immediately.
//
// This demo used to mount streamer.StreamLogs on a bare chi.NewRouter
// with no middleware at all: no auth, no tracing, no metrics, no rate
// limiting protected the one route it advertised, a live unauthenticated
// entry point recorded as FAILURE_PATTERNS.md #67. It now goes through
// api.NewRouter like every other binary in this repository, which means
// it needs a real evaluator and a real token to demonstrate that one
// route with, not a way around either.
func mintDemoToken() (auth.Evaluator, string, error) {
	secret := make([]byte, demoSecretLen)
	if _, err := rand.Read(secret); err != nil {
		return nil, "", fmt.Errorf("generating demo signing secret: %w", err)
	}
	provider, err := auth.NewStaticKeyProvider(secret)
	if err != nil {
		return nil, "", err
	}
	evaluator, err := auth.NewJWTEvaluator(provider, "pleiades-demo", "pleiades-demo")
	if err != nil {
		return nil, "", err
	}

	claims := jwt.MapClaims{
		"sub":  "demo-operator",
		"role": string(auth.RoleAdmin),
		"iss":  "pleiades-demo",
		"aud":  "pleiades-demo",
		"exp":  time.Now().Add(24 * time.Hour).Unix(),
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(secret)
	if err != nil {
		return nil, "", fmt.Errorf("signing demo token: %w", err)
	}
	return evaluator, token, nil
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// bus is used for publishing mock job log events (streamMockJob,
	// above), which also ensures the single Pleiades stream
	// (topology.EnsureStream) exists. A second, independent connection
	// below backs the raw jetstream.JetStream handle api.LogStreamer
	// needs for its own per-request consumer creation; the two
	// connections are a known, documented tradeoff (mirroring
	// internal/lock's own separate connection), not an oversight -- see
	// HANDOFF_DOCUMENT.md.
	bus, err := event.NewNatsBus(ctx, nats.DefaultURL)
	if err != nil {
		log.Fatalf("failed to connect event bus: %v", err)
	}

	nc, err := nats.Connect(nats.DefaultURL)
	if err != nil {
		log.Fatalf("failed to connect to nats: %v", err)
	}
	defer nc.Close()

	js, err := jetstream.New(nc)
	if err != nil {
		log.Fatalf("failed to get jetstream: %v", err)
	}

	streamer := api.NewLogStreamer(js)

	// A real UUID, not the literal "123" this used to be: every job ID
	// this platform mints is a UUID (api.Dispatcher's own uuid.New()), and
	// LogStreamer's own {id} boundary has required one since
	// FAILURE_PATTERNS.md #63, so the old literal 400'd against this
	// binary's own advertised URL from that phase onward.
	demoJobID := uuid.New().String()

	// Continually pump logs rapidly
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
				if err := streamMockJob(ctx, bus, demoJobID, 1); err != nil {
					log.Printf("mock job stream error: %v", err)
				}
				time.Sleep(100 * time.Millisecond)
			}
		}
	}()

	evaluator, token, err := mintDemoToken()
	if err != nil {
		log.Fatalf("failed to mint demo token: %v", err)
	}

	// One chain, two consumers, exactly as cmd/controller wires it: a demo
	// binary bypassing authorization while every real composition root
	// enforces it would itself be exactly the kind of second, unguarded
	// entry point this fix is closing. admission enforces and records;
	// hateoas advertises against the same rules, unrecorded.
	chain := auth.AdmissionChain{auth.NewTokenScopeRule(evaluator)}
	admission := auth.Admission{
		Chain:    chain,
		Recorder: auth.NewSlogRecorder(nil),
	}
	hateoas, err := auth.NewAdmissionHATEOASGenerator(chain)
	if err != nil {
		log.Fatalf("failed to build HATEOAS generator: %v", err)
	}

	r, err := api.NewRouter(api.RouterConfig{
		Auth:      api.AuthMiddleware(evaluator),
		Admission: admission,
		HATEOAS:   hateoas,
		Routes: []api.Route{
			{Method: http.MethodGet, Pattern: "/jobs/{id}/logs", Scope: auth.ScopeJobRead, Rel: auth.RelLogs, Handler: streamer.StreamLogs},
		},
	})
	if err != nil {
		log.Fatalf("failed to build router: %v", err)
	}

	srv := &http.Server{
		Addr:              ":8081",
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("Starting API server on :8081")
		log.Printf("try: curl -H 'Authorization: Bearer %s' http://localhost:8081/api/v1/jobs/%s/logs", token, demoJobID)
		if err := srv.ListenAndServe(); err != http.ErrServerClosed {
			log.Fatalf("Server failed: %v", err)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("graceful shutdown failed: %v", err)
	}
	if err := bus.Close(); err != nil {
		log.Printf("event bus drain failed: %v", err)
	}
}
