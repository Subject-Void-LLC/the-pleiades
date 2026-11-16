// Package testsupport: the pin of Nokia SR Linux's image, kept here where
// tools/testimages reads every image the container tests pull. The container
// itself is started by internal/testsupport/srlinux, a package of its own so
// its startup code, which only other packages' container tests run, is not
// counted against this package's coverage.
package testsupport

// SRLinuxImage is Nokia SR Linux 26.7.2, published on GitHub Container
// Registry, pinned by its tag's multi-arch index digest.
const SRLinuxImage = "ghcr.io/nokia/srlinux@sha256:0096fe3ebcafabb7253492e2060425fe027a168e0e066766d1e85efbb0b48be8"
