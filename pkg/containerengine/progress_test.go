// Tests for reading an engine's progress stream.
package containerengine

import (
	"slices"
	"strings"
	"testing"
)

// TestDrainProgress keeps the lines a stream says, and returns the error a
// message carries as the stream's failure.
func TestDrainProgress(t *testing.T) {
	lines, err := DrainProgress(strings.NewReader(`{"stream":"Loaded image: app:1\n"}{"status":"sha256:abc"}{"stream":"  "}`))
	if err != nil || !slices.Equal(lines, []string{"Loaded image: app:1", "sha256:abc"}) {
		t.Errorf("DrainProgress = %q, %v", lines, err)
	}
	if _, err := DrainProgress(strings.NewReader(`{"stream":"x"}{"errorDetail":{"message":"no space left"}}`)); err == nil || !strings.Contains(err.Error(), "no space left") {
		t.Errorf("an error message = %v", err)
	}
	if _, err := DrainProgress(strings.NewReader(`{"stream":`)); err == nil {
		t.Error("a cut stream was read as whole")
	}
}

// TestLoadedImages picks the loaded references out of both engines' lines.
func TestLoadedImages(t *testing.T) {
	got := LoadedImages([]string{"Loaded image: app:1", "Loaded image ID: sha256:abc", "Loaded image(s): a:1,b:2", "other"})
	if !slices.Equal(got, []string{"app:1", "sha256:abc", "a:1", "b:2"}) {
		t.Errorf("LoadedImages = %q", got)
	}
}
