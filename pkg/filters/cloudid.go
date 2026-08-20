package filters

import "strings"

// ParseARN parses an AWS ARN ("arn:partition:service:region:account-id:
// resource") into its six components. resource itself is further split
// into resource_type/resource_id at its first "/" or ":" (whichever
// occurs first), since both separators are real across different AWS
// services (IAM uses "role/MyRole", RDS uses "db:mydatabase"); a
// resource with neither separator (S3's bucket-name-only form) yields an
// empty resource_type. The raw, unsplit resource string is always kept
// under "resource" too, and resource_delimiter records which separator
// (if any) was found, so BuildARN can reconstruct byte-for-byte from
// either the raw or the split form. Returns nil if arn does not start
// with the literal "arn:" or does not have exactly six colon-separated
// fields (a resource containing its own colon, e.g. RDS's "db:name",
// still parses correctly: the split uses SplitN with a limit of 6, so
// only the first five colons are treated as field separators).
func ParseARN(arn string) map[string]any {
	if len(arn) > MaxInputBytes {
		return nil
	}
	fields := strings.SplitN(arn, ":", 6)
	if len(fields) != 6 || fields[0] != "arn" {
		return nil
	}
	resource := fields[5]
	resourceType, resourceID, delimiter := splitARNResource(resource)
	return map[string]any{
		"partition":          fields[1],
		"service":            fields[2],
		"region":             fields[3],
		"account_id":         fields[4],
		"resource":           resource,
		"resource_type":      resourceType,
		"resource_id":        resourceID,
		"resource_delimiter": delimiter,
	}
}

// splitARNResource splits an ARN's resource field at its first "/" or
// ":", whichever occurs first, returning "" for the type and the whole
// string for the id when neither is present.
func splitARNResource(resource string) (resourceType, resourceID, delimiter string) {
	idx := strings.IndexAny(resource, "/:")
	if idx < 0 {
		return "", resource, ""
	}
	return resource[:idx], resource[idx+1:], string(resource[idx])
}

// BuildARN reconstructs an ARN string from a map shaped like ParseARN's
// own return value, the inverse of ParseARN. partition/service/region/
// account_id default to "" when absent (a real ARN legitimately leaves
// region and account_id blank, e.g. S3's "arn:aws:s3:::bucket-name"), so
// only a resource is required. The resource itself is resolved in
// priority order: a string "resource" value is used verbatim (this is
// what makes BuildARN(ParseARN(x)) == x exact for every x ParseARN
// accepts, since ParseARN always sets "resource" to the original,
// unsplit field); otherwise "resource_type" and "resource_id" are joined
// with "resource_delimiter" (defaulting to "/", the most common
// separator across AWS services) when resource_type is non-empty, or
// "resource_id" alone otherwise. Returns "" if none of "resource" or
// "resource_id" is present as a string.
func BuildARN(parts map[string]any) string {
	partition := stringFieldOr(parts, "partition", "")
	service := stringFieldOr(parts, "service", "")
	region := stringFieldOr(parts, "region", "")
	accountID := stringFieldOr(parts, "account_id", "")

	resource, ok := parts["resource"].(string)
	if !ok {
		resourceID, hasID := parts["resource_id"].(string)
		if !hasID {
			return ""
		}
		resourceType := stringFieldOr(parts, "resource_type", "")
		if resourceType == "" {
			resource = resourceID
		} else {
			delimiter := stringFieldOr(parts, "resource_delimiter", "/")
			if delimiter == "" {
				delimiter = "/"
			}
			resource = resourceType + delimiter + resourceID
		}
	}

	return "arn:" + partition + ":" + service + ":" + region + ":" + accountID + ":" + resource
}

// stringFieldOr reads m[key] as a string, returning def if the key is
// absent or not a string.
func stringFieldOr(m map[string]any, key, def string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return def
}

