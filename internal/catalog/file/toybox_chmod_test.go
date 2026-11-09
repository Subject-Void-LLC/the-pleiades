// Package file_test: the file methods against toybox's chmod, over real SSH
// to an Alpine sshd.
package file_test

import (
	"context"
	"testing"

	"github.com/testcontainers/testcontainers-go"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
)

// The toybox half of FAILURE_PATTERNS 248's platform list. Toybox is the
// userland of Android and of some embedded Linux images, and Alpine ships
// it only in its moving edge/testing repository, so the test uses the
// toybox project's own static build, pinned by version and by SHA-256
// (testsupport.ToyboxChmod), and copies it into the pinned Alpine sshd
// image. The build comes from the downloads cache (testsupport.Download), so
// a run does not depend on landley.net being up.
//
// Probed before this was written, against 0.8.13: chmod 00755 means 0755,
// and, like BusyBox and unlike GNU, the four-digit form also clears a
// directory's setgid, so the five-digit form pkg/remotefile sends is safe
// here and was never needed here.
const toyboxPath = "/usr/local/bin/toybox"

// toyboxApplet installs the pinned toybox build in the container.
var toyboxApplet = chmodApplet{
	name: "toybox",
	path: toyboxPath,
	install: func(t *testing.T, container testcontainers.Container) {
		t.Helper()
		// Skips ("needs toybox") when the build is neither cached nor
		// fetchable, and fails when its checksum is wrong.
		binary := testsupport.Download(t, testsupport.ToyboxChmod)
		if err := container.CopyToContainer(context.Background(), binary, toyboxPath, 0o755); err != nil {
			t.Fatalf("copying toybox into the container: %v", err)
		}
	},
}

// TestToyboxChmod_FiveDigitModesMeanWhatTheySay is
// TestBusyBoxChmod_FiveDigitModesMeanWhatTheySay against toybox's chmod.
func TestToyboxChmod_FiveDigitModesMeanWhatTheySay(t *testing.T) {
	if testing.Short() {
		t.Skip("starts an sshd container and may download toybox")
	}
	fiveDigitChmodCases(t, toyboxApplet)
}
