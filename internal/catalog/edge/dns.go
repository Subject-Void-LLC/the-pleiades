// edge.dns: one name's records of one type against what is expected.
package edge

import (
	"context"
	"fmt"
	"net/netip"
	"slices"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/cfapi"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// Parameters.
const (
	paramZone = "zone"
	paramName = "name"
	paramType = "type"
)

func init() { method(fqcnDNS, dnsDoc(), DNS) }

// DNS implements edge.dns.
func DNS(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	fail := func(err error) (collection.Result, error) {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcnDNS, err)
	}
	zoneName, err := sdk.RequiredStringParam(params, paramZone)
	if err != nil {
		return fail(err)
	}
	name, err := sdk.RequiredStringParam(params, paramName)
	if err != nil {
		return fail(err)
	}
	typ, err := sdk.RequiredStringParam(params, paramType)
	if err != nil {
		return fail(err)
	}
	if !cfapi.ValidRecordType(typ) {
		return fail(fmt.Errorf("%s %q is not one of %v", paramType, typ, cfapi.RecordTypes))
	}
	expect, err := expectMap(params, "content", "proxied", "ttl", "absent")
	if err != nil {
		return fail(err)
	}
	wantContent, err := contents(expect["content"], typ)
	if err != nil {
		return fail(err)
	}
	absent, _ := expect["absent"].(bool)
	if absent && wantContent != nil {
		return fail(fmt.Errorf("%s.absent and %s.content contradict each other", paramExpect, paramExpect))
	}
	c, acct, err := account(rc, device)
	if err != nil {
		return fail(err)
	}
	zone, err := c.Zone(ctx, acct, zoneName)
	if err != nil {
		return fail(ctxDone(ctx, err))
	}
	records, err := c.Records(ctx, zone.ID, name, typ)
	if err != nil {
		return fail(ctxDone(ctx, err))
	}
	var got []string
	listed := make([]any, len(records))
	for i, r := range records {
		got = append(got, normalize(r.Content, typ))
		listed[i] = map[string]any{"content": r.Content, "proxied": r.Proxied, "ttl": r.TTL}
	}
	slices.Sort(got)
	var diffs []string
	switch {
	case absent && len(records) > 0:
		diffs = append(diffs, fmt.Sprintf("%d %s record(s) exist where none should", len(records), typ))
	case wantContent != nil && !slices.Equal(got, wantContent):
		diffs = append(diffs, fmt.Sprintf("the records hold [%s], not [%s]", strings.Join(got, ", "), strings.Join(wantContent, ", ")))
	case !absent && len(records) == 0:
		diffs = append(diffs, fmt.Sprintf("there is no %s record for %s", typ, name))
	}
	if raw, set := expect["proxied"]; set {
		want, ok := raw.(bool)
		if !ok {
			return fail(fmt.Errorf("%s.proxied is true or false", paramExpect))
		}
		for _, r := range records {
			if r.Proxied != want {
				diffs = append(diffs, fmt.Sprintf("%s is proxied %v, not %v", r.Content, r.Proxied, want))
			}
		}
	}
	if raw, set := expect["ttl"]; set {
		want, ok := raw.(int)
		if !ok {
			return fail(fmt.Errorf("%s.ttl is a whole number of seconds, 1 for automatic", paramExpect))
		}
		for _, r := range records {
			if r.TTL != want {
				diffs = append(diffs, fmt.Sprintf("%s has ttl %d, not %d", r.Content, r.TTL, want))
			}
		}
	}
	return finish(rc, fqcnDNS, map[string]any{"zone": zone.Name, "zone_id": zone.ID, "name": name, "type": typ, "records": listed}, diffs)
}

// contents reads expect.content: one value or a list, normalized and
// sorted; nil when absent.
func contents(raw any, typ string) ([]string, error) {
	var list []string
	switch v := raw.(type) {
	case nil:
		return nil, nil
	case string:
		list = []string{v}
	case []any:
		for _, x := range v {
			s, ok := x.(string)
			if !ok {
				return nil, fmt.Errorf("%s.content holds a value that is not text", paramExpect)
			}
			list = append(list, s)
		}
	default:
		return nil, fmt.Errorf("%s.content is a record's content or a list of them", paramExpect)
	}
	out := make([]string, len(list))
	for i, s := range list {
		out[i] = normalize(s, typ)
	}
	slices.Sort(out)
	return out, nil
}

// normalize writes content as compared: an address in its canonical form,
// a name lower case without its final dot.
func normalize(content, typ string) string {
	switch typ {
	case "A", "AAAA":
		if a, err := netip.ParseAddr(content); err == nil {
			return a.String()
		}
	case "CNAME", "NS", "PTR":
		return strings.TrimSuffix(strings.ToLower(content), ".")
	}
	return content
}

// cfapiRecordTypes is the record types edge.dns reads, for its docs.
func cfapiRecordTypes() []string { return cfapi.RecordTypes }
