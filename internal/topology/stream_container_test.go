package topology_test

import (
	"context"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/topology"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/testcontainers/testcontainers-go"
	natscontainer "github.com/testcontainers/testcontainers-go/modules/nats"
	"github.com/testcontainers/testcontainers-go/wait"
)

// TestEnsureStream proves EnsureStream against a real NATS JetStream
// broker: this is the one CreateOrUpdateStream call site every other
// adapter (natsBus, cmd/controller, cmd/runner, cmd/demo) is meant to
// share, so its own package needs a direct proof, not just the indirect
// coverage event.NewNatsBus's own container tests already give it.
func TestEnsureStream(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ctx := context.Background()

	natsC, err := natscontainer.RunContainer(ctx,
		testcontainers.WithImage("nats:2.10"),
		testcontainers.WithCmd("-js"),
		testcontainers.WithWaitStrategy(wait.ForLog("Server is ready")),
	)
	if err != nil {
		t.Fatalf("failed to start container: %v", err)
	}
	defer natsC.Terminate(context.Background())

	url, err := natsC.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}

	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer nc.Close()
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatalf("jetstream: %v", err)
	}

	stream, err := topology.EnsureStream(ctx, js)
	if err != nil {
		t.Fatalf("EnsureStream: %v", err)
	}
	if stream == nil {
		t.Fatal("EnsureStream returned a nil stream with no error")
	}

	info, err := stream.Info(ctx)
	if err != nil {
		t.Fatalf("stream.Info: %v", err)
	}
	if info.Config.Name != topology.StreamName {
		t.Errorf("stream name = %q, want %q", info.Config.Name, topology.StreamName)
	}

	// Calling EnsureStream again (the "update" half of CreateOrUpdate,
	// exercised for real since every one of the real adapters/binaries
	// calls this on every startup, not just once ever) must not error.
	if _, err := topology.EnsureStream(ctx, js); err != nil {
		t.Fatalf("second EnsureStream call: %v", err)
	}
}
