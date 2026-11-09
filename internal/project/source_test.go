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
	"context"
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

// TestHasEmbeddedSecret proves the rule refuses any password, and a user only
// over HTTP, where a forge takes a token in the user position: the
// distinction that decides whether the most common way of writing an ssh
// remote is refused.
func TestHasEmbeddedSecret(t *testing.T) {
	cases := []struct {
		raw  string
		want bool
	}{
		{"https://someone:ghp-TOKEN@git.invalid/private/repo.git", true},
		{"https://x-access-token:secret@github.com/org/repo.git", true},

		// A token in the user position over HTTP: what git sends as Basic auth
		// and a forge accepts, refused since 2026-10-14.
		{"https://ghp_0123456789abcdefTOKEN@github.com/org/repo.git", true},
		{"http://glpat-TOKEN@gitlab.internal/group/repo.git", true},
		{"ssh://git:password@github.com/org/repo.git", true},
		// go-git reads an scp-like userinfo whole, password included.
		{"git:password@github.com:org/repo.git", true},

		// Shapes a parser misreads or refuses, each of which passed and was
		// then shown raw: neither parser reads a bad port or a space in the
		// host; go-git calls git+https its own protocol; go-git reads a
		// scheme-less authority as a local path; and with a space in the host
		// go-git reads the scp-like form as a local path while net/url reads
		// "git:" as an opaque scheme, so neither sees the userinfo.
		{"https://svc:hunter2@host:badport/x", true},
		{"https://svc:hunter2@exa mple.invalid/x", true},
		{"https://svc:hunter2@[::1:bad/x", true},
		{"git+https://ghp_TOKEN@github.com/org/repo.git", true},
		{"//bot:hunter2@host/org/repo", true},
		{"//ghp_TOKEN@host/org/repo", true},
		{"git:hunter2@ho st:team/repo", true},
		// An empty password still says where one goes.
		{"https://svc:@github.com/org/repo.git", true},
		// A user alone is ordinary only for ssh, whose user picks the
		// account; under any other scheme it may be a token.
		{"git://ghp_TOKEN@git.internal/org/repo.git", true},
		{"HTTPS://ghp_TOKEN@github.com/org/repo.git", true},

		// Shapes the second review found past the shape list, now refused
		// because the rule names what is clean rather than what is not: a
		// password holding a "/" (net/url reads "user:1234" as a host and
		// port and puts the "@" in the path), compound schemes, lenient
		// slash forms, a password-shaped ssh user, and a second "@".
		{"https://user:pa/ss@host/r", true},
		{"https://user:1234/ss@host/r", true},
		{"https://user:pa?ss@host/r", true},
		{"https://user:pa#ss@host/r", true},
		{"git+ssh+https://ghp_TOKEN@host/r", true},
		{"https+ssh://ghp_TOKEN@host/r", true},
		{"https:/user:pw@host/r", true},
		{"https:///user:pw@host/r", true},
		{`https:\/ghp_TOKEN@host/r`, true},
		{"ssh:/git@host/r", true},
		{"ssh://git:@host/r", true},
		{"ssh://git%3Apw@host/r", true},
		{"ssh://git@host/r@x", true},
		{"git@host:org/r@v1.git", true},
		{"a@b@host:org/r", true},
		// A token in the query, which a forge takes as readily as one in
		// the userinfo.
		{"https://github.com/org/repo.git?access_token=ghp_TOKEN", true},
		{"https://gitlab.example/group/repo.git?private_token=glpat-TOKEN", true},
		{"https://github.com/org/repo.git?ref=main&token=ghp_TOKEN", true},
		{"git@github.com:org/repo.git?token=ghp_TOKEN", true},
		// An "@" in a path is refused too: the rule accepts the rare
		// over-refusal rather than a shape test.
		{"https://github.com/org/repo@v1.git", true},
		{"https://github.com/org/repo.git?ref=a@b", true},

		// An encoded or lookalike "@" is read as one: a URL holding any of
		// them is not clean, since some reader may decode it into a
		// separator, and a lookalike reached sync_error whole through a DNS
		// error.
		{"https://ghp_TOK%40github.com/r", true},
		{"https://user:pass%40github.com/r", true},
		{"https://user:pass%40github.com/r?", true},
		{"https://ghp_TOK" + fullwidthAt + "github.com/org/repo.git", true},
		{"https://ghp_TOK" + smallAt + "github.com/org/repo.git", true},
		{"ssh://git@github.com/org%40x/repo.git", true},

		// Any query or fragment, whatever its names: no list of secret
		// names covers an Azure SAS sig, an X-Amz-Signature or an apikey.
		{"https://acct.blob.example/c/repo.tar?sv=2024&sig=SIGNATURE", true},
		{"https://bucket.example/repo.tar?X-Amz-Signature=abc", true},
		{"https://host/r.git?apikey=abc", true},
		{"https://host/r.git?pwd=abc", true},
		{"https://host/r.git#token=abc", true},
		{"https://github.com/org/repo.git?ref=main", true},
		{"https://github.com/org/repo.git#readme", true},
		{"git@github.com:org/repo.git#main", true},
		{"/srv/repos/automation.git?x", true},

		// A user with no password: ordinary, and required for an ssh remote
		// written the usual way.
		{"git@github.com:org/repo.git", false},
		{"ssh://git@github.com/org/repo.git", false},
		{"git+ssh://git@github.com/org/repo.git", false},
		{"ssh+git://git@github.com/org/repo.git", false},
		{"SSH://git@github.com/org/repo.git", false},
		{"ssh://git@github.com:2222/org/repo.git", false},
		{"ssh://git@[::1]:2222/org/repo.git", false},
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

// TestRedactedSourceURL withholds a credential from a URL that carries one,
// keeps every other URL as written, and produces a value the write rule
// still refuses, so saving it back cannot quietly drop the credential.
func TestRedactedSourceURL(t *testing.T) {
	for raw, want := range map[string]string{
		"https://ghp_TOKEN@github.com/org/repo.git":   "https://redacted@github.com/org/repo.git",
		"https://someone:secret@git.invalid/repo.git": "https://redacted@git.invalid/repo.git",
		"https://github.com/org/repo.git":             "https://github.com/org/repo.git",
		"git@github.com:org/repo.git":                 "git@github.com:org/repo.git",
		"ssh://git@github.com/org/repo.git":           "ssh://git@github.com/org/repo.git",
		"git:secret@github.com:org/repo.git":          "(withheld: the URL carries a credential)",

		// Redacted precisely where net/url reads the userinfo, and withheld
		// whole where neither parser can say where it ends.
		"git+https://TOKEN@github.com/org/repo.git": "git+https://redacted@github.com/org/repo.git",
		"//bot:secret@host/org/repo":                "//redacted@host/org/repo",
		"https://svc:secret@host:badport/x":         "(withheld: the URL carries a credential)",
		"https://svc:secret@exa mple.invalid/x":     "(withheld: the URL carries a credential)",
		"https://svc:secret@[::1:bad/x":             "(withheld: the URL carries a credential)",
		"git:secret@ho st:team/repo":                "(withheld: the URL carries a credential)",

		// The second review's shapes: none can be cut exactly, so each is
		// withheld whole.
		"https://user:pa/secret@host/r":                "(withheld: the URL carries a credential)",
		"https://user:1234/secret@host/r":              "(withheld: the URL carries a credential)",
		"git+ssh+https://TOKEN@host/r":                 "git+ssh+https://redacted@host/r",
		"https:/user:secret@host/r":                    "(withheld: the URL carries a credential)",
		"https:///user:secret@host/r":                  "(withheld: the URL carries a credential)",
		`https:\/TOKEN@host/r`:                         "(withheld: the URL carries a credential)",
		"https://host/r.git?access_token=TOKEN":        "(withheld: the URL carries a credential)",
		"https://host/r.git?private_token=TOKEN":       "(withheld: the URL carries a credential)",
		"ssh://git:secret@host/r":                      "(withheld: the URL carries a credential)",
		"https://host/org/repo@v1.git":                 "(withheld: the URL carries a credential)",
		"https://TOKEN%40github.com/r":                 "(withheld: the URL carries a credential)",
		"https://user:secret%40github.com/r":           "(withheld: the URL carries a credential)",
		"https://TOKEN" + fullwidthAt + "github.com/r": "(withheld: the URL carries a credential)",
		"https://TOKEN" + smallAt + "github.com/r":     "(withheld: the URL carries a credential)",
		"https://acct.example/repo.tar?sig=TOKEN":      "(withheld: the URL carries a credential)",
		"https://host/r.git#token=TOKEN":               "(withheld: the URL carries a credential)",
		"https://redacted:secret@host/r?x":             "(withheld: the URL carries a credential)",
	} {
		got := project.RedactedSourceURL(raw)
		if got != want {
			t.Errorf("RedactedSourceURL(%q) = %q, want %q", raw, got, want)
		}
		if strings.Contains(got, "TOKEN") || strings.Contains(got, "secret") || strings.Contains(got, "pa/") {
			t.Errorf("RedactedSourceURL(%q) = %q, which still carries the credential", raw, got)
		}
		// A redacted URL saved back is refused, never stored without its
		// credential as if that were what the operator meant.
		if project.HasEmbeddedSecret(raw) && strings.Contains(got, "://") && !project.HasEmbeddedSecret(got) {
			t.Errorf("RedactedSourceURL(%q) = %q, which the write rule would accept", raw, got)
		}
	}
}

// TestSync_WarnsOfACredentialInTheURL: a row stored before the rule still
// syncs, since refusing it would leave it unsyncable, and every sync says in
// its own log that the URL carries a credential. Here the source is then
// refused by the zero policy, which still warns first and fetches nothing.
func TestSync_WarnsOfACredentialInTheURL(t *testing.T) {
	syncer := project.NewGitSyncer(t.TempDir(), nil, project.SourcePolicy{})
	var log strings.Builder
	p := project.Project{ID: 1, OrganizationID: 1, Name: "old", SCMType: project.SCMGit,
		SCMURL: "https://ghp_0123456789abcdefTOKEN@github.com/org/repo.git"}
	result, err := syncer.Sync(context.Background(), p, &log)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(log.String(), "carries a credential") {
		t.Errorf("the sync log = %q, want the credential named", log.String())
	}
	if strings.Contains(log.String(), "TOKEN") || strings.Contains(result.Err, "TOKEN") {
		t.Errorf("the sync repeated the token: log %q, error %q", log.String(), result.Err)
	}
}
