// What this package costs at start-up, measured rather than assumed.
//
// Ensure runs on the critical path of every controller start, so the question
// "did making concurrent starts work make booting slow?" deserves a number
// rather than an opinion. It also corrects one that was wrong: the design
// this replaced justified a 20ms first backoff by asserting that the work it
// guarded was "about a millisecond", and on the machine that wrote that
// sentence BenchmarkEnsureGenerate measures roughly twenty. The key
// generation really is about a millisecond; the three fsyncs around it are
// not, and a schedule built on the wrong one of those two numbers is a
// schedule built on nothing. There is no schedule any more, and this file is
// where a claim about timing has to come from.
//
// The comparison that matters is against what a deployment does instead. An
// operator provisioning a certificate by hand runs an ACME client or an
// openssl invocation, both of which are tens of milliseconds at best and a
// network round trip at worst. The reuse path, which is the one that runs on
// every restart after the first, is a few hundred microseconds.
package tlscert_test

import (
	"path/filepath"
	"strconv"
	"sync"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/tlscert"
)

// BenchmarkEnsureReuse is the ordinary restart: a certificate is already
// there and nothing has to be written.
func BenchmarkEnsureReuse(b *testing.B) {
	dir := filepath.Join(b.TempDir(), "tls")
	if _, err := tlscert.Ensure(dir, tlscert.Options{}); err != nil {
		b.Fatalf("Ensure: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := tlscert.Ensure(dir, tlscert.Options{}); err != nil {
			b.Fatalf("Ensure: %v", err)
		}
	}
}

// BenchmarkEnsureGenerate is the first start on a fresh volume, which is
// dominated by the P-256 key generation and the signing rather than by
// anything this package added.
func BenchmarkEnsureGenerate(b *testing.B) {
	root := b.TempDir()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dir := filepath.Join(root, strconv.Itoa(i))
		if _, err := tlscert.Ensure(dir, tlscert.Options{}); err != nil {
			b.Fatalf("Ensure: %v", err)
		}
	}
}

// BenchmarkEnsureContended is the stress case: eight controllers starting
// against one directory at the same instant, which is what a Deployment with
// replicas on a shared volume does.
//
// It measures the whole convoy, and the whole convoy is now bounded by the
// slowest single generation rather than by any waiting, because nobody waits
// for anybody. This is the number that would regress if a future change put
// a lock, a sleep or a retry back into the start-up path, which is the only
// way this figure can grow without the certificate work itself growing.
func BenchmarkEnsureContended(b *testing.B) {
	root := b.TempDir()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dir := filepath.Join(root, strconv.Itoa(i))
		var wg sync.WaitGroup
		errs := make([]error, racers)
		start := make(chan struct{})
		for r := 0; r < racers; r++ {
			wg.Add(1)
			go func(r int) {
				defer wg.Done()
				<-start
				_, errs[r] = tlscert.Ensure(dir, tlscert.Options{})
			}(r)
		}
		close(start)
		wg.Wait()
		for r, err := range errs {
			if err != nil {
				b.Fatalf("racer %d: %v", r, err)
			}
		}
	}
}
