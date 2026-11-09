// This file decides whether a project's source URL may carry a credential,
// and how such a URL may be shown.
//
// Both are allowlists, and both fail closed. The detector used to be a list
// of shapes that carry a credential, and every review found a shape the list
// had missed (a password behind a bad port, a token under git+https, a
// password holding a "/", a lenient "https:/" form, an encoded or lookalike
// "@", a signature in a query no list of secret names covered), each of
// which was then stored and shown raw (LESSONS 190: a denylist fails open on
// every shape nobody listed). It now names the few URLs that carry none.
//
// Two limits it does not claim to cover. A token in the path
// ("https://host/token/abc") cannot be told from a path, so it is stored and
// shown like any path. And a backup restore (internal/backup: pg_restore of
// a dump, or a SQLite file copy) writes rows without the store's checks, so a
// backup taken before a rule existed brings back a URL the rule now refuses;
// every reader still reads such a row and shows its URL redacted or withheld
// (RedactedSourceURL), and its next save is refused until the URL changes.
package project

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/go-git/go-git/v5/plumbing/transport"
)

// withheldSourceURL is what a URL that may carry a credential is shown as
// when its userinfo cannot be cut out exactly.
const withheldSourceURL = "(withheld: the URL carries a credential)"

// sshSchemes are the schemes under which a user name alone is a login, not a
// credential, compared without regard to case.
var sshSchemes = map[string]bool{"ssh": true, "git+ssh": true, "ssh+git": true}

// atLookalikes are the texts read as an "@" though they are not one: its
// percent encoding, the two Unicode lookalikes (FULLWIDTH COMMERCIAL AT,
// U+FF20, and SMALL COMMERCIAL AT, U+FE6B), and their percent encodings.
// Some reader along the way (a URL decoder, an IDNA or NFKC mapping) can turn
// any of them into a separator, and none belongs in a repository URL. Built
// from code points so this source holds no confusable character
// (FAILURE_PATTERNS 256, 834). The encoded forms are compared lowercased.
var atLookalikes = []string{
	"%40",
	string(rune(0xFF20)), "%ef%bc%a0",
	string(rune(0xFE6B)), "%ef%b9%ab",
}

// HasEmbeddedSecret reports whether raw may carry a credential.
//
// The rule is an allowlist:
//
//   - A URL with a query or a fragment ("?" or "#" anywhere) may carry one,
//     whatever the parameter is called: an Azure SAS "sig", an S3
//     "X-Amz-Signature", an "apikey" or a "#token=" are all credentials, no
//     list of secret names covers them all, and a repository source needs
//     neither a query nor a fragment.
//   - A URL holding an encoded or lookalike "@" (atLookalikes) may carry one.
//   - Otherwise, a URL with no "@" anywhere carries no userinfo credential.
//   - A URL with an "@" carries none only when it is exactly one of two ssh
//     logins, where the user picks the account and is not a secret:
//     (a) a URL net/url parses cleanly with exactly one "@", which is its
//     userinfo separator (the raw authority is exactly the user, "@" and the
//     host net/url read), whose user has no ":" and no "%" (no password,
//     written or encoded), under exactly the scheme ssh, git+ssh or ssh+git,
//     written "scheme://"; or (b) the scp-like user@host:path form as go-git
//     reads it, with no scheme, exactly one "@", and a user with no ":",
//     "%", "/", "\" or space.
//   - Every other URL with an "@" anywhere may carry one.
//
// So a password holding "/" ("https://user:pa/ss@host/r"), a compound scheme
// ("git+ssh+https://TOKEN@host"), a lenient slash form ("https:/user:pw@host/r")
// and a URL no parser reads are all refused, with no need to have listed
// them. The cost is refusing an "@" in a path ("https://host/org/repo@v1"),
// which is rare for a repository URL and rewritten more easily than a stored
// secret is removed. A token in the path is not caught (see the file's
// comment).
func HasEmbeddedSecret(raw string) bool {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return false
	}
	if hasQueryOrFragment(trimmed) || hasAtLookalike(trimmed) {
		return true
	}
	if !strings.Contains(trimmed, "@") {
		return false
	}
	return !isSSHLoginURL(trimmed) && !isSCPLikeLogin(trimmed)
}

// hasQueryOrFragment reports whether raw has a "?" or a "#" anywhere, read
// from the text so a URL no parser reads is read too.
func hasQueryOrFragment(raw string) bool {
	return strings.ContainsAny(raw, "?#")
}

// hasAtLookalike reports whether raw holds any of atLookalikes.
func hasAtLookalike(raw string) bool {
	lower := strings.ToLower(raw)
	for _, lookalike := range atLookalikes {
		if strings.Contains(lower, lookalike) {
			return true
		}
	}
	return false
}

