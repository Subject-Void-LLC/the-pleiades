// wait.netconf's reference documentation, which
// internal/forge/catalogdata holds word for word (internal/archtest's
// TestCatalogDataDocsMatchTheRegistry).
package wait

import "github.com/Subject-Void-LLC/the-pleiades/pkg/collection"

// netconfDoc is wait.netconf's Doc.
var netconfDoc = collection.Doc{
	Summary: "Waits until the target's NETCONF listener lets its account in and sends its hello.",
	Description: "Tries the device's NETCONF listener, on its netconf_port (830 unless set), until a NETCONF session opens there, and fails when the timeout runs out first. " +
		"It is the task to put between one that starts a network device and the first net.netconf.* task: a device can report booted, and answer SSH on its own port, while its NETCONF listener still refuses connections (a Catalyst 8000V's does for minutes after its lab reports it booted). " +
		"Each try connects to the listener, completes the SSH handshake, logs in afresh with the device's credential, opens the netconf subsystem and reads the server's hello, then closes the session, so a session kept open from an earlier task is never taken for a login. " +
		"The listener's host key is checked against known_hosts before any credential is sent, and that check cannot be skipped: a key known_hosts does not hold ends the wait at once, naming the trust-host command that trusts the listener (the key is seen only once the listener answers, so on a node made new the first wait ends there, with the listener up), and so does a key that differs from the one known_hosts holds, or a login the device refuses, since none of these changes by being asked again and a password sent at every try can lock the account. " +
		"A listener that does not answer, refuses the connection, declines the netconf subsystem, or sends no hello within connect_timeout is tried again until the timeout. " +
		"A failed try is not an error, since it is what the task waits to see stop; the last one is quoted when the timeout ends the wait. " +
		"The delay, backoff and steady parameters work as wait.connection's do. It changes nothing.",
	Params: []collection.Param{
		{Name: "timeout", Type: "int", Default: "600", Description: "How many seconds to wait in total before giving up, counted from the start of the task, so the delay comes out of it. Must be more than 0."},
		{Name: "delay", Type: "int", Default: "0", Description: "How many seconds to wait before the first try. Must be shorter than the timeout, which it is spent out of."},
		{Name: "sleep", Type: "int", Default: "5", Description: "How many seconds to wait between tries, or with backoff: exponential before the second try, the gap doubling after each failed one. Must be more than 0."},
		{Name: "backoff", Type: "string", Default: "fixed", Choices: []string{"fixed", "exponential"}, Description: "How the gap between failed tries grows: fixed keeps it at sleep; exponential doubles it after each failed try, up to max_sleep, each gap a random point in its upper half."},
		{Name: "max_sleep", Type: "int", Default: "60", Description: "With backoff: exponential, the longest gap between tries, in seconds. Not less than sleep; refused with backoff: fixed, where it would do nothing."},
		{Name: "steady", Type: "int", Default: "1", Description: "How many tries in a row must open a session before the listener counts as ready. Each is its own login. Must be at least 1, and the streak must fit in the timeout after the delay."},
		{Name: "steady_interval", Type: "int", Default: "10", Description: "With steady above 1, the seconds between the answering tries. Must be more than 0; refused with steady 1, where it would do nothing."},
		{Name: "connect_timeout", Type: "int", Default: "20", Description: "How many seconds one try may take, from the connection to the hello, before it counts as failed. Must be more than 0."},
	},
	Returns: []collection.ReturnField{
		{Name: "elapsed", Type: "int", Returned: "always", Description: "How many whole seconds the task waited, including the delay."},
		{Name: "port", Type: "int", Returned: "always", Description: "The port of the NETCONF listener that answered."},
		{Name: "tries", Type: "int", Returned: "always", Description: "How many tries the task made, answered or not."},
		{Name: "failures", Type: "int", Returned: "always", Description: "How many of those tries did not answer."},
		{Name: "diff", Type: "dict", Returned: "always", Description: "The port that answered. Both halves are identical, since a wait changes nothing."},
	},
	Examples: []collection.Example{
		{Name: "Wait for a router's NETCONF server after it boots", RunbookYAML: "- name: Wait for the router's NETCONF listener to let its account in\n  wait.netconf:\n    timeout: 600\n    sleep: 10\n"},
	},
	SeeAlso: []string{"wait.connection", "net.netconf.config", "virt.cml.lab.start"},
}
