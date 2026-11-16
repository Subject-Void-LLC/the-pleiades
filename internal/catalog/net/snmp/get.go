// Package snmp: "net.snmp.get", OIDs read in one GET or each walked, every
// value masked and bounded, changing nothing.
package snmp

import (
	"context"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/snmp"
)

// getFQCN is the method's name.
const getFQCN = "net.snmp.get"

// net.snmp.get's parameters and stats.
const (
	paramOIDs       = "oids"
	paramWalk       = "walk"
	paramMaxResults = "max_results"
	statValues      = "values"
	statTruncated   = "truncated"
)

// defaultMaxResults is how many bindings a walk returns unless max_results
// says otherwise.
const defaultMaxResults = 1000

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: getFQCN,
		Manifest: collection.Manifest{
			SupportedTransports:  []string{capability.TransportSNMP},
			RequiredCapabilities: []capability.Name{capability.NameSNMP},
			ExecutionContext: collection.ExecutionContext{
				Site:   collection.SiteTarget,
				Device: collection.DeviceRequired,
			},
			EngineVersion:      ">=0.4.0",
			Status:             collection.StatusImplemented,
			NoBecomeReason:     noBecomeReason,
			SupportsCheck:      true,
			UsesSNMPCredential: true,
			NoExistsReason:     "it reads OIDs and answers each one, an object the agent does not hold included, as a value of type no-such-object or no-such-instance",
			Reversibility: collection.Reversibility{
				Reversible: false,
				Notes:      "A GET or a walk changes nothing on the device, so there is nothing to undo.",
				ReadOnly:   true,
			},
			Doc: getDoc(),
		},
		Invoke: Get,
		Check:  Get,
	})
}

// getDoc is net.snmp.get's documentation, word for word its catalog entry's.
func getDoc() collection.Doc {
	return collection.Doc{
		Summary: "Reads OIDs from a device's SNMP agent, in one GET or each walked, changing nothing.",
		Description: "Reads the OIDs oids names from the device's SNMP agent with the SNMP family of its own credential, through the client onboarding and wait.snmp use: SNMPv3 unless the device's record opts into SNMPv1 or SNMPv2c (snmp_allow_cleartext_community), every one of which is warned about. Without walk the OIDs are read in one GET, at most 60 of them, and an object the agent does not hold is answered as a value of type no-such-object or no-such-instance; an SNMPv1 agent answers that noSuchName instead, which fails the task naming the OID. With walk each OID is the root of a subtree read in order, with GETBULK (unless the device's record sets snmp_no_bulk, or it speaks SNMPv1) or GETNEXT, until the subtree ends or max_results bindings are read, and an agent answering an OID not after the one asked from stops the walk rather than loop. " +
			"An OID is numeric (1.3.6.1.2.1.1.5.0) or a name from a small curated table with its instance (sysName.0, ifDescr.2, SNMPv2-MIB::sysName.0): the MIB-II system and interfaces groups, IF-MIB's ifXTable and the SNMP engine group; anything else is refused before anything is sent, and is written numerically. " +
			"Every value is masked of the device's credential, and every octet string in the agent's community table (SNMP-COMMUNITY-MIB) or USM user table is masked whole, whatever it holds. Reports no change, and a check runs it for real.",
		Params: []collection.Param{
			{Name: paramOIDs, Type: "list", Required: true, Description: "The OIDs to read, or with walk the roots of the subtrees to walk: numeric, or a curated name with its instance (sysName.0). From 1 to 60 of them."},
			{Name: paramWalk, Type: "bool", Default: "false", Description: "Walk each OID's subtree instead of reading the OID itself."},
			{Name: paramMaxResults, Type: "int", Default: "1000", Description: "With walk, the most bindings read for each OID, from 1 to 10000; a walk that stops there says so with truncated. Refused without walk."},
		},
		Returns: []collection.ReturnField{
			{Name: statValues, Type: "list of dict", Returned: "always", Description: "Each binding read, in order: oid (numeric), type (integer, octet-string, object-identifier, ip-address, counter32, gauge32, timeticks, counter64, opaque, null, no-such-object, no-such-instance or end-of-mib-view) and value as text (a number in decimal, an OID dotted, an octet string as itself, or as 0x and hex when it is not printable, with hex true)."},
			{Name: statTruncated, Type: "bool", Returned: "with walk", Description: "Whether a walk stopped at max_results before its subtree ended, so values may hold only its first part."},
		},
		Examples: []collection.Example{
			{Name: "Read a device's name and uptime", RunbookYAML: "- name: Read the agent's name and uptime\n  net.snmp.get:\n    oids:\n      - sysName.0\n      - sysUpTime.0\n  register: system\n"},
			{Name: "Walk the interface descriptions", RunbookYAML: "- name: Walk ifDescr\n  net.snmp.get:\n    oids:\n      - ifDescr\n    walk: true\n    max_results: 500\n"},
		},
		SeeAlso: []string{"net.snmp.facts", "net.snmp.set", "wait.snmp"},
	}
}

