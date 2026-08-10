package credential

import "context"

// staticStore is a Store backed by one already-resolved secret map,
// returned regardless of the requested device name. See NewStaticStore.
type staticStore struct {
	cred Credential
	has  bool
}

// NewStaticStore returns a Store that always resolves to the Credential
// Unflatten(secrets) produces, ignoring the device name Lookup is called
// with, or ErrNotFound if secrets is empty.
//
// This is the Runner-mesh shape of credential resolution (Phase 16,
// Native Go Execution Adapter): the Controller already resolved and
// attached the one device's credential directly to
// wire.DispatchPayload.Secrets at dispatch time (PLAN.md Section 17's
// Just-in-Time delivery principle), so a Runner-side ActionExecutor that
// needs a credential.Store (internal/engine.NewTransportActionExecutor)
// has no second device to look up and no store of its own to query --
// ignoring the requested name is correct here specifically because
// exactly one device is ever in scope for a single wire.DispatchPayload,
// unlike FileStore, where the name genuinely selects among many stored
// credentials.
func NewStaticStore(secrets map[string]string) Store {
	if len(secrets) == 0 {
		return staticStore{}
	}
	return staticStore{cred: Unflatten(secrets), has: true}
}

// Lookup implements Store.
func (s staticStore) Lookup(_ context.Context, _ string) (Credential, error) {
	if !s.has {
		return Credential{}, ErrNotFound
	}
	return s.cred, nil
}
