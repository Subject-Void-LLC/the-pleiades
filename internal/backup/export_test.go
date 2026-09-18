// Test-only access to unexported parts of the package.
package backup

// KnownTables exposes knownTables to the external tests.
var KnownTables = knownTables

// NewName exposes newName.
var NewName = newName

// Printable exposes printable.
var Printable = printable
