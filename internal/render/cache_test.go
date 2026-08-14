package render_test

import (
	"fmt"
	"sync"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
)

// TestCompileSharesOneInstancePerSource proves the Flyweight claim rather
// than assuming it. A compiled template is immutable, so sharing is safe,
// and sharing is the point: a controller renders the same handful of
// injector templates on every launch for the life of the process.
func TestCompileSharesOneInstancePerSource(t *testing.T) {
	t.Parallel()

	eng := render.New()

	first, err := eng.Compile("{{ api_token }}")
	if err != nil {
		t.Fatalf("Compile() failed: %v", err)
	}
	second, err := eng.Compile("{{ api_token }}")
	if err != nil {
		t.Fatalf("Compile() failed: %v", err)
	}

	if first != second {
		t.Error("compiling identical source twice returned two instances, so the cache is not shared")
	}

	other, err := eng.Compile("{{ api_url }}")
	if err != nil {
		t.Fatalf("Compile() failed: %v", err)
	}
	if first == other {
		t.Error("compiling different source returned the same instance")
	}
}

// TestConcurrentCompilesConvergeOnOneWinner covers the race the miss path
// is written around: parsing happens outside the lock, so two goroutines
// can parse the same new source at once, and the loser must adopt the
// winner's instance rather than keeping its own.
//
// Run under -race this also proves the cache map itself is safe.
func TestConcurrentCompilesConvergeOnOneWinner(t *testing.T) {
	t.Parallel()

	const goroutines = 32
	eng := render.New()

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		results = make([]render.Template, 0, goroutines)
		start   = make(chan struct{})
	)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start

			tmpl, err := eng.Compile("{{ contended.source }}")
			if err != nil {
				t.Errorf("Compile() failed: %v", err)
				return
			}
			mu.Lock()
			results = append(results, tmpl)
			mu.Unlock()
		}()
	}

	close(start)
	wg.Wait()

	if len(results) != goroutines {
		t.Fatalf("collected %d results, want %d", len(results), goroutines)
	}
	for i, got := range results {
		if got != results[0] {
			t.Fatalf("goroutine %d kept its own instance instead of the shared winner", i)
		}
	}
}

// TestCacheDegradesToUncachedPastItsBound pins the documented overflow
// behavior. Past the bound a template is returned uncached rather than
// evicting something, so the degradation is a slowdown and never a
// failure. Asserting it from outside is what stops the bound quietly
// becoming an eviction policy later.
func TestCacheDegradesToUncachedPastItsBound(t *testing.T) {
	t.Parallel()

	// Matches maxCachedTemplates in limits.go. The constant is unexported
	// on purpose, so this test states the number it depends on and will
	// fail loudly if the two ever disagree.
	const bound = 4096

	eng := render.New()
	for i := 0; i < bound; i++ {
		if _, err := eng.Compile(fmt.Sprintf("{{ v%d }}", i)); err != nil {
			t.Fatalf("Compile() failed while filling the cache: %v", err)
		}
	}

	// The cache is now full, so this source is compiled fresh each time.
	const overflow = "{{ past_the_bound }}"
	first, err := eng.Compile(overflow)
	if err != nil {
		t.Fatalf("Compile() past the bound failed: %v", err)
	}
	second, err := eng.Compile(overflow)
	if err != nil {
		t.Fatalf("Compile() past the bound failed: %v", err)
	}

	if first == second {
		t.Error("a source compiled past the cache bound was cached, so the bound is not enforced")
	}

	// The important half: it still works.
	got, err := second.Render(map[string]any{"past_the_bound": "ok"})
	if err != nil {
		t.Fatalf("Render() of an uncached template failed: %v", err)
	}
	if got != "ok" {
		t.Errorf("Render() = %q, want %q", got, "ok")
	}

	// And an entry cached before the bound was reached is still shared,
	// proving nothing was evicted to make room.
	early, err := eng.Compile("{{ v0 }}")
	if err != nil {
		t.Fatalf("Compile() failed: %v", err)
	}
	earlyAgain, err := eng.Compile("{{ v0 }}")
	if err != nil {
		t.Fatalf("Compile() failed: %v", err)
	}
	if early != earlyAgain {
		t.Error("an entry cached before the bound was evicted, but this cache has no eviction policy")
	}
}

// TestEngineInstancesDoNotShareACache documents that the cache belongs to
// the Engine rather than to the package. Two composition roots in one
// process (the controller's HTTP path and its dispatch path, say) may hold
// separate engines without one being able to observe the other's entries.
func TestEngineInstancesDoNotShareACache(t *testing.T) {
	t.Parallel()

	a, err := render.New().Compile("{{ x }}")
	if err != nil {
		t.Fatalf("Compile() failed: %v", err)
	}
	b, err := render.New().Compile("{{ x }}")
	if err != nil {
		t.Fatalf("Compile() failed: %v", err)
	}

	if a == b {
		t.Error("two independent engines shared a cached template, so the cache is package-level")
	}
}
