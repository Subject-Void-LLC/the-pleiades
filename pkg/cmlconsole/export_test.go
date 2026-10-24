// Test access to the console reads' time limits.
package cmlconsole

import "time"

// SetWaits sets each server read's and each login read's time limit, and
// returns a function that puts them back.
func SetWaits(server, node time.Duration) func() {
	oldServer, oldNode := serverWait, nodeWait
	serverWait, nodeWait = server, node
	return func() { serverWait, nodeWait = oldServer, oldNode }
}
