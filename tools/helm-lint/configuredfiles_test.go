// Package main tests the rule that every file a mounted configuration
// names is a file that is really mounted.
//
// The matcher behind that rule reports a problem only when it FINDS a path,
// so a matcher that recognized nothing would leave the rule permanently and
// silently clean. These are its negative controls.
package main

import "testing"

// TestConfiguredFilePathsFindsPaths is the negative control for the
// matcher behind checkConfiguredFilesAreMounted.
//
// It matters more than a matcher test usually does, because the rule it
// feeds reports a problem only when it finds a path. A matcher that
// recognized nothing would leave that rule permanently, silently clean,
// which is indistinguishable from a chart with nothing wrong.
func TestConfiguredFilePathsFindsPaths(t *testing.T) {
	const conf = `
tls {
  cert_file: "/etc/nats/tls/tls.crt"
  key_file: "/etc/nats/tls/tls.key"
}
websocket {
  port: 8080
  ca_file:   "/etc/nats/ca/ca.pem"
  no_tls: true
  store_dir: "/data"
  relative_file: "nats/relative.pem"
  bare_file: unquoted.pem
}
# cert_file: "/etc/nats/commented/out.pem"
`
	got := configuredFilePaths(conf)
	want := []string{
		"/etc/nats/tls/tls.crt",
		"/etc/nats/tls/tls.key",
		"/etc/nats/ca/ca.pem",
	}
	if len(got) != len(want) {
		t.Fatalf("configuredFilePaths() = %v, want %v", got, want)
	}
	for i, path := range got {
		if path != want[i] {
			t.Errorf("path %d = %q, want %q", i, path, want[i])
		}
	}
}

// TestConfiguredFilePathsIgnoresACommentedLine pins the one near-miss the
// table above states but does not isolate.
//
// A commented cert_file is not a path the broker reads, and a rule that
// reported one would send somebody looking for a mount that should not
// exist. The matcher achieves this by anchoring on the start of a line
// with only whitespace before the key, which is worth a test of its own
// because it is a property of one character in the pattern.
func TestConfiguredFilePathsIgnoresACommentedLine(t *testing.T) {
	if got := configuredFilePaths(`# cert_file: "/etc/nats/tls/tls.crt"`); len(got) != 0 {
		t.Errorf("configuredFilePaths() = %v on a commented line, want none", got)
	}
}

// TestMountCoversMatchesOnPathSegments proves the prefix test is a path
// test rather than a string test.
//
// The distinction is the whole correctness of the rule: a mount at
// /etc/nats/tls must cover /etc/nats/tls/tls.crt and must NOT cover
// /etc/nats/tlsconfig/tls.crt, and a naive strings.HasPrefix gets the
// second one wrong in the direction that hides a real defect.
func TestMountCoversMatchesOnPathSegments(t *testing.T) {
	mounts := []volumeMount{
		{Name: "tls", MountPath: "/etc/nats/tls"},
		{Name: "config", MountPath: "/etc/nats/nats.conf"},
	}
	for _, tc := range []struct {
		path string
		want bool
	}{
		{path: "/etc/nats/tls/tls.crt", want: true},
		{path: "/etc/nats/tls/tls.key", want: true},
		{path: "/etc/nats/nats.conf", want: true}, // an exact match, the subPath shape
		{path: "/etc/nats/tlsconfig/tls.crt", want: false},
		{path: "/etc/nats/tls-backup/tls.crt", want: false},
		{path: "/data/jetstream", want: false},
	} {
		if got := mountCovers(mounts, tc.path); got != tc.want {
			t.Errorf("mountCovers(%q) = %t, want %t", tc.path, got, tc.want)
		}
	}
}
