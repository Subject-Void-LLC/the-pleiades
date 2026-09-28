// Test-only access to the store's decoy derivation.
//
// Lives in package localauth, not localauth_test, because the field it wraps
// is unexported on purpose: production code has no reason to observe which
// path a refusal took, and a test does.
package localauth

// ObserveDecoys makes s call observe each time a call takes the decoy path,
// then run the real decoy derivation exactly as before. It counts; it never
// replaces the work, so a test using it still spends the time production
// spends.
//
// s must be a store NewEntStore or NewEntStoreWithPolicy built.
func ObserveDecoys(s Store, observe func()) {
	store := s.(*entStore)
	real := store.decoy
	store.decoy = func(password string) {
		observe()
		real(password)
	}
}
