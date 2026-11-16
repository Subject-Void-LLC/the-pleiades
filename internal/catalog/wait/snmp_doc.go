// wait.snmp's reference documentation, which internal/forge/catalogdata
// holds word for word (internal/archtest's TestCatalogDataDocsMatchTheRegistry).
package wait

import "github.com/Subject-Void-LLC/the-pleiades/pkg/collection"

// snmpDoc is wait.snmp's Doc. Its timing parameters are wait.netconf's, word
// for word but connect_timeout's and steady's, since the two share
// readConnection.
var snmpDoc = collection.Doc{
	Summary: "Waits until the target's SNMP agent answers its credential with sysUpTime.0.",
	Description: "Tries the device's SNMP agent until a GET of sysUpTime.0 is answered with the SNMP family of the device's own credential, and fails when the timeout runs out first, through the same client the net.snmp.* methods and onboarding use: SNMPv3 unless the device's record opts into SNMPv1 or SNMPv2c (snmp_allow_cleartext_community), which is warned about. " +
		"It is the task to put between one that starts or reconfigures a device and the first net.snmp.* task: a device answers a ping, and even SSH, before its agent runs, and wait.connection says nothing about SNMP. " +
		"Each try is a new client, so a session from an earlier try is never taken for an answer. " +
		"An SNMPv3 refusal ends the wait at once (an unknown user, a wrong authentication passphrase or protocol, a security level the user is not configured for, or a request the agent could not decrypt), since none of them changes by being asked again and a credential sent at every try can lock it out; so does a device record or credential the client refuses, such as a community for an SNMPv3 device or 3des privacy. " +
		"An agent that does not answer within connect_timeout is tried again until the timeout, and that includes a wrong community: an SNMPv1 or SNMPv2c agent drops a request whose community it does not know without a word, so it reads exactly as an agent not up yet; so is a host name that does not resolve yet. An SNMPv3 user still being configured is not waited through: the agent refuses it, and the wait ends. " +
		"A failed try is not an error, since it is what the task waits to see stop; the last one is quoted when the timeout ends the wait. " +
		"The delay, backoff and steady parameters work as wait.connection's do. It changes nothing.",
	Params: snmpParams(),
	Returns: []collection.ReturnField{
		{Name: "elapsed", Type: "int", Returned: "always", Description: "How many whole seconds the task waited, including the delay."},
		{Name: "version", Type: "string", Returned: "always", Description: "The SNMP version the agent answered in: 3, 2c or 1."},
		{Name: "port", Type: "int", Returned: "always", Description: "The UDP port of the agent that answered."},
		{Name: "tries", Type: "int", Returned: "always", Description: "How many tries the task made, answered or not."},
		{Name: "failures", Type: "int", Returned: "always", Description: "How many of those tries did not answer."},
		{Name: "diff", Type: "dict", Returned: "always", Description: "The port that answered. Both halves are identical, since a wait changes nothing."},
	},
	Examples: []collection.Example{
		{Name: "Wait for a device's SNMP agent after its configuration is applied", RunbookYAML: "- name: Wait for the agent to answer its SNMPv3 user\n  wait.snmp:\n    timeout: 300\n    sleep: 5\n"},
	},
	SeeAlso: []string{"wait.connection", "net.snmp.facts", "net.snmp.get"},
}

// snmpParams are wait.netconf's timing parameters with connect_timeout and
// steady described for an SNMP try.
func snmpParams() []collection.Param {
	out := make([]collection.Param, 0, len(netconfDoc.Params))
	for _, p := range netconfDoc.Params {
		switch p.Name {
		case connectionParamConnectTimeout:
			p.Description = "How many seconds one try may take, every retry of its GET included (the device's snmp_timeout and snmp_retries), before it counts as failed. Must be more than 0."
		case connectionParamSteady:
			p.Description = "How many tries in a row must be answered before the agent counts as ready. Must be at least 1, and the streak must fit in the timeout after the delay."
		}
		out = append(out, p)
	}
	return out
}
