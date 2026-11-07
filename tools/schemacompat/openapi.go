// Package main: the OpenAPI envelope around the JSON Schema comparer.
//
// OpenAPI 3.1's schemas are JSON Schema 2020-12, so the one comparer serves
// them; this file walks what only OpenAPI has (paths, operations, parameters,
// request bodies, responses and each operation's required scopes) and hands
// each schema to the comparer with the direction its place gives it: what a
// client sends is input, what the Controller answers is output. The checks
// are the minimal useful subset of oasdiff's error-level ones.
package main

import "strings"

// operationMethods are the HTTP methods an OpenAPI path item may hold.
var operationMethods = []string{"get", "put", "post", "delete", "patch", "options", "head", "trace"}

// compareOpenAPI compares two OpenAPI documents.
func (c *schemaComparer) compareOpenAPI(o, n map[string]any) {
	op, _ := o["paths"].(map[string]any)
	np, _ := n["paths"].(map[string]any)
	for _, path := range sortedKeys(op) {
		at := "/paths/" + escape(path)
		nItem, ok := np[path].(map[string]any)
		if !ok {
			c.report("path-removed", at, both, input)
			continue
		}
		oItem, _ := op[path].(map[string]any)
		for _, method := range operationMethods {
			oOp, has := oItem[method].(map[string]any)
			if !has {
				continue
			}
			nOp, still := nItem[method].(map[string]any)
			if !still {
				c.report("operation-removed", at+"/"+method, both, input)
				continue
			}
			c.compareOperation(oItem, nItem, oOp, nOp, at+"/"+method)
		}
	}
}

// compareOperation compares one operation's parameters, body, responses and
// scopes.
func (c *schemaComparer) compareOperation(oItem, nItem, oOp, nOp map[string]any, at string) {
	oParams, nParams := parameters(oItem, oOp), parameters(nItem, nOp)
	for _, key := range sortedKeys(nParams) {
		nPar := nParams[key]
		pAt := at + "/parameters/" + escape(key)
		oPar, had := oParams[key]
		switch {
		case !had && truthy(nPar["required"]):
			c.report("parameter-added-required", pAt, both, input)
		case had && !truthy(oPar["required"]) && truthy(nPar["required"]):
			c.report("parameter-became-required", pAt, both, input)
		case had:
			c.compare(oPar["schema"], nPar["schema"], pAt+"/schema", input)
		}
	}
	c.compareBody(oOp, nOp, at)
	c.compareResponses(oOp, nOp, at)
	c.compareScopes(oOp, nOp, at)
}

// parameters keys an operation's parameters by location and name, the
// operation's own over its path item's, since positions in the list mean
// nothing.
func parameters(item, op map[string]any) map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, src := range []map[string]any{item, op} {
		list, _ := src["parameters"].([]any)
		for _, p := range list {
			m, ok := p.(map[string]any)
			if !ok {
				continue
			}
			in, _ := m["in"].(string)
			name, _ := m["name"].(string)
			out[in+":"+name] = m
		}
	}
	return out
}

// truthy reads a JSON boolean, treating absent and null as false.
func truthy(v any) bool {
	b, ok := v.(bool)
	return ok && b
}

// compareBody compares a request body: a body becoming required, a media
// type no longer accepted, and each media type's schema as input.
func (c *schemaComparer) compareBody(oOp, nOp map[string]any, at string) {
	ob, oHas := oOp["requestBody"].(map[string]any)
	nb, nHas := nOp["requestBody"].(map[string]any)
	bAt := at + "/requestBody"
	switch {
	case !nHas:
		return
	case !oHas:
		if truthy(nb["required"]) {
			c.report("request-body-added-required", bAt, both, input)
		}
		return
	}
	if !truthy(ob["required"]) && truthy(nb["required"]) {
		c.report("request-body-became-required", bAt, both, input)
	}
	c.compareContent(ob, nb, bAt, input)
}

// compareContent compares each media type a body or response had.
func (c *schemaComparer) compareContent(o, n map[string]any, at string, d direction) {
	oc, _ := o["content"].(map[string]any)
	nc, _ := n["content"].(map[string]any)
	for _, media := range sortedKeys(oc) {
		mAt := at + "/content/" + escape(media)
		nm, ok := nc[media].(map[string]any)
		if !ok {
			c.report("media-type-removed", mAt, both, d)
			continue
		}
		om, _ := oc[media].(map[string]any)
		c.compare(om["schema"], nm["schema"], mAt+"/schema", d)
	}
}

// compareResponses reports a success status no longer answered, and
// compares each response both sides have as output. A removed error status
// is not reported: a client handles an unknown error as an error.
func (c *schemaComparer) compareResponses(oOp, nOp map[string]any, at string) {
	or, _ := oOp["responses"].(map[string]any)
	nr, _ := nOp["responses"].(map[string]any)
	for _, status := range sortedKeys(or) {
		rAt := at + "/responses/" + escape(status)
		nResp, ok := nr[status].(map[string]any)
		if !ok {
			if strings.HasPrefix(status, "2") {
				c.report("response-status-removed", rAt, both, output)
			}
			continue
		}
		oResp, _ := or[status].(map[string]any)
		c.compareContent(oResp, nResp, rAt, output)
	}
}

// compareScopes reports a scope an operation now requires that it did not:
// a token that could call it before gets a 403.
func (c *schemaComparer) compareScopes(oOp, nOp map[string]any, at string) {
	olds, news := scopes(oOp), scopes(nOp)
	for _, s := range sortedKeys(news) {
		if !olds[s] {
			c.report("scope-added", at+"/security/"+escape(s), both, input)
		}
	}
}

// scopes collects every scope an operation's security requirements name.
func scopes(op map[string]any) map[string]bool {
	out := map[string]bool{}
	reqs, _ := op["security"].([]any)
	for _, r := range reqs {
		m, _ := r.(map[string]any)
		for scheme, list := range m {
			for s := range stringSet(list) {
				out[scheme+":"+s] = true
			}
		}
	}
	return out
}
