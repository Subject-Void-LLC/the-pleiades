package remoteexec

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh"
)

// TestConnect_HopChain_StressManyConcurrentSessions opens and tears down
// several hundred concurrent two-hop chained sessions against the real
// mechanism (real TCP, real SSH handshakes, real direct-tcpip
// forwarding), all sharing one Runner and therefore one circuit breaker
// per address, under -race. This is the concurrency-safety proof
// Phase 72's own Fuzz/Stress Test item asks for: nothing here should
// race, deadlock, or leave a connection unclosed under real concurrent
// load.
func TestConnect_HopChain_StressManyConcurrentSessions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}

	const sessions = 300

	bastionAddr, bastionKey := startFakeSSHListener(t, func(cmd string) (string, string, int) {
		return "bastion should never run a command directly", "", 1
	})
	targetAddr, targetKey := startFakeSSHListener(t, func(cmd string) (string, string, int) {
		return "target-reached:" + cmd, "", 0
	})

	knownHostsPath := writeMultiKnownHosts(t, map[string]ssh.PublicKey{
		bastionAddr: bastionKey,
		targetAddr:  targetKey,
	})

	// Shared Runner, shared breaker state: this is what a Collection
	// method sharing one Runner via Shared actually does under real
	// concurrent task execution, so proving it here (rather than a fresh
	// Runner per goroutine) is the representative shape.
	r := New(Options{KnownHostsPath: knownHostsPath, MaxRetries: 1})

	bastionHost, bastionPort := splitHostPortT(t, bastionAddr)
	targetHost, targetPort := splitHostPortT(t, targetAddr)
	hops := []Hop{{Target: Target{Host: bastionHost, Port: bastionPort}, Auth: testAuth}}
	target := Target{Host: targetHost, Port: targetPort}

	var wg sync.WaitGroup
	errs := make(chan error, sessions)
	for i := 0; i < sessions; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			result, err := r.Run(context.Background(), hops, target, testAuth, fmt.Sprintf("echo session-%d", n))
			if err != nil {
				errs <- fmt.Errorf("session %d: %w", n, err)
				return
			}
			want := fmt.Sprintf("target-reached:echo session-%d", n)
			if result.Stdout != want {
				errs <- fmt.Errorf("session %d: stdout = %q, want %q", n, result.Stdout, want)
			}
		}(i)
	}
	wg.Wait()
	close(errs)

	var failed int
	for err := range errs {
		failed++
		if failed <= 5 {
			t.Error(err)
		}
	}
	if failed > 0 {
		t.Fatalf("%d of %d concurrent hop-chained sessions failed", failed, sessions)
	}
}

// BenchmarkConnect_HopChainDepth measures per-Connect latency at 0, 1 and
// 2 hops against the real fake-server mechanism, so a longer chain's
// cost is a real number an operator can plan against rather than a claim
// of "some overhead."
func BenchmarkConnect_HopChainDepth(b *testing.B) {
	bastionAAddr, bastionAKey := startFakeSSHListener(b, func(string) (string, string, int) { return "", "", 0 })
	bastionBAddr, bastionBKey := startFakeSSHListener(b, func(string) (string, string, int) { return "", "", 0 })
	targetAddr, targetKey := startFakeSSHListener(b, func(string) (string, string, int) { return "", "", 0 })

	knownHostsPath := writeMultiKnownHosts(b, map[string]ssh.PublicKey{
		bastionAAddr: bastionAKey,
		bastionBAddr: bastionBKey,
		targetAddr:   targetKey,
	})

	aHost, aPort := splitHostPortT(b, bastionAAddr)
	bHost, bPort := splitHostPortT(b, bastionBAddr)
	tHost, tPort := splitHostPortT(b, targetAddr)
	target := Target{Host: tHost, Port: tPort}

	depths := []struct {
		name string
		hops []Hop
	}{
		{"Direct", nil},
		{"OneHop", []Hop{{Target: Target{Host: aHost, Port: aPort}, Auth: testAuth}}},
		{"TwoHops", []Hop{
			{Target: Target{Host: aHost, Port: aPort}, Auth: testAuth},
			{Target: Target{Host: bHost, Port: bPort}, Auth: testAuth},
		}},
	}

	for _, d := range depths {
		b.Run(d.name, func(b *testing.B) {
			r := New(Options{KnownHostsPath: knownHostsPath, MaxRetries: 1})
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				conn, err := r.Connect(context.Background(), d.hops, target, testAuth)
				if err != nil {
					b.Fatalf("Connect: %v", err)
				}
				conn.Close()
			}
		})
	}
}
