// Package project_test's source coverage: which addresses a deployment will
// fetch from, and how each one is classified.
//
// The classification cases matter more than they look. This validator exists to
// agree with go-git about what a string means, so the table below is really a
// record of what go-git does with each shape: a bare path is a local path, and
// anything shaped like "host:path" is ssh. Both are surprising until seen once,
// and both decide whether a refusal lands.
package project_test

import (
	"errors"
	"runtime"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/project"
)

// TestClassifySource records what go-git makes of each shape of address.
func TestClassifySource(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"https", "https://github.com/org/repo.git", project.ProtocolHTTPS},
		{"http", "http://mirror.internal/org/repo.git", project.ProtocolHTTP},
		{"ssh with a port", "ssh://git@github.com:2222/org/repo.git", project.ProtocolSSH},
		{"the git daemon", "git://git.kernel.org/pub/scm/git/git.git", project.ProtocolGit},
		{"an explicit file url", "file:///srv/repos/automation.git", project.ProtocolFile},

		// The two shapes worth knowing about, because neither looks like what
		// it is.
		{
			// The most common way of writing an ssh remote, and the reason
			// this validator cannot simply require a "scheme://" prefix.
			name: "scp-like is ssh",
			raw:  "git@github.com:org/repo.git",
			want: project.ProtocolSSH,
		},
		{
			// A bare path is a LOCAL path, absolutized against the
			// Controller's working directory. Nothing announces that.
			name: "a bare absolute path is local",
			raw:  "/srv/repos/automation.git",
			want: project.ProtocolFile,
		},
		{"a relative path is local", "../automation.git", project.ProtocolFile},

		{
			// The stated limit of this whole mechanism, pinned so it cannot be
			// mistaken later for something the allowlist prevents: anything
			// with a colon and no slash before it is an ssh HOST, so an
			// allowed protocol still reaches any host the Controller resolves.
			name: "a bare hostname with a path is ssh, which the allowlist does not narrow",
			raw:  "srv:secrets-repo",
			want: project.ProtocolSSH,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := project.ClassifySource(tc.raw)
			if err != nil {
				t.Fatalf("ClassifySource(%q) = %v", tc.raw, err)
			}
			if got != tc.want {
				t.Errorf("ClassifySource(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

// TestClassifySource_AnEmptyAddressIsNotALocalPath is the ordering this
// validator has to get right.
//
// go-git does not refuse an empty string: it absolutizes it, so it becomes a
// local path to whatever working directory the Controller has. Classified
// first, an empty URL would be reported as a refused local source, which tells
// somebody filling in a form the wrong thing entirely.
func TestClassifySource_AnEmptyAddressIsNotALocalPath(t *testing.T) {
	for _, raw := range []string{"", "   ", "\t\n"} {
		_, err := project.ClassifySource(raw)
		if !errors.Is(err, project.ErrSourceRefused) {
			t.Fatalf("ClassifySource(%q) = %v, want it refused", raw, err)
		}
		if !strings.Contains(err.Error(), "needs a repository URL") {
			t.Errorf("ClassifySource(%q) = %q, want it to say the URL is missing rather than that it is local", raw, err)
		}
	}
}

// TestClassifySource_ADOSPathIsLocalOnWindows exists as a skip on every other
// platform on purpose.
//
// "C:/repo" classifies as SSH on Linux, host "C", because go-git consults its
// DOS-drive rule only when it is running on Windows. A table case asserting
// "a DOS path is a local path" would therefore pass on this machine for the
// wrong reason, which is worse than not having it.
func TestClassifySource_ADOSPathIsLocalOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("go-git reads C:/repo as ssh host \"C\" off Windows; the rule under test is the Windows one")
	}
	got, err := project.ClassifySource(`C:/repo`)
	if err != nil {
		t.Fatalf("ClassifySource: %v", err)
	}
	if got != project.ProtocolFile {
		t.Errorf("ClassifySource(C:/repo) = %q, want %q on Windows", got, project.ProtocolFile)
	}
}

// TestAdmitsSource is the policy table: what the default refuses, and what each
// toggle adds.
func TestAdmitsSource(t *testing.T) {
	const (
		https = "https://github.com/org/repo.git"
		ssh   = "git@github.com:org/repo.git"
		plain = "http://mirror.internal/org/repo.git"
		daemn = "git://git.internal/repo.git"
		local = "/srv/repos/automation.git"
	)

	cases := []struct {
		name    string
		policy  project.SourcePolicy
		admits  []string
		refuses []string
	}{
		{
			// The zero value, which is what a forgotten threading produces.
			name:    "the default fetches only from https and ssh",
			policy:  project.SourcePolicy{},
			admits:  []string{https, ssh},
			refuses: []string{plain, daemn, local, "file:///srv/x"},
		},
		{
			name:    "the insecure toggle adds http and the git daemon, and nothing else",
			policy:  project.SourcePolicy{AllowInsecureTransport: true},
			admits:  []string{https, ssh, plain, daemn},
			refuses: []string{local},
		},
		{
			name:    "the local toggle adds paths, and nothing else",
			policy:  project.SourcePolicy{AllowLocalPath: true},
			admits:  []string{https, ssh, local, "file:///srv/x"},
			refuses: []string{plain, daemn},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, raw := range tc.admits {
				if err := tc.policy.AdmitsSource(raw); err != nil {
					t.Errorf("AdmitsSource(%q) = %v, want it admitted", raw, err)
				}
			}
			for _, raw := range tc.refuses {
				err := tc.policy.AdmitsSource(raw)
				if !errors.Is(err, project.ErrSourceRefused) {
					t.Errorf("AdmitsSource(%q) = %v, want ErrSourceRefused", raw, err)
				}
			}
		})
	}
}