// isSSHLoginURL reports whether raw is case (a) of HasEmbeddedSecret: an ssh
// URL whose only "@" separates a user, with no password, from its host.
func isSSHLoginURL(raw string) bool {
	if strings.Count(raw, "@") != 1 {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || u.User == nil || u.Opaque != "" || u.Host == "" || !sshSchemes[strings.ToLower(u.Scheme)] {
		return false
	}
	if _, hasPassword := u.User.Password(); hasPassword {
		return false
	}
	user := u.User.Username()
	if user == "" || strings.ContainsAny(user, ":%") {
		return false
	}
	// The raw text, not only the parse: "scheme://" written exactly, and an
	// authority that is exactly user@host, so nothing net/url read leniently
	// or moved into the path is taken for a login.
	prefix := u.Scheme + "://"
	if len(raw) < len(prefix) || !strings.EqualFold(raw[:len(prefix)], prefix) {
		return false
	}
	return cutAtAny(raw[len(prefix):], "/?#") == user+"@"+u.Host
}

// isSCPLikeLogin reports whether raw is case (b) of HasEmbeddedSecret: the
// scp-like user@host:path form go-git reads as ssh, with a plain user.
func isSCPLikeLogin(raw string) bool {
	if strings.Contains(raw, "://") || strings.Count(raw, "@") != 1 {
		return false
	}
	user, _, _ := strings.Cut(raw, "@")
	if user == "" || strings.ContainsAny(user, ":%/\\ \t") {
		return false
	}
	endpoint, err := transport.NewEndpoint(raw)
	return err == nil && endpoint.Protocol == ProtocolSSH && endpoint.User == user && endpoint.Password == ""
}

// cutAtAny returns s up to the first of any of the bytes in stops.
func cutAtAny(s, stops string) string {
	if i := strings.IndexAny(s, stops); i >= 0 {
		return s[:i]
	}
	return s
}

// RedactedSourceURL is raw as it may be shown.
//
// A URL HasEmbeddedSecret clears (no "@", no encoded or lookalike "@", no
// query and no fragment, or an ssh login) is shown as written. One it does
// not is shown with its userinfo replaced by "redacted" only when the cut is
// exact: no query, no fragment, no encoded or lookalike "@", exactly one "@",
// net/url parses it cleanly
// with that "@" as its userinfo separator, and the raw text is
// "scheme://userinfo@host" (or "//userinfo@host") with the userinfo exactly
// what net/url read. Every other such URL is withheld whole as
// "(withheld: the URL carries a credential)", since a cut whose edges are
// guessed can leave part of the secret behind. So a row stored before the
// rule never shows its credential, and every reader still reads it.
//
// Saving the shown value back is an echo that keeps the stored URL
// (KeepEchoedSourceURL), which the store then refuses for the credential it
// may carry, for a project of every type, rather than silently dropping the
// credential the sync relied on.
func RedactedSourceURL(raw string) string {
	if !HasEmbeddedSecret(raw) {
		return raw
	}
	trimmed := strings.TrimSpace(raw)
	if hasQueryOrFragment(trimmed) || hasAtLookalike(trimmed) || strings.Count(trimmed, "@") != 1 {
		return withheldSourceURL
	}
	u, err := url.Parse(trimmed)
	if err != nil || u.User == nil || u.Opaque != "" || u.Host == "" {
		return withheldSourceURL
	}
	prefix := "//"
	if u.Scheme != "" {
		prefix = u.Scheme + "://"
	}
	if len(trimmed) < len(prefix) || !strings.EqualFold(trimmed[:len(prefix)], prefix) {
		return withheldSourceURL
	}
	userinfo := u.User.String()
	if cutAtAny(trimmed[len(prefix):], "/?#") != userinfo+"@"+u.Host {
		return withheldSourceURL
	}
	u.User = url.User("redacted")
	shown := u.String()
	// The cut is checked rather than trusted: one "@", the one it put there.
	// And what is shown must still be refused as a write, so sending it back
	// as a new URL can never quietly store the URL without its credential
	// (an ssh URL with a password would otherwise show as a clean ssh login).
	if strings.Count(shown, "@") != 1 || !strings.Contains(shown, "redacted@") ||
		strings.Contains(shown, userinfo+"@") || !HasEmbeddedSecret(shown) {
		return withheldSourceURL
	}
	return shown
}

// RefuseEmbeddedSecret is the refusal of a source URL that may carry a
// credential (HasEmbeddedSecret), for a project of every type: an
// ErrSourceSecretInURL telling the caller to give the credential to the
// project instead, or nil. Its text names neither the URL nor the credential.
//
// Every type, because the concern is the column rather than the fetch:
// scm_url is stored unencrypted whatever the type, so a manual or archive
// project holding a password is a plaintext secret at rest even though
// nothing dials it. It used to be checked for git alone, behind
// checkSource's git-only return.
func RefuseEmbeddedSecret(raw string) error {
	trimmed := strings.TrimSpace(raw)
	switch {
	case hasQueryOrFragment(trimmed):
		return fmt.Errorf("%w: a source URL may not carry a query or a fragment; "+
			"give the credential to this project instead, where it is encrypted", ErrSourceSecretInURL)
	case HasEmbeddedSecret(trimmed):
		return fmt.Errorf("%w: an @ outside an ssh login, written or encoded, is refused; "+
			"give the credential to this project instead, where it is encrypted", ErrSourceSecretInURL)
	}
	return nil
}

// KeepEchoedSourceURL is the URL an edit saves when it submits submitted
// over stored: stored when submitted is exactly what RedactedSourceURL shows
// for stored, and submitted otherwise.
//
// Every edit surface shows the URL redacted or withheld (the API's GET, the
// UI's edit form), so a client that reads a project, changes one field and
// sends the rest back sends that form. Read as a new URL it replaced the
// stored one with "https://redacted@...", or with the withheld placeholder,
// which for a project the store does not dial (manual, archive) was saved as
// it was. A URL with no credential is shown as written, so for one of those
// this is plain equality, and any other value is a real change. Nothing here
// reads or returns the stored URL anywhere but into the record being saved,
// so it never reaches an error or a log.
func KeepEchoedSourceURL(stored, submitted string) string {
	if submitted == RedactedSourceURL(stored) {
		return stored
	}
	return submitted
}
