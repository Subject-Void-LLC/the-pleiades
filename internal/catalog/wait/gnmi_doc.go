// wait.gnmi's reference documentation, which internal/forge/catalogdata
// holds word for word (internal/archtest's
// TestCatalogDataDocsMatchTheRegistry).
package wait

import "github.com/Subject-Void-LLC/the-pleiades/pkg/collection"

// gnmiDoc is wait.gnmi's Doc. Its timing parameters are wait.netconf's, word
// for word but connect_timeout's, since the waits share readConnection.
var gnmiDoc = collection.Doc{
	Summary: "Waits until the target's gNMI server answers its account with its Capabilities.",
	Description: "Tries the device's gNMI server until its Capabilities RPC answers with the device's own credential, and fails when the timeout runs out first, through the same client the net.gnmi.* methods use. " +
		"It is the task to put between one that starts or restarts a network device and the first net.gnmi.* task: a device's gRPC port can accept connections before its gNMI service answers, and wait.connection, which logs in over SSH, says nothing about it. " +
		"Each try is a new client on a new connection, so a connection kept open from an earlier try is never taken for an answer. " +
		"The device's own TLS settings decide which certificate is trusted, and nothing here can weaken them: a certificate they do not trust ends the wait at once, before any call is sent, naming its subject and fingerprint and the tls_fingerprint or tls_ca_pem property that would trust it once checked; and so does a login the device refuses (UNAUTHENTICATED), since neither changes by being asked again and a password sent at every try can lock the account. " +
		"A server that refuses or resets the connection, sends no answer within connect_timeout, or answers with any other gRPC status (UNAVAILABLE while it starts, among them) is tried again until the timeout. " +
		"A failed try is not an error, since it is what the task waits to see stop; the last one is quoted when the timeout ends the wait. " +
		"The delay, backoff and steady parameters work as wait.connection's do. It changes nothing.",
	Params: gnmiParams(),
	Returns: []collection.ReturnField{
		{Name: "elapsed", Type: "int", Returned: "always", Description: "How many whole seconds the task waited, including the delay."},
		{Name: "version", Type: "string", Returned: "always", Description: "The gNMI version the server named on the try that answered, such as 0.7.0; empty when it names none."},
		{Name: "tries", Type: "int", Returned: "always", Description: "How many tries the task made, answered or not."},
		{Name: "failures", Type: "int", Returned: "always", Description: "How many of those tries did not answer."},
		{Name: "diff", Type: "dict", Returned: "always", Description: "The version that answered. Both halves are identical, since a wait changes nothing."},
	},
	Examples: []collection.Example{
		{Name: "Wait for a switch's gNMI server after it boots", RunbookYAML: "- name: Wait for the switch's gNMI server to answer its account\n  wait.gnmi:\n    timeout: 600\n    sleep: 5\n"},
	},
	SeeAlso: []string{"wait.restconf", "wait.netconf", "net.gnmi.facts", "net.gnmi.config"},
}

// gnmiParams are wait.netconf's timing parameters with connect_timeout
// described for a gNMI try.
func gnmiParams() []collection.Param {
	out := make([]collection.Param, 0, len(netconfDoc.Params))
	for _, p := range netconfDoc.Params {
		if p.Name == connectionParamConnectTimeout {
			p.Description = "How many seconds one try may take, from the connection to the Capabilities answer, before it counts as failed. Must be more than 0."
		}
		out = append(out, p)
	}
	return out
}
