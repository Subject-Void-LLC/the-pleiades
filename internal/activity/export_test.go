package activity

// MaxPageSizeForTest exposes the read ceiling to this package's external
// test.
//
// Exposed rather than hardcoded in the assertion, because a test that
// spells the number itself keeps passing when the constant changes: it
// would then be asserting a ceiling nothing enforces, which is the shape of
// a test that cannot fail.
const MaxPageSizeForTest = maxPageSize
