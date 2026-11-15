// wait.restconf's reference documentation, which
// internal/forge/catalogdata holds word for word (internal/archtest's
// TestCatalogDataDocsMatchTheRegistry).
package wait

import "github.com/Subject-Void-LLC/the-pleiades/pkg/collection"

// restconfDoc is wait.restconf's Doc. Its timing parameters are
// wait.netconf's, word for word but connect_timeout's, since the two share
// readConnection.
var restconfDoc = collection.Doc{
	Summary: "Waits until the target's RESTCONF server answers its account with the capability list under its root.",
	Description: "Tries the device's RESTCONF server until an authenticated read under its root answers, and fails when the timeout runs out first: each try reads the origin's /.well-known/host-meta with no credential to find the root (taking the conventional /restconf when it answers 404), then, with the device's credential, the capability list ietf-restconf-monitoring keeps under it, through the same client the net.restconf.* methods use. " +
		"It is the task to put between one that starts a network device and the first net.restconf.* task: a device's HTTPS can answer long before its RESTCONF does (a Catalyst 8000V running IOS XE 17.15.1a, measured, served its web server's own page at / over TLS 1.3 while every read under /restconf answered 502 for more than seven minutes after its licence reload), so an answer at / is not readiness, and wait.connection, which logs in over SSH, says nothing about it. " +
		"Each try is a new client on a new connection, so a connection kept open from an earlier try is never taken for an answer. " +
		"The device's own TLS settings decide which certificate is trusted, and nothing here can weaken them: a certificate they do not trust ends the wait at once, before any request is sent, naming its subject and fingerprint and the tls_fingerprint or tls_ca_pem property that would trust it once checked; and so does a login the device refuses (401, or 403 to the first read under the root), since neither changes by being asked again and a password sent at every try can lock the account. " +
		"A server that refuses or resets the connection, sends no answer within connect_timeout, answers 500, 502, 503 or 504, answers anything but the capability list, or answers the capability read under the conventional root 404 is tried again until the timeout. A host-meta answered 404 is not a failed try in itself: IOS XE answers host-meta only to a valid login and 404 otherwise, a wrong password included (measured on 17.15.1a, 2026-11-15), so the credential is never sent there and the read under /restconf decides, where a wrong password is a 401 that ends the wait after one attempt. " +
		"A failed try is not an error, since it is what the task waits to see stop; the last one is quoted when the timeout ends the wait. " +
		"The delay, backoff and steady parameters work as wait.connection's do. It changes nothing.",
	Params: restconfParams(),
	Returns: []collection.ReturnField{
		{Name: "elapsed", Type: "int", Returned: "always", Description: "How many whole seconds the task waited, including the delay."},
		{Name: "root", Type: "string", Returned: "always", Description: "The RESTCONF root on the try that answered, as an escaped path such as /restconf, or / for a root at the device's origin itself."},
		{Name: "root_source", Type: "string", Returned: "always", Description: "How root was learned: host-meta (its restconf link) or default (host-meta answered 404, as IOS XE answers it to anything but a valid login, and the read under the conventional /restconf answered)."},
		{Name: "tries", Type: "int", Returned: "always", Description: "How many tries the task made, answered or not."},
		{Name: "failures", Type: "int", Returned: "always", Description: "How many of those tries did not answer."},
		{Name: "diff", Type: "dict", Returned: "always", Description: "The root that answered. Both halves are identical, since a wait changes nothing."},
	},
	Examples: []collection.Example{
		{Name: "Wait for a router's RESTCONF server after it boots", RunbookYAML: "- name: Wait for the router's RESTCONF server to answer its account\n  wait.restconf:\n    timeout: 900\n    sleep: 10\n"},
	},
	SeeAlso: []string{"wait.netconf", "net.restconf.config", "net.restconf.facts"},
}

// restconfParams are wait.netconf's timing parameters with connect_timeout
// described for a RESTCONF try.
func restconfParams() []collection.Param {
	out := make([]collection.Param, 0, len(netconfDoc.Params))
	for _, p := range netconfDoc.Params {
		if p.Name == connectionParamConnectTimeout {
			p.Description = "How many seconds one try may take, from the connection to the capability list's answer, before it counts as failed. Must be more than 0."
		}
		out = append(out, p)
	}
	return out
}
