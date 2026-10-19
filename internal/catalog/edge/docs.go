// The reference documentation of the edge methods.
package edge

import "github.com/Subject-Void-LLC/the-pleiades/pkg/collection"

// readNote is what every edge method says about how it reads.
const readNote = " It reads through Cloudflare's API with the cloudflare_account device's own token (stored with add-credential --token-stdin, sent only to its api_base, no redirect followed), follows every list page by page up to 50 pages, retries a rate limit or a server error twice, and changes nothing, so a check of it is the task itself. A token without the permission a read needs is refused by Cloudflare (403), and the error says so."

// differencesReturn is every method's list of differences.
var differencesReturn = collection.ReturnField{Name: statDifferences, Type: "list of string", Returned: "when the reads succeed", Description: "Every difference from what was expected; the task fails when there is one. A read that fails (a refused token, a name that resolves to nothing) fails the task before there are differences to list."}

func tunnelDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Checks a Cloudflare Tunnel's status and how many of its connectors are up.",
		Description: "Reads a tunnel by id, or by name among the account's tunnels not deleted (a name two tunnels share is refused, naming both), and its connectors: each cloudflared process serving it, with its version, architecture and connections by colo. The task fails when the tunnel's status is not expect_status or fewer connectors than min_connectors have a live connection (one pending reconnect has dropped, and is only still tracked, so it does not count)." + readNote,
		Params: []collection.Param{
			{Name: paramTunnel, Type: "string", Required: true, Description: "The tunnel's id, or its name."},
			{Name: paramExpectStatus, Type: "string", Default: "healthy", Choices: tunnelStatuses, Description: "The status the tunnel must have; any accepts every status."},
			{Name: paramMinConnectors, Type: "int", Default: "1", Description: "The fewest connectors that must have a connection to Cloudflare's edge, 0 to 1000."},
		},
		Returns: []collection.ReturnField{
			{Name: "id", Type: "string", Returned: "success or difference", Description: "The tunnel's id."},
			{Name: "name", Type: "string", Returned: "success or difference", Description: "The tunnel's name."},
			{Name: "status", Type: "string", Returned: "success or difference", Description: "healthy, degraded, down or inactive, as Cloudflare reports it."},
			{Name: "connectors", Type: "list of dict", Returned: "success or difference", Description: "Each connector's id, version, architecture, connection count and colos."},
			{Name: "connectors_up", Type: "int", Returned: "success or difference", Description: "How many connectors have a connection."},
			{Name: "connections", Type: "int", Returned: "success or difference", Description: "How many live connections the connectors hold in all, not counting those pending reconnect."},
			differencesReturn,
		},
		Examples: []collection.Example{{Name: "The production tunnel is healthy on two connectors", RunbookYAML: "- name: Tunnel\n  edge.tunnel:\n    tunnel: prod\n    min_connectors: 2\n"}},
		SeeAlso:  []string{"edge.access", "edge.dns"},
	}
}

func accessDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Checks a Cloudflare Access application's audience, issuer, settings and policies against what is expected.",
		Description: "Reads an Access application by id, or by its domain (one two applications share is refused, naming both), its policies, and the issuer of the tokens Access signs for it (https:// and the account's team domain). Each field expect names is compared: audience, issuer and domain (domain and issuer without regard to case), type and session_duration. Policies are compared by name: a policy expected and absent is missing; one whose decision, precedence or include, exclude or require rules differ is changed, rules compared as JSON with their keys sorted; and with exact_policies one present and not expected is extra. The task fails listing every difference." + readNote,
		Params: []collection.Param{
			{Name: paramApp, Type: "string", Required: true, Description: "The application's id, or its domain."},
			{Name: paramExpect, Type: "dict", Description: "What the application must be: any of audience, issuer, domain, type, session_duration, and policies (a list of {name, decision, precedence, include, exclude, require}, each field optional but name)."},
			{Name: paramExactPolicies, Type: "bool", Default: "false", Description: "Fail on a policy the application has and expect.policies does not name."},
		},
		Returns: []collection.ReturnField{
			{Name: "id", Type: "string", Returned: "success or difference", Description: "The application's id."},
			{Name: "domain", Type: "string", Returned: "success or difference", Description: "Its domain."},
			{Name: "audience", Type: "string", Returned: "success or difference", Description: "Its audience tag (aud), which an origin validating Access tokens must expect."},
			{Name: "issuer", Type: "string", Returned: "success or difference", Description: "The issuer of its tokens."},
			{Name: "policies", Type: "list of dict", Returned: "success or difference", Description: "Each policy's name, decision and precedence."},
			differencesReturn,
		},
		Examples: []collection.Example{{Name: "Only staff reach the admin site", RunbookYAML: "- name: Access\n  edge.access:\n    app: admin.example.com\n    expect:\n      audience: 2b1d0a9c5f3e8d7a6b4c1e0f9d8c7b6a5e4d3c2b1a0f9e8d7c6b5a4f3e2d1c0b\n      issuer: https://example.cloudflareaccess.com\n      policies:\n        - name: staff\n          decision: allow\n          include: [{email_domain: {domain: example.com}}]\n    exact_policies: true\n"}},
		SeeAlso:  []string{"edge.tunnel", "edge.dns"},
	}
}

func dnsDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Checks a DNS record set in a Cloudflare zone against what is expected.",
		Description: "Reads a zone by id, or by name within the account (a zone of another account is refused, even where the token reaches it), and its records of one name and type. The set of contents is compared with expect.content, addresses in their canonical form and names in lower case without the final dot; proxied and ttl are compared on every record when expected; and absent requires there be none. With no expect, the task fails only when there is no such record." + readNote,
		Params: []collection.Param{
			{Name: paramZone, Type: "string", Required: true, Description: "The zone's id, or its name."},
			{Name: paramName, Type: "string", Required: true, Description: "The record's full name (www.example.com)."},
			{Name: paramType, Type: "string", Required: true, Choices: append([]string(nil), cfapiRecordTypes()...), Description: "The record type."},
			{Name: paramExpect, Type: "dict", Description: "What the records must be: content (one value or a list, compared as a set), proxied, ttl (1 is automatic), or absent: true."},
		},
		Returns: []collection.ReturnField{
			{Name: "zone", Type: "string", Returned: "success or difference", Description: "The zone's name."},
			{Name: "records", Type: "list of dict", Returned: "success or difference", Description: "Each record's content, proxied and ttl."},
			differencesReturn,
		},
		Examples: []collection.Example{{Name: "www points at the two load balancers, proxied", RunbookYAML: "- name: DNS\n  edge.dns:\n    zone: example.com\n    name: www.example.com\n    type: A\n    expect:\n      content: [192.0.2.10, 192.0.2.11]\n      proxied: true\n"}},
		SeeAlso:  []string{"edge.tunnel", "edge.access"},
	}
}
