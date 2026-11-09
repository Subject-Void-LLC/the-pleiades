// Package testsupport: every file a test downloads, in one place. CI keys its
// download cache on a hash of this file, so a changed pin is a new cache
// entry and an unchanged one is fetched once.
//
// The key is made from this file alone, and every container shard restores
// the same entry. The first shard to miss saves it, holding only what that
// shard downloaded, so a pin that only another shard uses is missing from the
// entry and is downloaded by that shard on every run without being saved.
// Today one test uses the one pin. When a pin is used in a different shard
// from an earlier one, add the shard number to the key in ci.yml.
package testsupport

import "github.com/Subject-Void-LLC/the-pleiades/internal/testsupport/need"

// ToyboxChmod is the toybox project's own static x86_64 build, which the
// toybox chmod test copies into an Alpine sshd container (Alpine ships toybox
// only in its moving edge/testing repository). Pinned by version and by the
// SHA-256 read when the pin was written (2026-09-27; the project publishes no
// checksum file).
var ToyboxChmod = Pin{
	What:   need.Toybox,
	URL:    "https://landley.net/toybox/downloads/binaries/0.8.13/toybox-x86_64",
	SHA256: "8c98795a15db31ea55c8065fed379db3669766b7a714c46b009d8bfb87b25ffd",
}
