// The named secrets a method may ask its run for, to resolve the secret
// references ({{ secret.<name>.<field> }}) a task wrote in a parameter the
// method declares takes them (collection.Param.SecretRefs).
package sdk

import (
	"errors"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/secretref"
)

// NamedSecrets is implemented by a RunbookContext that can give a method one
// field of a named secret from the project's credential store
// (`pleiades add-secret`). A value it hands out joins the run's masking set
// as it is handed out, as a registry password does. The Crawl tier implements
// it; a Runner and an external Collection's context do not, so a reference
// there fails naming the reason rather than reaching a device as literal
// text.
type NamedSecrets interface {
	// NamedSecret returns field of the secret named name, false when there is
	// no such secret or field, and an error when the store holding it cannot
	// be read (which is not the same as there being none).
	NamedSecret(name, field string) (string, bool, error)
}

// ErrNoNamedSecrets is returned for a secret reference in a run that has no
// named secrets to resolve it from.
var ErrNoNamedSecrets = errors.New("this run cannot resolve secret references: they are resolved from the project's named secrets by `pleiades run` and `pleiades adhoc` only")

// ResolveSecretRefs returns text with each secret reference resolved from
// rc's named secrets, and the values it substituted, each already added to the
// run's masking set, for the method to mask in what it returns. Text with no
// reference is returned as it is, whatever rc can resolve.
func ResolveSecretRefs(rc RunbookContext, text string) (string, []string, error) {
	if !secretref.Has(text) {
		return text, nil, nil
	}
	ns, ok := rc.(NamedSecrets)
	if !ok {
		return "", nil, ErrNoNamedSecrets
	}
	out, values, err := secretref.Resolve(text, func(r secretref.Ref) (string, bool, error) {
		return ns.NamedSecret(r.Name, r.Field)
	})
	if err != nil {
		return "", nil, err
	}
	RegisterSecret(rc, values...)
	return out, values, nil
}
