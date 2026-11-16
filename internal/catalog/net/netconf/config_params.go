// Package netconf: net.netconf.config's parameters, read and refused from the
// call alone, before any session opens.
package netconf

import (
	"encoding/xml"
	"fmt"
	"slices"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcfg"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netconf"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/secretref"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/yangdoc"
)

// configRequest is what a task asked of one session.
type configRequest struct {
	content          string
	target           netconf.Datastore
	lock             string
	lockSet          bool
	backup           bool
	commit           bool
	candidateCheck   bool
	defaultOperation string
	errorOption      string
}

// The values default_operation and error_option accept, RFC 6241 section
// 7.2's own.
var (
	defaultOperations = []string{netconf.DefaultOperationMerge, netconf.DefaultOperationReplace, netconf.DefaultOperationNone}
	errorOptions      = []string{netconf.ErrorOptionStopOnError, netconf.ErrorOptionContinueOnError, netconf.ErrorOptionRollbackOnError}
)

// readRequest reads and checks params, refusing from them alone what Config
// would refuse before any session opens: content missing, empty, not one
// well-formed XML document with no DOCTYPE nesting no deeper than 256
// elements, or holding a masked placeholder; a value outside a parameter's
// set; commit: false on a datastore named running or startup, where an edit
// is live at once; and lock: never on a datastore named candidate. The
// secret references in content are left as they are, unresolved.
func readRequest(params map[string]any) (configRequest, error) {
	var req configRequest
	content, err := sdk.RequiredStringParam(params, paramContent)
	if err != nil {
		return req, err
	}
	if strings.TrimSpace(content) == "" {
		return req, fmt.Errorf("%s is required and must not be empty", paramContent)
	}
	// Content built from a masked read would set each secret it carries to
	// its placeholder; refused before the session opens.
	if err := netcfg.RefuseMasked(paramContent, content); err != nil {
		return req, err
	}
	if err := yangdoc.CheckDocument(content, false); err != nil {
		return req, err
	}
	req.content = content
	if req.target, err = datastoreParam(params); err != nil {
		return req, err
	}
	if req.lock, err = lockParam(params); err != nil {
		return req, err
	}
	req.lockSet = hasParam(params, paramLock)
	if req.backup, err = sdk.BoolParamOr(params, paramBackup, false); err != nil {
		return req, err
	}
	if req.commit, err = sdk.BoolParamOr(params, paramCommit, true); err != nil {
		return req, err
	}
	if req.candidateCheck, err = sdk.BoolParamOr(params, paramCandidateCheck, false); err != nil {
		return req, err
	}
	if req.defaultOperation, err = choiceParam(params, paramDefaultOperation, defaultOperations); err != nil {
		return req, err
	}
	if req.errorOption, err = choiceParam(params, paramErrorOption, errorOptions); err != nil {
		return req, err
	}
	if !req.commit && (req.target == netconf.Running || req.target == netconf.Startup) {
		return req, commitFalseRefusal(req.target)
	}
	if req.target == netconf.Candidate && req.lockSet && req.lock == lockNever {
		return req, errLockNeverOnCandidate
	}
	return req, nil
}

// commitFalseRefusal is the refusal of commit: false where the session writes
// target, a datastore with no staging area.
func commitFalseRefusal(target netconf.Datastore) error {
	return fmt.Errorf("%s: false validates an edit in the candidate datastore and discards it, but this session writes %s, where an edit is live at once; set %s: candidate on a device that advertises it", paramCommit, target, paramDatastore)
}

// errLockNeverOnCandidate is the refusal of lock: never on the candidate.
var errLockNeverOnCandidate = fmt.Errorf("%s: never is refused on the candidate datastore: without the lock another client's staged changes would be committed with this edit, and a failed edit could not be discarded safely", paramLock)

// choiceParam is an optional text parameter, which must be one of choices
// when set; "" when it is not set.
func choiceParam(params map[string]any, name string, choices []string) (string, error) {
	v, err := sdk.OptionalStringParam(params, name)
	if err != nil || v == "" || slices.Contains(choices, v) {
		return v, err
	}
	return "", fmt.Errorf("%s: unknown value %q: valid values are %s", name, v, strings.Join(choices, ", "))
}

