// Package gnmi_test: a STREAM subscription's cost per update end to end,
// over a real loopback gRPC connection to gnmitest's server.
package gnmi_test

import (
	"context"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/gnmi"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/gnmi/gnmitest"
)

// BenchmarkSubscribeStream_PerUpdate receives b.N STREAM updates of one leaf
// sampled every microsecond. Its allocations are the process's, so they
// include the in-process server's own sending; BenchmarkStreamUpdate_Decode
// is the client's decoding alone.
func BenchmarkSubscribeStream_PerUpdate(b *testing.B) {
	s := gnmitest.New(b, gnmitest.Options{})
	s.Put(b, "/interfaces/interface[name=e1]/state/counters/in-pkts", `12345`)
	c, _, err := gnmi.ClientFor(gnmitest.NewDevice(b, s), "bench", nil)
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	p, err := gnmi.ParsePath("/interfaces/interface[name=e1]/state/counters/in-pkts")
	if err != nil {
		b.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	b.ReportAllocs()
	got := 0
	b.ResetTimer()
	err = c.SubscribeStream(ctx, gnmi.StreamRequest{
		SubscribeRequest: gnmi.SubscribeRequest{Paths: []gnmi.Path{p}, Encoding: gnmi.EncodingJSONIETF},
		Mode:             gnmi.StreamSample, SampleInterval: time.Microsecond,
	}, func(ev gnmi.StreamEvent) error {
		if ev.Sync {
			return nil
		}
		got++
		if got >= b.N {
			return gnmi.ErrStop
		}
		return nil
	})
	b.StopTimer()
	if err != nil {
		b.Fatal(err)
	}
}