// ParseAzureResourceID parses an Azure Resource Manager resource ID
// ("/subscriptions/{sub}/resourceGroups/{rg}/providers/{provider}/
// {type}/{name}[/{type}/{name}...]") into its subscription, resource
// group, provider namespace, and the ordered list of type/name pairs
// that follow it -- a resource ID may name a nested child resource (e.g.
// a subnet under a virtual network), which is why resource_types/
// resource_names are lists rather than a single pair; resource_type/
// resource_name additionally hold the last (leaf) pair for the common
// case of a top-level resource, where there is only one.
//
// This recognizes exactly Azure's own standard casing and segment order
// ("subscriptions", "resourceGroups", "providers", each spelled exactly
// as ARM itself emits them) and nothing else -- documented scope, not an
// attempt at every ARM ID variant (subscription-scoped, management-group
// -scoped, or extension resource IDs are out of scope). Returns nil if
// id does not match that shape, or its type/name segments after
// "providers/{provider}" do not come in complete pairs.
func ParseAzureResourceID(id string) map[string]any {
	if len(id) > MaxInputBytes {
		return nil
	}
	segs := strings.Split(strings.Trim(id, "/"), "/")
	if len(segs) < 7 {
		return nil
	}
	if segs[0] != "subscriptions" || segs[2] != "resourceGroups" || segs[4] != "providers" {
		return nil
	}
	rest := segs[6:]
	if len(rest)%2 != 0 || len(rest) == 0 {
		return nil
	}

	resourceTypes := make([]any, 0, len(rest)/2)
	resourceNames := make([]any, 0, len(rest)/2)
	for i := 0; i < len(rest); i += 2 {
		resourceTypes = append(resourceTypes, rest[i])
		resourceNames = append(resourceNames, rest[i+1])
	}

	return map[string]any{
		"subscription_id": segs[1],
		"resource_group":  segs[3],
		"provider":        segs[5],
		"resource_types":  resourceTypes,
		"resource_names":  resourceNames,
		"resource_type":   resourceTypes[len(resourceTypes)-1],
		"resource_name":   resourceNames[len(resourceNames)-1],
	}
}

// BuildAzureResourceID reconstructs an Azure resource ID from a map
// shaped like ParseAzureResourceID's own return value, the inverse of
// ParseAzureResourceID. resource_types/resource_names must both be
// present, of equal non-zero length, and hold only strings (accepted as
// either []any or []string, since a caller may have built the map by
// hand rather than received it from ParseAzureResourceID); every other
// mismatch, and a missing or empty subscription_id/resource_group/
// provider, returns "".
func BuildAzureResourceID(parts map[string]any) string {
	subscriptionID := stringFieldOr(parts, "subscription_id", "")
	resourceGroup := stringFieldOr(parts, "resource_group", "")
	provider := stringFieldOr(parts, "provider", "")
	if subscriptionID == "" || resourceGroup == "" || provider == "" {
		return ""
	}

	types, ok := anyToStringSlice(parts["resource_types"])
	if !ok || len(types) == 0 {
		return ""
	}
	names, ok := anyToStringSlice(parts["resource_names"])
	if !ok || len(names) != len(types) {
		return ""
	}

	var b strings.Builder
	b.WriteString("/subscriptions/")
	b.WriteString(subscriptionID)
	b.WriteString("/resourceGroups/")
	b.WriteString(resourceGroup)
	b.WriteString("/providers/")
	b.WriteString(provider)
	for i := range types {
		b.WriteByte('/')
		b.WriteString(types[i])
		b.WriteByte('/')
		b.WriteString(names[i])
	}
	return b.String()
}

// anyToStringSlice converts v to a []string when it is either already
// one or a []any of strings (the shape a map value takes after
// round-tripping through this package's own CEL translation layer,
// internal/engine/cel_filters.go's celToAny). Reports false for anything
// else, including a []any containing a non-string element.
func anyToStringSlice(v any) ([]string, bool) {
	switch t := v.(type) {
	case []string:
		return t, true
	case []any:
		out := make([]string, len(t))
		for i, e := range t {
			s, ok := e.(string)
			if !ok {
				return nil, false
			}
			out[i] = s
		}
		return out, true
	default:
		return nil, false
	}
}

// gcpSelfLinkScopes are the three location scopes a GCP compute
// self-link's path can name after "projects/{project}/": a zonal
// resource ("zones/{zone}/{type}/{name}"), a regional one
// ("regions/{region}/{type}/{name}"), or a global one
// ("global/{type}/{name}", with no location segment at all). Anything
// else is out of scope for ParseGCPSelfLink.
var gcpSelfLinkScopes = map[string]bool{"zones": true, "regions": true}