// datastoreParam resolves the datastore to configure, refusing an
// unrecognized name here rather than letting it reach the device as an
// element name nobody defined.
func datastoreParam(params map[string]any) (netconf.Datastore, error) {
	raw, err := sdk.OptionalStringParam(params, paramDatastore)
	if err != nil {
		return "", err
	}
	switch v := strings.ToLower(strings.TrimSpace(raw)); v {
	case "":
		// Chosen when the session opens, from what the device advertises.
		return "", nil
	case string(netconf.Running), string(netconf.Candidate), string(netconf.Startup):
		return netconf.Datastore(v), nil
	default:
		return "", fmt.Errorf("%s: unknown datastore %q: valid values are running, candidate and startup", paramDatastore, v)
	}
}

// lockParam resolves the lock policy.
func lockParam(params map[string]any) (string, error) {
	raw, err := sdk.OptionalStringParam(params, paramLock)
	if err != nil {
		return "", err
	}
	switch v := strings.ToLower(strings.TrimSpace(raw)); v {
	case "":
		return lockNever, nil
	case lockNever, lockAlways, lockIfSupported:
		return v, nil
	default:
		return "", fmt.Errorf("%s: unknown value %q: valid values are never, always and if_supported", paramLock, v)
	}
}

// hasParam reports whether the task set name at all.
func hasParam(params map[string]any, name string) bool {
	_, ok := params[name]
	return ok
}

// resolveContent is content with each secret reference resolved from the
// run's named secrets, each value escaped as XML text, since it lands in an
// element; the raw value joins the run's masking set as the named secret
// hands it out, and the escaped form joins it here, so a device error echoing
// either is masked. Content with no reference is returned as it is.
func resolveContent(rc sdk.RunbookContext, content string) (string, error) {
	if !secretref.Has(content) {
		return content, nil
	}
	ns, ok := rc.(sdk.NamedSecrets)
	if !ok {
		return "", sdk.ErrNoNamedSecrets
	}
	out, values, err := secretref.Resolve(content, func(r secretref.Ref) (string, bool, error) {
		v, found, err := ns.NamedSecret(r.Name, r.Field)
		if err != nil || !found {
			return "", found, err
		}
		var b strings.Builder
		_ = xml.EscapeText(&b, []byte(v)) // #nosec G104 -- strings.Builder writes never fail
		return b.String(), true, nil
	})
	if err != nil {
		return "", fmt.Errorf("%s: %w", paramContent, err)
	}
	sdk.RegisterSecret(rc, values...)
	return out, nil
}

// maskedBackup is a datastore's XML masked for the backup stat through
// netcfg.MaskXML: each secret leaf is replaced by its placeholder, the run's
// own credential is masked wherever it appears whole, and each secret found
// that is long enough joins the run's masking set, so a later task printing
// it is masked too. A document masking cannot vouch for returns an error and
// none of the text.
func maskedBackup(rc sdk.RunbookContext, doc string) (string, error) {
	masked, found, err := netcfg.MaskXML(doc, credentialValues(rc.InjectSecrets())...)
	if err != nil {
		return "", fmt.Errorf("the configuration is not recorded, since masking cannot vouch for it: %w", err)
	}
	sdk.RegisterSecret(rc, netcfg.Learnable(found)...)
	return masked, nil
}

// credentialValues are the secret values of the credential a run handed this
// method, leaving out the fields the engine itself does not mask by value
// (a username, a certificate, a seed's login and host), the same set the
// engine's credentialSecrets keeps.
func credentialValues(secrets map[string]string) []string {
	var out []string
	for key, value := range secrets {
		switch key {
		case wire.SecretUsername, wire.SecretCertificatePEM, wire.SecretSeedUsername, wire.SecretSeedAuthorizedKey, wire.SecretSeedHost:
			continue
		}
		out = append(out, value)
	}
	return out
}
