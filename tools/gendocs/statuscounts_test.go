// Tests that the hand-written status counts in README.md and
// docs/01-start-here.md agree with the registry.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/catalogdata"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// catalogCounts is what the registry says about the built-in catalog.
type catalogCounts struct {
	registered, implemented, declared, checkable int
	// byNamespace counts implemented methods by first dotted segment.
	byNamespace map[string]int
}

// countCatalog reads every built-in method's live descriptor.
func countCatalog(t *testing.T) catalogCounts {
	t.Helper()
	c := catalogCounts{byNamespace: map[string]int{}}
	for _, cfg := range catalogdata.Collections {
		desc, ok := collection.Lookup(cfg.Name)
		if !ok {
			t.Fatalf("%s is in catalogdata but not registered", cfg.Name)
		}
		c.registered++
		if desc.Manifest.Status != collection.StatusImplemented {
			c.declared++
			continue
		}
		c.implemented++
		if desc.Manifest.SupportsCheck {
			c.checkable++
		}
		c.byNamespace[strings.SplitN(cfg.Name, ".", 2)[0]]++
	}
	return c
}

// readRepoDoc reads a document at the repository root.
func readRepoDoc(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", name)) // #nosec G304 -- a fixed document in this repository
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(strings.Fields(string(data)), " ")
}

// atoi reads a number a pattern matched.
func atoi(t *testing.T, s string) int {
	t.Helper()
	n, err := strconv.Atoi(s)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// TestStatusCounts_MatchTheRegistry: the README and the start-here page each
// state the catalog's size in prose, and both had drifted 22 methods behind
// the registry by 2026-10-14 with nothing to notice. Every count they state
// is now read and compared, so the next method added fails here until the
// prose says so.
func TestStatusCounts_MatchTheRegistry(t *testing.T) {
	c := countCatalog(t)
	namespaces := len(c.byNamespace)

	readme := readRepoDoc(t, "README.md")
	for _, check := range []struct {
		pattern string
		want    []int
	}{
		{`(\d+) of the (\d+) can predict what they would change`, []int{c.checkable, c.implemented}},
		{`The module catalog has (\d+) registered methods; (\d+) are implemented`, []int{c.registered, c.implemented}},
		{`across (\d+) namespaces`, []int{namespaces}},
	} {
		m := regexp.MustCompile(check.pattern).FindStringSubmatch(readme)
		if m == nil {
			t.Errorf("README.md no longer says %q, so its count is unchecked", check.pattern)
			continue
		}
		for i, want := range check.want {
			if got := atoi(t, m[i+1]); got != want {
				t.Errorf("README.md: %q says %d where the registry has %d", m[0], got, want)
			}
		}
	}

	start := readRepoDoc(t, "docs/01-start-here.md")
	m := regexp.MustCompile(`The module catalog has (\d+) registered methods across (\d+) namespaces; (\d+) are implemented and (\d+) are declared`).FindStringSubmatch(start)
	if m == nil {
		t.Fatal("docs/01-start-here.md no longer states the catalog's size, so its count is unchecked")
	}
	for i, want := range []int{c.registered, namespaces, c.implemented, c.declared} {
		if got := atoi(t, m[i+1]); got != want {
			t.Errorf("docs/01-start-here.md: %q says %d where the registry has %d", m[0], got, want)
		}
	}
	list := regexp.MustCompile("By namespace, implemented: (.*?)\\. These counts").FindStringSubmatch(start)
	if list == nil {
		t.Fatal("docs/01-start-here.md no longer lists the catalog by namespace, so the list is unchecked")
	}
	stated := map[string]int{}
	for _, pair := range regexp.MustCompile("`([a-z]+)` (\\d+)").FindAllStringSubmatch(list[1], -1) {
		stated[pair[1]] = atoi(t, pair[2])
	}
	if fmt.Sprint(sortedCounts(stated)) != fmt.Sprint(sortedCounts(c.byNamespace)) {
		t.Errorf("docs/01-start-here.md lists %v by namespace where the registry has %v", sortedCounts(stated), sortedCounts(c.byNamespace))
	}
}

// sortedCounts renders counts by namespace in a fixed order.
func sortedCounts(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for ns, n := range m {
		out = append(out, fmt.Sprintf("%s=%d", ns, n))
	}
	sort.Strings(out)
	return out
}