// TestAdmitsSource_ARefusalSaysWhatWouldAllowIt proves the message is
// actionable rather than only correct, and that it does not echo the address,
// which can carry a credential and which reaches a form, a log and a stored
// sync failure.
func TestAdmitsSource_ARefusalSaysWhatWouldAllowIt(t *testing.T) {
	cases := map[string]string{
		"http://mirror.internal/repo.git": "PLEIADES_PROJECT_ALLOW_INSECURE_SOURCE",
		"git://git.internal/repo.git":     "PLEIADES_PROJECT_ALLOW_INSECURE_SOURCE",
		"/srv/repos/automation.git":       "PLEIADES_PROJECT_ALLOW_LOCAL_SOURCE",
	}

	for raw, toggle := range cases {
		err := project.SourcePolicy{}.AdmitsSource(raw)
		if err == nil {
			t.Fatalf("AdmitsSource(%q) was admitted by the default policy", raw)
		}
		if !strings.Contains(err.Error(), toggle) {
			t.Errorf("the refusal of %q does not name %s: %v", raw, toggle, err)
		}
		if !strings.Contains(err.Error(), "https") {
			t.Errorf("the refusal of %q does not say what IS accepted: %v", raw, err)
		}
		if strings.Contains(err.Error(), raw) {
			t.Errorf("the refusal echoes the address, which may carry a credential: %v", err)
		}
	}
}

// TestHasEmbeddedSecret proves the rule is about a password and not about a
// user, which is the distinction that decides whether the most common way of
// writing an ssh remote is refused.
func TestHasEmbeddedSecret(t *testing.T) {
	cases := []struct {
		raw  string
		want bool
	}{
		{"https://someone:ghp-TOKEN@git.invalid/private/repo.git", true},
		{"https://x-access-token:secret@github.com/org/repo.git", true},

		// A user with no password: ordinary, and required for an ssh remote
		// written the usual way.
		{"git@github.com:org/repo.git", false},
		{"ssh://git@github.com/org/repo.git", false},
		{"https://github.com/org/repo.git", false},
		{"/srv/repos/automation.git", false},
		{"", false},
	}

	for _, tc := range cases {
		if got := project.HasEmbeddedSecret(tc.raw); got != tc.want {
			t.Errorf("HasEmbeddedSecret(%q) = %t, want %t", tc.raw, got, tc.want)
		}
	}
}
