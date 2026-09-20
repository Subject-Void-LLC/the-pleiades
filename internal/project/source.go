// This file decides which sources a project may be pointed at.
//
// Before it, nothing constrained a project's URL at all: the only check
// anywhere was that a git project's URL is not empty, and the string went
// straight to go-git. go-git's default registry serves five protocols, and a
// string with no scheme at all is a LOCAL PATH, absolutized against whatever
// working directory the Controller happens to have. So anybody holding
// project:write could aim the Controller at a repository on its own disk or
// anywhere on its network.
//
// # Why the scheme is the thing being checked
//
// What a sync produces is not data. It is code this platform then runs against
// managed devices, which makes integrity in transit the property that matters
// most: a clone over plain http or the git daemon protocol can be substituted
// in flight by anything on the path. Secrecy is the second reason and applies
// to http alone, which puts a bound credential's password on the wire.
//
// # What this deliberately does NOT promise
//
// Three limits, stated here rather than left for somebody to discover:
//
//  1. It is not a host allowlist. go-git reads anything shaped like
//     "host:path" as ssh, so "srv:secrets-repo" is a valid ssh endpoint and an
//     allowed scheme still reaches any host the Controller can resolve.
//     Restricting hosts is a larger decision and is not made here.
//  2. go-git's http client follows redirects, so an allowed https URL can land
//     somewhere else, including on http, without passing through this check
//     again.
//  3. Refusing local paths is defense in depth rather than a patch for
//     something exploitable in the shipped image: go-git's file transport
//     shells out to git-upload-pack, and the Controller image carries no git
//     binary, so a local source fails there anyway. It works on a developer's
//     machine, which is exactly why the tests that use one now say so.
package project

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/go-git/go-git/v5/plumbing/transport"
)

// Refusals a caller distinguishes.
var (
	// ErrSourceRefused is a source this deployment will not fetch from.
	ErrSourceRefused = errors.New("project: this deployment will not fetch from that source")

	// ErrSourceSecretInURL is a URL carrying a password or token in its
	// userinfo.
	//
	// Refused at the write rather than at the sync, and the asymmetry is
	// deliberate: scm_url is an ordinary column while a credential is
	// envelope-encrypted, so this keeps a new secret out of the database,
	// while an existing row that already has one keeps working (scrubURL is
	// what stops it reaching an error message). Refusing it at sync time would
	// make such a row permanently unsyncable with no migration to move the
	// secret anywhere.
	ErrSourceSecretInURL = errors.New("project: a password in the URL would be stored unencrypted")
)

// The protocols go-git can dial, and what each one costs.
//
// An allowlist rather than a denylist, for the reason internal/topology's own
// scheme list gives: a denylist admits every protocol nobody thought to
// forbid. Here the failure a denylist would let through is quiet in the worst
// way, since an unprotected fetch produces code that runs on real devices and
// looks identical to a protected one afterward.
const (
	// ProtocolHTTPS and ProtocolSSH are allowed by default: both
	// authenticate the far end and protect what comes back.
	ProtocolHTTPS = "https"
	ProtocolSSH   = "ssh"

	// ProtocolHTTP and ProtocolGit protect neither. The git daemon protocol
	// has no authentication at all.
	ProtocolHTTP = "http"
	ProtocolGit  = "git"

	// ProtocolFile is a path on the Controller's own disk, which is what a
	// URL with no scheme becomes.
	ProtocolFile = "file"
)

// sourcePolicyToggle names the deployment toggle that would allow a protocol,
// so a refusal can say what to do about itself rather than only that it
// happened.
var sourcePolicyToggle = map[string]string{
	ProtocolHTTP: "PLEIADES_PROJECT_ALLOW_INSECURE_SOURCE",
	ProtocolGit:  "PLEIADES_PROJECT_ALLOW_INSECURE_SOURCE",
	ProtocolFile: "PLEIADES_PROJECT_ALLOW_LOCAL_SOURCE",
}

