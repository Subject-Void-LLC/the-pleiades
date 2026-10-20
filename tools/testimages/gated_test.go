// Package main: an image no test can start without a token is left out
// only where the token is absent, and only while every test that starts it
// asks for the token first.
package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestStartable_LeavesOutLocalStackOnlyWithoutItsToken: with no token the
// LocalStack image is left out and named; with one it is kept; every other
// image is kept either way.
func TestStartable_LeavesOutLocalStackOnlyWithoutItsToken(t *testing.T) {
	images := []string{"localstack/localstack:2026.7.4", "nats:2.14.4-alpine", "postgres:15.19-alpine"}
	kept, omitted := startable(images, func(string) string { return "" })
	if !slices.Equal(kept, images[1:]) || !slices.Equal(omitted, images[:1]) {
		t.Errorf("without a token: kept %v, omitted %v; want LocalStack alone left out", kept, omitted)
	}
	kept, omitted = startable(images, func(name string) string {
		if name == "LOCALSTACK_AUTH_TOKEN" {
			return "a-token"
		}
		return ""
	})
	if !slices.Equal(kept, images) || len(omitted) != 0 {
		t.Errorf("with a token: kept %v, omitted %v; want every image", kept, omitted)
	}
}

// TestEveryLocalStackStartIsGated holds the reason the image may be left
// out: every Go file that starts a LocalStack container, which it does with
// the pinned testsupport.LocalStackImage (testsupport's pin rule refuses a
// literal), also asks testsupport.LocalStackToken, which skips its test
// without the token. A file that started one without asking would find no
// image on a CI runner and pull 2.1 GB inside its own test's time.
func TestEveryLocalStackStartIsGated(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	starts := 0
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (strings.HasPrefix(d.Name(), ".") && path != root || d.Name() == "node_modules") {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		src, err := os.ReadFile(path) // #nosec G304 -- a file of this module, walked from its root
		if err != nil {
			return err
		}
		text := string(src)
		rel, _ := filepath.Rel(root, path)
		if !strings.Contains(text, "testsupport.LocalStackImage") || strings.HasPrefix(filepath.ToSlash(rel), "internal/testsupport/") {
			return nil
		}
		starts++
		if !strings.Contains(text, "testsupport.LocalStackToken(") {
			t.Errorf("%s starts LocalStack without asking testsupport.LocalStackToken, so leaving its image out where there is no token would leave that test pulling it", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if starts == 0 {
		t.Fatal("found no file that starts LocalStack; the walk is not reading this module")
	}
}
