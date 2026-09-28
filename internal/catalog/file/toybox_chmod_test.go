// Package file_test: the file methods against toybox's chmod, over real SSH
// to an Alpine sshd.
package file_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
)

// The toybox half of FAILURE_PATTERNS 248's platform list. Toybox is the
// userland of Android and of some embedded Linux images, and Alpine ships
// it only in its moving edge/testing repository, so the test fetches the
// toybox project's own static build, pinned by version and by the SHA-256
// read when the pin was written (2026-09-27; the project publishes no
// checksum file), and copies it into the pinned Alpine sshd image.
//
// Probed before this was written, against 0.8.13: chmod 00755 means 0755,
// and, like BusyBox and unlike GNU, the four-digit form also clears a
// directory's setgid, so the five-digit form pkg/remotefile sends is safe
// here and was never needed here.
const (
	toyboxVersion = "0.8.13"
	toyboxURL     = "https://landley.net/toybox/downloads/binaries/" + toyboxVersion + "/toybox-x86_64"
	toyboxSHA256  = "8c98795a15db31ea55c8065fed379db3669766b7a714c46b009d8bfb87b25ffd"
	toyboxPath    = "/usr/local/bin/toybox"
)

// toyboxApplet installs the pinned toybox build in the container.
var toyboxApplet = chmodApplet{
	name: "toybox",
	path: toyboxPath,
	install: func(t *testing.T, container testcontainers.Container) {
		t.Helper()
		binary := fetchToybox(t)
		if err := container.CopyToContainer(context.Background(), binary, toyboxPath, 0o755); err != nil {
			t.Fatalf("copying toybox into the container: %v", err)
		}
	},
}

// fetchToybox downloads the pinned toybox build and refuses one whose
// checksum differs. A failed download fails the test rather than skipping
// it, since a skip would read as a pass.
func fetchToybox(t *testing.T) []byte {
	t.Helper()
	client := &http.Client{Timeout: 2 * time.Minute}
	resp, err := client.Get(toyboxURL)
	if err != nil {
		t.Fatalf("downloading toybox %s: %v", toyboxVersion, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("downloading toybox %s: HTTP %d", toyboxVersion, resp.StatusCode)
	}
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, io.LimitReader(resp.Body, 16<<20)); err != nil {
		t.Fatalf("reading toybox %s: %v", toyboxVersion, err)
	}
	sum := sha256.Sum256(buf.Bytes())
	if got := hex.EncodeToString(sum[:]); got != toyboxSHA256 {
		t.Fatalf("toybox %s has SHA-256 %s, want the pinned %s; the published build changed", toyboxVersion, got, toyboxSHA256)
	}
	return buf.Bytes()
}

// TestToyboxChmod_FiveDigitModesMeanWhatTheySay is
// TestBusyBoxChmod_FiveDigitModesMeanWhatTheySay against toybox's chmod.
func TestToyboxChmod_FiveDigitModesMeanWhatTheySay(t *testing.T) {
	if testing.Short() {
		t.Skip("starts an sshd container and downloads toybox")
	}
	fiveDigitChmodCases(t, toyboxApplet)
}