// SourcePolicy is the deployment's half of the source decision.
//
// A value threaded from the composition root, never a package variable and
// never re-read from the environment downstream, so what a project is judged
// by is what the Controller was started with. The zero value refuses
// everything but https and ssh, which is what makes a forgotten threading fail
// closed rather than open.
type SourcePolicy struct {
	// AllowInsecureTransport admits http and the git daemon protocol.
	//
	// It exists for a deployment whose internal mirror speaks plain http, and
	// it is one decision rather than per-project, because "this deployment
	// accepts unverified automation content" is not a judgement a person
	// filling in a form is positioned to make.
	AllowInsecureTransport bool

	// AllowLocalPath admits file:// URLs and bare paths, which is what a
	// development machine and this package's own tests use.
	AllowLocalPath bool
}

// ClassifySource reports the protocol go-git will dial for raw.
//
// It asks go-git rather than parsing the string here, and that is the whole
// point of the function. A second parser would be free to disagree with the
// one that actually dials, and the disagreement would be a source this
// validator called https and go-git treated as something else. transport
// .NewEndpoint is the same call the transport layer makes.
func ClassifySource(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		// Answered before classification, because NewEndpoint does not fail
		// on an empty string: it returns a local path to the Controller's
		// working directory, so an empty URL would be reported as a refused
		// local source rather than as a missing value.
		return "", fmt.Errorf("%w: a git project needs a repository URL", ErrSourceRefused)
	}

	endpoint, err := transport.NewEndpoint(trimmed)
	if err != nil {
		return "", fmt.Errorf("%w: %s is not a repository address", ErrSourceRefused, scrubURL(err.Error(), trimmed))
	}
	return endpoint.Protocol, nil
}

// HasEmbeddedSecret reports whether raw carries a password in its userinfo.
//
// Keyed on the password alone. A user is ordinary and often required: the
// "git" in git@github.com:org/repo.git is a username, and refusing that would
// refuse the single most common way of writing an ssh remote.
func HasEmbeddedSecret(raw string) bool {
	endpoint, err := transport.NewEndpoint(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	return endpoint.Password != ""
}

// AdmitsSource reports whether this deployment will fetch from raw.
//
// It does not look at the project's SCM type. That is the caller's question:
// only a git project is ever dialed today, and an archive project's URL should
// still be a checked value on the day archive ships rather than whatever was
// stored while nothing read it.
func (p SourcePolicy) AdmitsSource(raw string) error {
	protocol, err := ClassifySource(raw)
	if err != nil {
		return err
	}

	switch protocol {
	case ProtocolHTTPS, ProtocolSSH:
		return nil
	case ProtocolHTTP, ProtocolGit:
		if p.AllowInsecureTransport {
			return nil
		}
	case ProtocolFile:
		if p.AllowLocalPath {
			return nil
		}
	}

	return fmt.Errorf("%w: %s", ErrSourceRefused, p.refusal(protocol))
}

// refusal explains one protocol's refusal: what was read, what is accepted,
// and which toggle would change the answer.
//
// The protocol is named rather than the URL, because a URL can carry a
// credential and this message reaches a form, a log and a stored sync failure.
func (p SourcePolicy) refusal(protocol string) string {
	msg := fmt.Sprintf("that address is %s, and this deployment fetches from %s",
		describeProtocol(protocol), strings.Join(p.allowed(), " or "))
	if toggle, ok := sourcePolicyToggle[protocol]; ok {
		msg += fmt.Sprintf(" (set %s to allow it)", toggle)
	}
	return msg
}

// allowed lists the protocols this policy accepts, in a stable order so a
// message does not reorder itself between reads.
func (p SourcePolicy) allowed() []string {
	allowed := []string{ProtocolHTTPS, ProtocolSSH}
	if p.AllowInsecureTransport {
		allowed = append(allowed, ProtocolHTTP, ProtocolGit)
	}
	if p.AllowLocalPath {
		allowed = append(allowed, ProtocolFile)
	}
	sort.Strings(allowed)
	return allowed
}

// describeProtocol renders a protocol as something a person reads, naming what
// is wrong with it rather than only what it is.
func describeProtocol(protocol string) string {
	switch protocol {
	case ProtocolHTTP:
		return "plain http, which neither proves what sent the code nor stops it being changed in transit"
	case ProtocolGit:
		return "the git daemon protocol, which has no authentication and no encryption"
	case ProtocolFile:
		return "a path on this server's own disk"
	case "":
		return "of no recognisable protocol"
	default:
		return protocol
	}
}