// ParseGCPSelfLink parses a GCP Compute Engine "selfLink" URL (e.g.
// "https://www.googleapis.com/compute/v1/projects/my-project/zones/
// us-central1-a/instances/my-vm") into its project, location scope
// ("zone", "region", or "global"), location name (empty for "global"),
// resource type, and resource name. api holds the path segment(s)
// between the host and "projects" (e.g. "compute/v1"). This recognizes
// exactly the "projects/{project}/{zones,regions}/{location}/{type}/
// {name}" and "projects/{project}/global/{type}/{name}" shapes -- the
// two that cover the overwhelming majority of GCP resource self-links --
// and returns nil for anything else, including a self-link with no
// "projects" segment at all.
func ParseGCPSelfLink(selfLink string) map[string]any {
	u, ok := parseURLWithScheme(selfLink)
	if !ok {
		return nil
	}
	segs := strings.Split(strings.Trim(u.Path, "/"), "/")

	projIdx := -1
	for i, s := range segs {
		if s == "projects" {
			projIdx = i
			break
		}
	}
	if projIdx < 0 || projIdx+1 >= len(segs) {
		return nil
	}
	project := segs[projIdx+1]
	rest := segs[projIdx+2:]
	if len(rest) == 0 {
		return nil
	}

	var scope, location, resourceType, resourceName string
	if rest[0] == "global" {
		if len(rest) < 3 {
			return nil
		}
		scope, location, resourceType, resourceName = "global", "", rest[1], rest[2]
	} else if gcpSelfLinkScopes[rest[0]] {
		if len(rest) < 4 {
			return nil
		}
		scope, location, resourceType, resourceName = strings.TrimSuffix(rest[0], "s"), rest[1], rest[2], rest[3]
	} else {
		return nil
	}

	return map[string]any{
		"project":       project,
		"scope":         scope,
		"location":      location,
		"resource_type": resourceType,
		"resource_name": resourceName,
		"api":           strings.Join(segs[:projIdx], "/"),
	}
}

// gcpIAMMemberTypes are the IAM member type prefixes ParseGCPIAMMember
// recognizes before a colon-separated identifier: "user:", "serviceAccount:",
// "group:", and "domain:". "principal:"/"principalSet:" (workload
// identity federation's newer member forms) are out of scope.
var gcpIAMMemberTypes = map[string]bool{
	"user":           true,
	"serviceAccount": true,
	"group":          true,
	"domain":         true,
}

// ParseGCPIAMMember parses a GCP IAM policy binding's member string into
// its type, identifier, and deletion state. Recognizes the two
// no-identifier singleton forms ("allUsers", "allAuthenticatedUsers");
// the four "type:identifier" forms this file curates (see
// gcpIAMMemberTypes); an optional "deleted:" prefix on any of the above,
// which GCP uses to mark a principal that no longer exists (deleted is
// true, and the returned type/id are still those of the deleted
// principal); and an optional trailing "?uid=..." GCP appends to a
// deleted principal's identifier, captured separately in uid rather than
// left embedded in id. Returns nil for anything else, including an
// unrecognized type prefix or a "type:" with an empty identifier.
func ParseGCPIAMMember(member string) map[string]any {
	if len(member) > MaxInputBytes {
		return nil
	}
	s := member
	deleted := false
	if rest, ok := strings.CutPrefix(s, "deleted:"); ok {
		deleted = true
		s = rest
	}

	if s == "allUsers" || s == "allAuthenticatedUsers" {
		return map[string]any{"type": s, "id": "", "deleted": deleted, "uid": ""}
	}

	idx := strings.IndexByte(s, ':')
	if idx < 0 {
		return nil
	}
	memberType, id := s[:idx], s[idx+1:]
	if !gcpIAMMemberTypes[memberType] {
		return nil
	}

	uid := ""
	if q := strings.Index(id, "?uid="); q >= 0 {
		uid = id[q+len("?uid="):]
		id = id[:q]
	}
	if id == "" {
		return nil
	}

	return map[string]any{"type": memberType, "id": id, "deleted": deleted, "uid": uid}
}
