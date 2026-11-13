// Stress and benchmark for the endpoint circuit: many callers and a wait on
// one dead endpoint at once, under -race, and what a call pays for the
// circuit when nothing is wrong.
package winrmexec

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport/refusing"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/breaker"
)

// TestEndpoints_ManyCallersAndAWaitOnADeadHost runs sixteen callers of ten
// calls each against one closed port while a wait tries it beside them.
// The circuit opens and stays open, the calls after it are refused with
// nothing sent, and the wait is never refused, whatever the interleaving.
func TestEndpoints_ManyCallersAndAWaitOnADeadHost(t *testing.T) {
	b := useCircuits(t, breaker.DefaultThreshold, time.Hour)
	target := Target{Host: "127.0.0.1", Port: refusing.Port(t)}
	var sent, refused atomic.Int32
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 10; i++ {
				if err := probe(target); errors.Is(err, breaker.ErrOpen) {
					refused.Add(1)
				} else {
					sent.Add(1)
				}
			}
		}()
	}
	waitErrs := make(chan error, 20)
	wg.Add(1)
	go func() {
		defer wg.Done()
		w := NewWaiter(target, passwordAuth, Options{Timeout: time.Second})
		w.sleep = func(context.Context, time.Duration) error { return nil }
		for i := 0; i < 20; i++ {
			waitErrs <- w.Probe(context.Background())
		}
		close(waitErrs)
	}()
	wg.Wait()
	for err := range waitErrs {
		if !errors.Is(err, ErrNotServing) {
			t.Fatalf("the wait got %v, want not serving yet: a wait is never refused", err)
		}
	}
	if b.Permitted(Addr(target, passwordAuth, Options{})) {
		t.Fatal("160 failed calls left the circuit closed")
	}
	if sent.Load() < breaker.DefaultThreshold || refused.Load() == 0 || sent.Load()+refused.Load() != 160 {
		t.Fatalf("sent %d, refused %d of 160: want at least %d sent and the rest refused", sent.Load(), refused.Load(), breaker.DefaultThreshold)
	}
	t.Logf("sent %d, refused %d: the first wave passes a closed circuit together, then it refuses", sent.Load(), refused.Load())
}

// BenchmarkEndpoints_AdmitAndRecord is what one call pays for the circuit
// on a healthy endpoint: a claim and a recorded success, under one mutex.
// The WinRM round trip it guards is milliseconds at best (an NTLM
// handshake and a sealed message per post), so a cost in nanoseconds is
// noise beside it.
func BenchmarkEndpoints_AdmitAndRecord(b *testing.B) {
	old := endpoints.swap(breaker.New(breaker.DefaultThreshold, breaker.DefaultCooldown))
	defer endpoints.swap(old)
	ctx := context.Background()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if err := endpoints.admit(ctx, "win1:5985"); err != nil {
			b.Fatal(err)
		}
		endpoints.record("win1:5985", nil)
	}
}
