// An independent check of package iso9660's output with util-linux
// blkid, the probe cloud-init uses to find a NoCloud seed by its label.
package iso9660_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/iso9660"
)

// findBlkid returns the path of blkid, or skips the test when it is not
// installed. It looks on PATH first and then in the sbin directories,
// which an unprivileged user's PATH often leaves out.
func findBlkid(t *testing.T) string {
	t.Helper()
	if p, err := exec.LookPath("blkid"); err == nil {
		return p
	}
	for _, p := range []string{"/usr/sbin/blkid", "/sbin/blkid"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	t.Skip("blkid is not installed")
	return ""
}

// probe runs `blkid -p -o export` on an image file and returns its
// KEY=VALUE pairs. -p probes the file's content directly instead of
// consulting blkid's cache, so the answer comes from the bytes written.
func probe(t *testing.T, blkid, path string) map[string]string {
	t.Helper()
	out, err := exec.Command(blkid, "-p", "-o", "export", path).CombinedOutput()
	if err != nil {
		t.Fatalf("blkid -p -o export %s: %v\n%s", path, err, out)
	}
	t.Logf("blkid -p -o export:\n%s", out)
	tags := make(map[string]string)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			t.Fatalf("blkid printed a line that is not KEY=VALUE: %q", line)
		}
		// The export format escapes spaces and other shell-special
		// characters with a backslash.
		tags[key] = strings.ReplaceAll(value, `\`, "")
	}
	return tags
}

// TestBlkidRecognizesTheSeed writes the seed image to disk and checks
// blkid reports an iso9660 filesystem labeled cidata, with a Joliet
// descriptor, and with the UUID libblkid derives from the modification
// date, which proves blkid parsed the volume dates where they belong.
func TestBlkidRecognizesTheSeed(t *testing.T) {
	blkid := findBlkid(t)
	tests := []struct {
		name     string
		volumeID string
	}{
		{"lower-case label", seedLabel},
		{"upper-case label", strings.ToUpper(seedLabel)},
		{"sixteen characters, mixed case", "Seed_Volume_0016"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "seed.iso")
			if err := os.WriteFile(path, writeImage(t, tc.volumeID, seedFiles(t)), 0o600); err != nil {
				t.Fatalf("writing the image: %v", err)
			}
			tags := probe(t, blkid, path)
			if tags["TYPE"] != "iso9660" {
				t.Errorf("TYPE=%q, want iso9660", tags["TYPE"])
			}
			if !strings.EqualFold(tags["LABEL"], tc.volumeID) {
				t.Errorf("LABEL=%q, want %q in either case", tags["LABEL"], tc.volumeID)
			}
			if tags["VERSION"] != "Joliet Extension" {
				t.Errorf("VERSION=%q, want Joliet Extension: blkid did not find the Joliet descriptor", tags["VERSION"])
			}
			if tags["UUID"] != seedUUID {
				t.Errorf("UUID=%q, want %s from the volume modification date", tags["UUID"], seedUUID)
			}
			if tags["BLOCK_SIZE"] != "2048" {
				t.Errorf("BLOCK_SIZE=%q, want 2048", tags["BLOCK_SIZE"])
			}
		})
	}
}

// TestBlkidNoticesABrokenJolietDescriptor is the negative control for
// TestBlkidRecognizesTheSeed. With the Joliet escape sequence blanked,
// sector 17 is no longer a Joliet descriptor, and blkid must stop
// reporting Joliet and fall back to the primary descriptor's upper-cased
// label. That proves the VERSION check above can fail, and that the
// mixed-case label blkid reported there came from the Joliet descriptor.
func TestBlkidNoticesABrokenJolietDescriptor(t *testing.T) {
	blkid := findBlkid(t)
	img := writeImage(t, "Seed_Volume_0016", seedFiles(t))
	copy(img[17*iso9660.SectorSize+88:], "\x00\x00\x00")
	path := filepath.Join(t.TempDir(), "broken.iso")
	if err := os.WriteFile(path, img, 0o600); err != nil {
		t.Fatalf("writing the image: %v", err)
	}
	tags := probe(t, blkid, path)
	if tags["TYPE"] != "iso9660" {
		t.Errorf("TYPE=%q, want iso9660: the primary descriptor is intact", tags["TYPE"])
	}
	if tags["VERSION"] == "Joliet Extension" {
		t.Error("blkid still reports Joliet after the escape sequence was blanked")
	}
	if tags["LABEL"] != "SEED_VOLUME_0016" {
		t.Errorf("LABEL=%q, want the primary descriptor's SEED_VOLUME_0016", tags["LABEL"])
	}
}
