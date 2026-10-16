// Package archtest: that no test the external Collection SDK reaches pulls
// testcontainers-go into an external Collection author's module graph.
package archtest

import (
	"os/exec"
	"slices"
	"sort"
	"strings"
	"testing"
)

// testcontainersModule is the module a test of an SDK-reached package may
// not reach.
const testcontainersModule = "github.com/testcontainers/testcontainers-go"

// TestExternalSDKTestsStayLight refuses a test, of any package of this
// module that pkg/external reaches, that imports something reaching
// testcontainers-go.
//
// An external Collection author's `go mod tidy`, a step of the scaffolded
// README, loads the tests of every package the author's program reaches,
// in other modules too. Such an import therefore puts testcontainers-go's
// whole module graph into every author's go.sum, and an offline tidy fails
// wherever the module cache lacks one of its platform-only modules: the
// macOS CI runner's did, while the Linux one's happened to hold them all.
// A test there needs internal/testsupport/need and .../refusing, which
// import only the standard library, rather than internal/testsupport.
func TestExternalSDKTestsStayLight(t *testing.T) {
	var sdk []listedPackage
	for _, p := range goList(t, true, modulePath+"/pkg/external") {
		if strings.HasPrefix(p.ImportPath, modulePath+"/") {
			sdk = append(sdk, p)
		}
	}
	if len(sdk) < 5 {
		t.Fatalf("pkg/external reaches only %d packages of this module, so the listing is misaimed", len(sdk))
	}
	testImports := func(p listedPackage) []string {
		return append(append([]string{}, p.TestImports...), p.XTestImports...)
	}
	imports := map[string]bool{}
	for _, p := range sdk {
		for _, imp := range testImports(p) {
			imports[imp] = true
		}
	}
	heavy := reachingTestcontainers(t, imports)
	if len(heavy) == len(imports) {
		t.Fatalf("every one of %d test imports reaches %s, so the dependency listing is misaimed", len(imports), testcontainersModule)
	}
	for _, p := range sdk {
		for _, imp := range testImports(p) {
			if heavy[imp] {
				t.Errorf("%s's tests import %s, which reaches %s; an external Collection author's go mod tidy would need its whole module graph", p.ImportPath, imp, testcontainersModule)
			}
		}
	}
}

// reachingTestcontainers reports which of imports is testcontainers-go or
// depends on it, from one `go list` of them all.
func reachingTestcontainers(t *testing.T, imports map[string]bool) map[string]bool {
	t.Helper()
	paths := make([]string, 0, len(imports))
	for p := range imports {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	args := append([]string{"list", "-e", "-f", `{{.ImportPath}} {{join .Deps " "}}`}, paths...)
	out, err := exec.Command("go", args...).Output()
	if err != nil {
		t.Fatalf("go list of %d test imports: %v", len(paths), err)
	}
	within := func(path string) bool {
		return path == testcontainersModule || strings.HasPrefix(path, testcontainersModule+"/")
	}
	heavy := map[string]bool{}
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		if fields := strings.Fields(line); len(fields) > 0 && slices.ContainsFunc(fields, within) {
			heavy[fields[0]] = true
		}
	}
	return heavy
}
