// This file holds what a credential update carries rather than checks: the
// value, reference and source binding a credential stores for an input its
// type does not declare.
//
// Such an entry is what the rollback window leaves. A newer build ships an
// input, a credential stores something for it, and an older build's
// reconcile leaves the input out of the type and keeps what credentials hold
// for it, since that belongs to the newer build's release
// (adoptExistingType). The entry was written under a schema that declared
// it, so it is not the update's to check. Checking it refused every update of
// the credential until the newer build was back, rotating a compromised
// token included. So an update carries it as it is, neither checked against
// the current schema nor dropped, and leaves it to the newer build's
// reconcile, or a custom type's next edit, whose purge decides it
// (type_inputs.go). What a caller sends for such an input is still refused:
// carrying a stored entry is not writing one.
//
// Nothing shows it meanwhile, and nothing acts on it. A read redacts by the
// current schema and leaves out every value, reference and binding for an id
// it does not declare (redactInputs, project, ListCredentialInputSources):
// none is secret by that schema's say, but none is the caller's to send
// back, since its input is unknown to the type. A write replacing a
// credential's bindings replaces those of declared inputs only
// (writeInputSources). The binding rule judges a credential by its declared
// inputs (boundFor), and the resolver hands a run nothing else
// (internal/credstore/resolve), so the rule and the run agree.
package credstore

import (
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
)

// uncarried returns the inputs an update is checked by: merged without the
// values the update carries for an input schema does not declare, those
// stored holds and next does not set (the marker sets nothing).
func uncarried(schema credtype.InputSchema, stored *ent.Credential, merged map[string]string, next Credential) map[string]string {
	inputs, _ := without(merged, func(id string) bool {
		if _, held := stored.Inputs[id]; !held || declares(schema, id) {
			return false
		}
		sent, named := next.Inputs[id]
		return !named || sent == redact.Marker
	})
	return inputs
}

// withCarriedReferences returns the references an update writes: next's,
// which are checked against schema, and every reference stored holds for an
// input schema does not declare, which a read never showed the caller and the
// update carries as it carries a value.
func withCarriedReferences(schema credtype.InputSchema, stored, next map[string]string) map[string]string {
	var out map[string]string
	for id, ref := range stored {
		if declares(schema, id) {
			continue
		}
		if out == nil {
			out = make(map[string]string, len(next)+1)
			for k, v := range next {
				out[k] = v
			}
		}
		out[id] = ref
	}
	if out == nil {
		return next
	}
	return out
}

// declares reports whether schema declares the input id.
func declares(schema credtype.InputSchema, id string) bool {
	_, declared := schema.Field(id)
	return declared
}

// sealed reports whether a credential's inputs came back from the decrypting
// interceptor still encrypted, which they do when this Controller's key
// cannot open them.
func sealed(inputs map[string]string) bool {
	_, marked := inputs[crypto.EncryptedKeyMarker]
	return marked
}

// unreadable refuses a change that would have to read the stored values of a
// credential this Controller cannot decrypt. what says why it would.
//
// Carrying the sealed map into an update would seal the old ciphertext inside
// a new envelope as though it were a value, and filtering it by input id in a
// purge would save the credential empty, so both are refused instead.
func unreadable(id int, what string) error {
	return fmt.Errorf(
		"%w: credential %d's stored values cannot be read with this Controller's key, so %s; restore the key that "+
			"sealed them or delete the credential, then try again",
		ErrInUse, id, what)
}
