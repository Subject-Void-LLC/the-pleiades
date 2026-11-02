// Exports of unexported names for this package's external tests.
package remotefile

// ParseLocation is parseLocation, for its tests of output a device should
// never print.
var ParseLocation = parseLocation

// RemovableScript is removableScript, for its tests run under a chosen shell
// and a stand-in id.
const RemovableScript = removableScript
