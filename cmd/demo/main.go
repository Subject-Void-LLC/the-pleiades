package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/SubjectVoidLLC/the-pleiades/internal/ansible"
	"github.com/SubjectVoidLLC/the-pleiades/internal/api"
	"github.com/SubjectVoidLLC/the-pleiades/internal/event"
	"github.com/go-chi/chi/v5"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// bus is used for publishing mock job log events (ansible.ReceptorAdapter),
	// which also ensures the single Pleiades stream (topology.EnsureStream)
	// exists. A second, independent connection below backs the raw
	// jetstream.JetStream handle api.LogStreamer needs for its own
	// per-request consumer creation; the two connections are a known,
	// documented tradeoff (mirroring internal/lock's own separate
	// connection), not an oversight -- see HANDOFF_DOCUMENT.md.
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

	adapter := ansible.NewReceptorAdapter(bus)
	streamer := api.NewLogStreamer(js)

	const demoJobID = "123"

	// Continually pump logs rapidly
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
				if err := adapter.StreamMockJob(ctx, demoJobID, 1); err != nil {
					log.Printf("mock job stream error: %v", err)
				}
				time.Sleep(100 * time.Millisecond)
			}
		}
	}()

	r := chi.NewRouter()
	r.Get("/api/v1/jobs/{id}/logs", streamer.StreamLogs)

	srv := &http.Server{
		Addr:              ":8081",
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("Starting API server on :8081 (try /api/v1/jobs/%s/logs)", demoJobID)
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
