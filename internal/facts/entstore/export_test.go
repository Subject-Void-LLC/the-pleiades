// What this package's own tests reach that its callers cannot: compiled only
// into the test binary.
package entstore

// SetBeforeRewrite has s call hook in each re-key after the re-key has read
// and checked its device and before it writes anything (Store's
// beforeRewrite): how a test lines two re-keys up so both read the device
// before either writes.
func SetBeforeRewrite(s *Store, hook func()) {
	s.beforeRewrite = hook
}

// SetBeforeRemove has s call hook in each repair once it holds its device
// and has inspected it, before it removes anything (Store's beforeRemove):
// how a test races a gather against a repair.
func SetBeforeRemove(s *Store, hook func()) {
	s.beforeRemove = hook
}
