// Tests for finding image references in source, against a hand-written
// file of every shape and against this repository's own packages.
package main

import (
	"go/parser"
	"go/token"
	"slices"
	"testing"
)

// TestScanFile_FindsEveryShapeAndNothingElse proves the scanner keeps a
// pinned constant, a digest, a composite literal's Image field and a
// variable, and drops a locally built image, prose, a parameter name, a
// reference with no tag, and a value shaped like name:tag whose name has
// no letter (a uid:gid pair, which tests/e2e names packagingImageUID, and
// an address), which CI's pull step tried to fetch as an image.
func TestScanFile_FindsEveryShapeAndNothingElse(t *testing.T) {
	src := `package x

const NATSImage = "nats:2.14.4-alpine"
const NATSImageBeforeLimitMarkerTTL = "nats:2.10.29-alpine"
const gateNotconfImage = "ghcr.io/notconf/notconf@sha256:9ef5677e35d535ca81852d40135236e603526f4380547bc13ffc69ede1b6d03d"
const packagingControllerImage = "pleiades/controller:dev"
const paramImage = "image"
const doc = "an Image sentence: nats:2.14.4"
var pythonImage = "python:3.12-alpine3.20"
var untaggedImage = "alpine"
const packagingImageUID = "65532:65532"
const registryImageAddr = "127.0.0.1:5000"

type req struct{ Image string }

var r = req{Image: "gitea/gitea:1.22.6"}
var s = req{Image: "Windows Server 2025"}
`
	file, err := parser.ParseFile(token.NewFileSet(), "x.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	got := scanFile(file)
	slices.Sort(got)
	want := []string{
		"ghcr.io/notconf/notconf@sha256:9ef5677e35d535ca81852d40135236e603526f4380547bc13ffc69ede1b6d03d",
		"gitea/gitea:1.22.6",
		"nats:2.10.29-alpine",
		"nats:2.14.4-alpine",
		"python:3.12-alpine3.20",
	}
	if !slices.Equal(got, want) {
		t.Errorf("scanFile = %v, want %v", got, want)
	}
}

// TestImagesOf_ReadsThisRepository runs the real scan over a real
// container package and proves the testsupport pins are always there.
func TestImagesOf_ReadsThisRepository(t *testing.T) {
	images, err := imagesOf([]string{testsupportPackage, "github.com/Subject-Void-LLC/the-pleiades/internal/transport/ssh"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"nats:2.14.4-alpine", "lscr.io/linuxserver/openssh-server:version-10.3_p1-r0"} {
		if !slices.Contains(images, want) {
			t.Errorf("images = %v, want %s among them", images, want)
		}
	}
	for _, image := range images {
		if !imageRef.MatchString(image) {
			t.Errorf("%q is not a pullable reference", image)
		}
	}
}
