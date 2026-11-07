// Package main: the external Collection protocol window's contract.
//
// extension-protocols.json lists the protocol versions a release speaks
// (pkg/external.Protocols). A version gone, or one moved from supported to
// deprecated, strands or warns every external Collection built for it, so
// both are breaks: a minor release names them in a fragment, and a patch
// release, which never removes or deprecates a protocol, cannot make them.
package main

// compareProtocols compares two protocol documents.
func (c *schemaComparer) compareProtocols(o, n map[string]any) {
	olds, news := byVersion(o["protocols"]), byVersion(n["protocols"])
	for _, v := range sortedKeys(olds) {
		at := "/protocols/" + escape(v)
		np, still := news[v]
		switch {
		case !still:
			c.report("protocol-removed", at, both, input)
		case olds[v]["status"] == "supported" && np["status"] != "supported":
			c.report("protocol-deprecated", at+"/status", both, input)
		}
	}
}

// byVersion keys a protocols list by its version, written as text.
func byVersion(v any) map[string]map[string]any {
	out := map[string]map[string]any{}
	list, _ := v.([]any)
	for _, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if n, ok := m["version"].(float64); ok {
			out[canonical(n)] = m
		}
	}
	return out
}
