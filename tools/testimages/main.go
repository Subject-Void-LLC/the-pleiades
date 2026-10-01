// Command testimages lists the container images a set of packages' tests
// start, so CI can pull them once, cache them between runs and load them
// from the cache afterwards (Phase 118).
//
// Pulling on demand from inside the tests is fine on a developer's
// machine and fragile on hosted runners: Docker Hub limits anonymous
// pulls per address, runners share addresses, and a retry cannot outwait
// a limit measured in hours. A cached image is not pulled at all.
//
// It reads the source rather than a hand-kept list, the way
// internal/testsupport's pin rule reads the pins: a string constant or
// variable whose name ends in "Image", and a composite literal's Image
// field, in any Go file of the named packages. internal/testsupport is
// always included, because its helpers (StartNATS, StartSSHD, ...) start
// its pinned images for packages that never name one. Images this
// repository builds itself (pleiades/...) are left out: there is nothing
// to pull.
//
// Usage: go run ./tools/testimages [-pull] package ...
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// testsupportPackage is always scanned, for the reason the package doc
// gives.
const testsupportPackage = "github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"

// pullBackoff is how long to wait before each retry of a failed pull. A
// transient registry error clears in seconds; a rate limit does not clear
// in any wait worth making here, so the retries are few and short.
var pullBackoff = []time.Duration{15 * time.Second, 45 * time.Second}

func main() {
	pull := flag.Bool("pull", false, "pull each image, retrying a failed pull, instead of only listing them")
	flag.Parse()
	if flag.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "testimages: name the packages whose images to list")
		os.Exit(2)
	}
	images, err := imagesOf(append([]string{testsupportPackage}, flag.Args()...))
	if err != nil {
		fmt.Fprintln(os.Stderr, "testimages:", err)
		os.Exit(1)
	}
	for _, image := range images {
		fmt.Println(image)
	}
	if *pull {
		if err := pullAll(images); err != nil {
			fmt.Fprintln(os.Stderr, "testimages:", err)
			os.Exit(1)
		}
	}
}

// imagesOf lists the images the packages' source names, sorted and
// without duplicates.
func imagesOf(packages []string) ([]string, error) {
	// -e keeps a listed package whose files a build constraint excludes
	// (internal/ent/migrate/gen) from failing the whole list: it has a
	// directory and nothing to scan. Its images, if any, are still read.
	// #nosec G204 -- import paths the Makefile or the workflow lists.
	cmd := exec.Command("go", append([]string{"list", "-e", "-tags", "integration", "-f", "{{.Dir}}"}, packages...)...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	seen := map[string]bool{}
	for _, dir := range strings.Fields(string(out)) {
		refs, err := scanDir(dir)
		if err != nil {
			return nil, err
		}
		for _, ref := range refs {
			seen[ref] = true
		}
	}
	return sortedKeys(seen), nil
}

// pullAll pulls each image, retrying a failure after each pullBackoff
// wait, and names every image that never arrived.
func pullAll(images []string) error {
	var failed []string
	for _, image := range images {
		var err error
		for attempt := 0; ; attempt++ {
			// #nosec G204 -- an image reference read from this repository's source.
			cmd := exec.Command("docker", "pull", "--quiet", image)
			cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
			if err = cmd.Run(); err == nil || attempt == len(pullBackoff) {
				break
			}
			fmt.Fprintf(os.Stderr, "testimages: pulling %s failed (%v); retrying in %s\n", image, err, pullBackoff[attempt])
			time.Sleep(pullBackoff[attempt])
		}
		if err != nil {
			failed = append(failed, image)
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("could not pull %s", strings.Join(failed, ", "))
	}
	return nil
}