// getRequest is one checked call.
type getRequest struct {
	oids       []string
	walk       bool
	maxResults int
}

// readGet reads and checks the parameters, refusing a malformed one before
// anything is sent.
func readGet(params map[string]any) (getRequest, error) {
	var req getRequest
	raw, ok, err := sdk.StringSlice(params, paramOIDs)
	if err != nil {
		return req, err
	}
	if !ok || len(raw) == 0 || len(raw) > snmp.MaxOIDsPerRequest {
		return req, fmt.Errorf("%s names from 1 to %d OIDs", paramOIDs, snmp.MaxOIDsPerRequest)
	}
	for _, text := range raw {
		oid, err := snmp.ParseOID(text)
		if err != nil {
			return req, fmt.Errorf("%s: %w", paramOIDs, err)
		}
		req.oids = append(req.oids, oid)
	}
	if req.walk, err = sdk.BoolParamOr(params, paramWalk, false); err != nil {
		return req, err
	}
	n, set, err := sdk.IntParam(params, paramMaxResults)
	switch {
	case err != nil:
		return req, err
	case set && !req.walk:
		return req, fmt.Errorf("%s bounds a walk; without walk the OIDs are read in one GET", paramMaxResults)
	case !set:
		n = defaultMaxResults
	case n < 1 || n > snmp.MaxWalkResults:
		return req, fmt.Errorf("%s must be from 1 to %d, not %d", paramMaxResults, snmp.MaxWalkResults, n)
	}
	req.maxResults = n
	return req, nil
}

// Get implements the "net.snmp.get" collection method.
func Get(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	req, err := readGet(params)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", getFQCN, err)
	}
	s, err := open(rc, device, getFQCN)
	if err != nil {
		return collection.Result{}, err
	}
	defer s.close()
	var read []snmp.Varbind
	truncated := false
	if req.walk {
		for _, root := range req.oids {
			res, err := s.client.Walk(ctx, root, req.maxResults)
			if err != nil {
				return collection.Result{}, fmt.Errorf("%s: device %q: walking %s: %w", getFQCN, device.Name(), root, err)
			}
			read = append(read, res.Bindings...)
			truncated = truncated || res.Truncated
		}
	} else if read, err = s.client.Get(ctx, req.oids); err != nil {
		return collection.Result{}, fmt.Errorf("%s: device %q: %w", getFQCN, device.Name(), err)
	}
	values := make([]map[string]any, 0, len(read))
	for _, v := range read {
		values = append(values, s.binding(v))
	}
	if err := rc.SetStat(statValues, values); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", getFQCN, err)
	}
	if req.walk {
		if err := rc.SetStat(statTruncated, truncated); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", getFQCN, err)
		}
	}
	if !s.client.Authenticated() {
		if err := s.warn(rc, "the device was reached without SNMPv3 authentication, so nothing proves which agent answered these values"); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", getFQCN, err)
		}
	}
	return collection.Result{Changed: false}, nil
}
