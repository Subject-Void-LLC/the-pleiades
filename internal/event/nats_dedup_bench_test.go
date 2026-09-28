// Phase 96c's benchmark: what the broker's producer-side dedup table costs
// at the window the outage budget derives, against the two minute window
// it replaced.
//
// Every publish through event.Bus carries a Nats-Msg-Id, log lines
// included, and there is one stream, so the broker keeps one table entry
// per message for as long as the stream's Duplicates window. The table's
// size is therefore rate x window x the cost of one entry, and the window
// is the part 96c changed (two minutes to DerivedDuplicateWindow of the
// default budget, five). This measures the one number that is not already
// known, the cost of an entry, and the publish time at each window.
package event_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
)

// dedupBenchPayload is the size of a typical job log event on the stream,
// so each stored message costs what a real one does and only the id's
// table entry differs between the two arms.
const dedupBenchPayload = 512

// BenchmarkDedupTable publishes b.N messages to a fresh broker per arm,
// with and without a message id, at the old window and the derived one.
//
// It publishes through a raw jetstream handle rather than event.Bus,
// because the bus always sets an id and the arm without one is the control
// that isolates the table. The subject is on the one stream, as a log
// line's is. Run it with a fixed count, since the memory figure is only
// meaningful for a known number of entries:
//
//	go test ./internal/event -run '^$' -bench BenchmarkDedupTable -benchtime 100000x -count 1
//
// server-B/msg is the growth of the broker's resident memory per message;
// the difference between an arm with ids and the same window without them
// is the cost of one table entry.
func BenchmarkDedupTable(b *testing.B) {
	derived := topology.DerivedDuplicateWindow(topology.DefaultOutageBudget)
	for _, window := range []time.Duration{2 * time.Minute, derived} {
		for _, withID := range []bool{false, true} {
			b.Run(fmt.Sprintf("window=%v/msgid=%v", window, withID), func(b *testing.B) {
				benchDedupTable(b, window, withID)
			})
		}
	}
}

// benchDedupTable is one arm of BenchmarkDedupTable.
func benchDedupTable(b *testing.B, window time.Duration, withID bool) {
	ctx := context.Background()
	broker := testsupport.StartNATS(b, testsupport.WithNATSExposedPorts("4222", "8222"))

	nc, err := nats.Connect(broker.URL())
	if err != nil {
		b.Fatalf("connect: %v", err)
	}
	defer nc.Close()
	js, err := jetstream.New(nc)
	if err != nil {
		b.Fatalf("jetstream: %v", err)
	}

	// The shipped stream, with only the window changed, so MaxAge and
	// storage are what a deployment runs.
	cfg := topology.StreamConfig(topology.DefaultOutageBudget)
	cfg.Duplicates = window
	if _, err := js.CreateStream(ctx, cfg); err != nil {
		b.Fatalf("create stream: %v", err)
	}

	payload := make([]byte, dedupBenchPayload)
	memBefore := brokerMemory(b, broker)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		msg := &nats.Msg{Subject: "pleiades.bench.dedup.log", Data: payload}
		var opts []jetstream.PublishOpt
		if withID {
			// A fresh id per message, as every log event gets one.
			opts = append(opts, jetstream.WithMsgID(uuid.New().String()))
		}
		if _, err := js.PublishMsg(ctx, msg, opts...); err != nil {
			b.Fatalf("publish %d: %v", i, err)
		}
	}
	b.StopTimer()

	growth := brokerMemory(b, broker) - memBefore
	perMsg := float64(growth) / float64(b.N)
	nsPerOp := float64(b.Elapsed().Nanoseconds()) / float64(b.N)
	b.ReportMetric(perMsg, "server-B/msg")
	b.Logf("[MEASURED] window=%v msgid=%v: %d messages, broker grew %.1f MiB (%.0f B/msg), %.0f ns/publish (~%.0f msgs/sec)",
		window, withID, b.N, float64(growth)/(1<<20), perMsg, nsPerOp, 1e9/nsPerOp)
}

// brokerMemory reads the broker's resident memory from its own monitoring
// endpoint, /varz's "mem", in bytes.
func brokerMemory(b *testing.B, broker *testsupport.NATSBroker) int64 {
	b.Helper()
	resp, err := http.Get("http://" + broker.Endpoint("8222") + "/varz")
	if err != nil {
		b.Fatalf("varz: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var varz struct {
		Mem int64 `json:"mem"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&varz); err != nil {
		b.Fatalf("decode varz: %v", err)
	}
	return varz.Mem
}
