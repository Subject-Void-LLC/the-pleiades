// Command coverage-check runs the full test suite with -cover and fails
// if any package's coverage drops below the floor recorded for it in
// coverage-floor.json at the repo root. This is the coverage ratchet the
// Phase 0 CI harness item's coverage policy decision describes
// (.SPECIFICATION/IMPLEMENTATION.md): no package may regress, 90%
// (AGENTS.md's stated minimum) is the target for new and touched code,
// and a package with no recorded floor yet is reported, not failed, so
// adding a new package never blocks CI by itself.
//
// Usage: go run ./tools/coverage-check
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// tolerance absorbs floating point rounding in go test's own printed
// percentage; a package must drop by more than this to count as a real
// regression.
const tolerance = 0.05

var (
	pkgPattern     = regexp.MustCompile(`github\.com/SubjectVoidLLC/the-pleiades/\S*`)
	percentPattern = regexp.MustCompile(`coverage:\s+([\d.]+)%`)
)

type floorFile struct {
	Floors   map[string]float64 `json:"floors"`
	Excluded map[string]string  `json:"excluded"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "coverage-check:", err)
		os.Exit(1)
	}
}

func run() error {
	floors, err := loadFloorFile("coverage-floor.json")
	if err != nil {
		return fmt.Errorf("loading coverage-floor.json: %w", err)
	}

	measured, err := measureCoverage()
	if err != nil {
		return err
	}

	var regressions []string
	var newPackages []string
	seen := make(map[string]bool, len(measured))

	pkgs := make([]string, 0, len(measured))
	for pkg := range measured {
		pkgs = append(pkgs, pkg)
	}
	sort.Strings(pkgs)

	for _, pkg := range pkgs {
		pct := measured[pkg]
		seen[pkg] = true

		if _, excluded := floors.Excluded[pkg]; excluded {
			continue
		}
		floor, ok := floors.Floors[pkg]
		if !ok {
			newPackages = append(newPackages, fmt.Sprintf("%s: %.1f%% (no floor recorded yet)", pkg, pct))
			continue
		}
		if pct+tolerance < floor {
			regressions = append(regressions, fmt.Sprintf("%s: %.1f%% dropped below its floor of %.1f%%", pkg, pct, floor))
		}
	}

	if len(newPackages) > 0 {
		fmt.Println("coverage-check: packages with no recorded floor (informational, not a failure):")
		for _, n := range newPackages {
			fmt.Println("  " + n)
		}
	}

	if len(regressions) > 0 {
		fmt.Fprintln(os.Stderr, "coverage-check: regressions below the recorded floor:")
		for _, r := range regressions {
			fmt.Fprintln(os.Stderr, "  "+r)
		}
		return fmt.Errorf("%d package(s) regressed", len(regressions))
	}

	fmt.Printf("coverage-check: %d package(s) measured, none below their recorded floor\n", len(seen))
	return nil
}

func loadFloorFile(path string) (*floorFile, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- fixed repo-relative path, not user input
	if err != nil {
		return nil, err
	}
	var f floorFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, err
	}
	return &f, nil
}

// measureCoverage runs the full suite with -cover and parses each
// package's reported percentage. A package go test reports with no
// numeric percentage at all (a test-only package with nothing to
// instrument, "[no statements]") is simply absent from the result, not
// reported as 0%, since coverage-floor.json's own "excluded" entries for
// those packages document why deliberately rather than this tool
// silently treating "nothing to measure" the same as "measured and
// found lacking."
func measureCoverage() (map[string]float64, error) {
	cmd := exec.Command("go", "test", "./...", "-cover", "-count=1")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()

	results := make(map[string]float64)
	scanner := bufio.NewScanner(&stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.Contains(line, "coverage:") {
			continue
		}
		pkg := pkgPattern.FindString(line)
		if pkg == "" {
			continue
		}
		m := percentPattern.FindStringSubmatch(line)
		if m == nil {
			continue // "[no statements]": nothing numeric to record
		}
		pct, err := strconv.ParseFloat(m[1], 64)
		if err != nil {
			continue
		}
		results[pkg] = pct
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scanning go test output: %w", err)
	}

	if runErr != nil {
		return nil, fmt.Errorf("go test ./... failed (a test failure, not a coverage question, must be fixed first): %v\n%s", runErr, stderr.String())
	}
	return results, nil
}
