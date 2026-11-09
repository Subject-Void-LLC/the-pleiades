// Package testsupport: every file a test downloads, in one place. CI keys its
// download cache on a hash of this file, so a changed pin is a new cache
// entry and an unchanged one is fetched once.
package testsupport

// ToyboxChmod is the toybox project's own static x86_64 build, which the
// toybox chmod test copies into an Alpine sshd container (Alpine ships toybox
// only in its moving edge/testing repository). Pinned by version and by the
// SHA-256 read when the pin was written (2026-09-27; the project publishes no
// checksum file).
var ToyboxChmod = Pin{
	What:   "toybox",
	URL:    "https://landley.net/toybox/downloads/binaries/0.8.13/toybox-x86_64",
	SHA256: "8c98795a15db31ea55c8065fed379db3669766b7a714c46b009d8bfb87b25ffd",
}
