package filters

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"mime/multipart"
	"net/textproto"
	"regexp"
	"sort"
	"strings"
)

// AWSTagListToMap converts an AWS-shaped tag list ([]map[string]any,
// each entry {"Key": "...", "Value": "..."}, the exact shape returned by
// EC2's DescribeInstances, S3's GetBucketTagging, and most other AWS
// list/describe APIs) into a flat map from key to value. Returns nil if
// any entry is missing a string "Key" or a string "Value" -- an honest
// refusal of the whole call rather than silently dropping the malformed
// entry, matching ParseX509Certificate/ParseDistinguishedName's own
// "reject entirely" convention for structurally invalid input elsewhere
// in this Part. A duplicate Key keeps its last occurrence's Value, the
// same last-write-wins rule ShallowMerge uses. An empty tags list
// returns an empty, non-nil map (a resource legitimately has zero tags;
// that is not a parse failure).
func AWSTagListToMap(tags []map[string]any) map[string]any {
	out := make(map[string]any, len(tags))
	for _, t := range tags {
		key, ok := t["Key"].(string)
		if !ok {
			return nil
		}
		value, ok := t["Value"].(string)
		if !ok {
			return nil
		}
		out[key] = value
	}
	return out
}

// MapToAWSTagList converts a flat string-valued map into an AWS-shaped
// tag list, the inverse of AWSTagListToMap. Returns nil if any value in
// m is not a string (AWS tag values are always strings; a caller with a
// non-string value has the wrong data shape, not a formatting
// preference). Output order is m's keys sorted lexically: a Go map has
// no order of its own to preserve, and a deterministic order here means
// two calls with the same map produce byte-identical JSON when
// marshaled downstream, rather than an order that changes from run to
// run.
func MapToAWSTagList(m map[string]any) []map[string]any {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	out := make([]map[string]any, 0, len(m))
	for _, k := range keys {
		v, ok := m[k].(string)
		if !ok {
			return nil
		}
		out = append(out, map[string]any{"Key": k, "Value": v})
	}
	return out
}

// currencyAmountPattern is the decimal-number shape FormatCurrency
// requires amount to match: an optional leading "-", one or more
// digits, and an optional "." followed by one or more digits. No
// exponent form, no thousands separators already present -- a plain
// decimal string, matching how a cloud billing API's own JSON typically
// represents an amount (as a string, specifically to avoid the binary
// floating-point rounding a JSON number would risk for money -- the
// same reason this function itself parses amount with math/big.Rat
// rather than strconv.ParseFloat).
var currencyAmountPattern = regexp.MustCompile(`^-?[0-9]+(\.[0-9]+)?$`)

// currencyInfo is one currencyTable entry: the symbol to show, how many
// fractional digits the currency uses, and whether the symbol is
// written after the amount (with a separating space) rather than
// immediately before it.
type currencyInfo struct {
	symbol   string
	decimals int
	suffix   bool
}

// currencyTable is a small, explicitly curated set of ISO 4217 currency
// codes, the same "documented starting point, not an authoritative
// mirror" convention NormalizeCloudRegion's own table below uses. A code
// not listed here still formats correctly: FormatCurrency falls back to
// the code itself as a suffix with two decimal digits, never refusing an
// unrecognized-but-well-formed currency code outright.
var currencyTable = map[string]currencyInfo{
	"USD": {"$", 2, false},
	"EUR": {"€", 2, false},
	"GBP": {"£", 2, false},
	"JPY": {"¥", 0, false},
	"CNY": {"¥", 2, false},
	"INR": {"₹", 2, false},
	"KRW": {"₩", 0, false},
	"AUD": {"$", 2, false},
	"CAD": {"$", 2, false},
	"CHF": {"CHF", 2, true},
	"KWD": {"KWD", 3, true},
	"BHD": {"BHD", 3, true},
}

// FormatCurrency formats amount (a plain decimal string, see
// currencyAmountPattern) as human-readable currency text under
// currencyCode's own symbol, decimal-digit count and symbol placement
// (see currencyTable), with the integer part grouped into thousands by
// commas: FormatCurrency("1234.5", "USD") is "$1,234.50". Rounding to
// the currency's decimal digit count is half-away-from-zero, computed
// with math/big.Rat rather than float64, so a value already at the
// currency's own precision is never perturbed by binary floating-point
// representation error. Returns "" if amount does not match
// currencyAmountPattern or either argument exceeds MaxInputBytes.
func FormatCurrency(amount, currencyCode string) string {
	if len(amount) > MaxInputBytes || len(currencyCode) > MaxInputBytes {
		return ""
	}
	if !currencyAmountPattern.MatchString(amount) {
		return ""
	}
	// Every string currencyAmountPattern accepts is also one
	// big.Rat.SetString parses successfully -- verified directly, not
	// assumed, since SetString's own documented grammar is a superset of
	// the pattern's -- so this branch is provably unreachable today.
	// Checked anyway per this package's own convention of never assuming
	// a documented failure return cannot happen: SetString's contract
	// covers input this function does not currently allow through (a
	// fraction "a/b", an exponent), so this guards against a future
	// loosening of currencyAmountPattern reaching SetString unchecked.
	rat, ok := new(big.Rat).SetString(amount)
	if !ok {
		return ""
	}

	code := strings.ToUpper(strings.TrimSpace(currencyCode))
	info, known := currencyTable[code]
	if !known {
		info = currencyInfo{symbol: code, decimals: 2, suffix: true}
	}

	negative := rat.Sign() < 0
	if negative {
		rat.Neg(rat)
	}
	minorUnits := roundToMinorUnits(rat, info.decimals)

	for len(minorUnits) <= info.decimals {
		minorUnits = "0" + minorUnits
	}
	intPart := minorUnits[:len(minorUnits)-info.decimals]
	fracPart := minorUnits[len(minorUnits)-info.decimals:]

	var b strings.Builder
	if negative {
		b.WriteByte('-')
	}
	if !info.suffix {
		b.WriteString(info.symbol)
	}
	b.WriteString(groupThousands(intPart))
	if info.decimals > 0 {
		b.WriteByte('.')
		b.WriteString(fracPart)
	}
	if info.suffix {
		b.WriteByte(' ')
		b.WriteString(info.symbol)
	}
	return b.String()
}

// roundToMinorUnits rounds a non-negative rat to decimals fractional
// digits, half-away-from-zero, and returns the result as a plain base-10
// digit string of its minor units (e.g. 1234.5 at 2 decimals -> "123450"
// meaning 1234.50).
func roundToMinorUnits(rat *big.Rat, decimals int) string {
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
	scaled := new(big.Rat).Mul(rat, new(big.Rat).SetInt(scale))
	num, den := scaled.Num(), scaled.Denom()
	q, r := new(big.Int).QuoRem(num, den, new(big.Int))
	if new(big.Int).Mul(r, big.NewInt(2)).Cmp(den) >= 0 {
		q.Add(q, big.NewInt(1))
	}
	return q.String()
}

// groupThousands inserts a comma every three digits from the right of a
// plain (sign-free) base-10 digit string.
func groupThousands(digits string) string {
	if len(digits) <= 3 {
		return digits
	}
	var b strings.Builder
	lead := len(digits) % 3
	if lead == 0 {
		lead = 3
	}
	b.WriteString(digits[:lead])
	for i := lead; i < len(digits); i += 3 {
		b.WriteByte(',')
		b.WriteString(digits[i : i+3])
	}
	return b.String()
}

// cloudInitDefaultContentType is CloudInitWrap's contentType when the
// caller supplies none: a shell script is the single most common
// cloud-init user-data payload.
const cloudInitDefaultContentType = "text/x-shellscript"

// CloudInitWrap wraps content as a single-part MIME message in the
// shape cloud-init's own "user-data mime multi part archive" format
// documents: an outer "Content-Type: multipart/mixed" envelope around
// one part carrying contentType (defaulting to
// cloudInitDefaultContentType when contentType is "") and a base64
// Content-Transfer-Encoding, RFC 2045-wrapped at 76 characters per line.
// This never inspects or validates content: a syntactically broken
// shell script or an unrelated arbitrary string wraps exactly as
// readily as a working one, since deciding whether a script is correct
// is not this function's job. Returns "" if content exceeds
// MaxStructuredInputBytes or contentType exceeds MaxInputBytes.
func CloudInitWrap(content, contentType string) string {
	if len(content) > MaxStructuredInputBytes || len(contentType) > MaxInputBytes {
		return ""
	}
	ct := strings.TrimSpace(contentType)
	if ct == "" {
		ct = cloudInitDefaultContentType
	}

	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	header := textproto.MIMEHeader{}
	header.Set("Content-Type", ct+`; charset="us-ascii"`)
	header.Set("MIME-Version", "1.0")
	header.Set("Content-Transfer-Encoding", "base64")
	// w.CreatePart's only failure mode -- verified directly against
	// mime/multipart's own source, not assumed -- is the underlying
	// Writer erroring (this is the first and only part ever created here,
	// so its other failure path, closing a previous part, never runs),
	// the same provably-unreachable reason part.Write/w.Close below are;
	// checked anyway for the same reason.
	part, err := w.CreatePart(header)
	if err != nil {
		return ""
	}
	encoded := base64.StdEncoding.EncodeToString([]byte(content))
	// part.Write's only failure mode is the underlying bytes.Buffer
	// erroring, which never happens (bytes.Buffer.Write always returns a
	// nil error), so these are provably unreachable; checked anyway per
	// this package's own convention of never assuming a documented
	// failure return cannot happen.
	for i := 0; i < len(encoded); i += 76 {
		end := min(i+76, len(encoded))
		if _, err := part.Write([]byte(encoded[i:end])); err != nil {
			return ""
		}
		if _, err := part.Write([]byte("\n")); err != nil {
			return ""
		}
	}
	boundary := w.Boundary()
	// w.Close's only failure mode is the underlying Writer erroring, for
	// the same provably-unreachable reason as part.Write above; checked
	// anyway for the same reason.
	if err := w.Close(); err != nil {
		return ""
	}

	return "Content-Type: multipart/mixed; boundary=\"" + boundary + "\"\nMIME-Version: 1.0\n\n" + body.String()
}

// paginationHeaderKeys are the header names ExtractPaginationToken looks
// for inside a response's "headers" sub-map, matched case-insensitively:
// Azure Table Storage's continuation header, and two generic
// conventions ("x-next-token", "x-amz-continuation-token") seen across
// hand-rolled and AWS pagination wrappers that surface the token as a
// response header rather than a body field.
var paginationHeaderKeys = map[string]bool{
	"x-ms-continuation":        true,
	"x-next-token":             true,
	"x-amz-continuation-token": true,
}

// ExtractPaginationToken looks for a continuation/pagination token in
// response, checking a small, explicitly curated set of shapes real
// cloud APIs use, in this priority order: a top-level "next_token"
// string (the generic snake_case convention several SDKs normalize to);
// a top-level "nextPageToken" string (GCP's own convention); a top-level
// "NextToken" string (AWS's own PascalCase convention); a top-level
// "@odata.nextLink" string (Azure/OData's convention, itself a full URL
// -- if it carries a "$skiptoken" or "$skip" query parameter that value
// is returned instead of the whole URL, since that is what a caller
// re-issuing the request actually needs); and finally a "headers"
// sub-map, checked case-insensitively against paginationHeaderKeys. This
// is not an attempt to cover every pagination convention in existence;
// it is scoped the same way NormalizeCloudRegion's and
// ResourceTShirtSize's own tables are. Returns "" when none of these
// shapes is present -- itself a legitimate, common answer ("this is the
// last page"), not a parse failure.
func ExtractPaginationToken(response map[string]any) string {
	for _, key := range []string{"next_token", "nextPageToken", "NextToken"} {
		if v, ok := response[key].(string); ok && v != "" {
			return v
		}
	}
	if v, ok := response["@odata.nextLink"].(string); ok && v != "" {
		if u, ok := parseURLWithScheme(v); ok {
			if tok := u.Query().Get("$skiptoken"); tok != "" {
				return tok
			}
			if tok := u.Query().Get("$skip"); tok != "" {
				return tok
			}
		}
		return v
	}
	if headers, ok := response["headers"].(map[string]any); ok {
		for k, v := range headers {
			if !paginationHeaderKeys[strings.ToLower(k)] {
				continue
			}
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

// resourceTShirtTiers is ResourceTShirtSize's default vCPU/RAM sizing
// table, one row per tier in ascending order: a resource is classified
// into the smallest tier whose vcpu and ramMB bounds it fits within.
// This is a documented starting point an operator is expected to
// override for their own fleet's actual instance catalog, not an
// attempt to model any cloud provider's real, much larger and
// constantly changing set of instance types.
var resourceTShirtTiers = []struct {
	label string
	vcpu  int
	ramMB int
}{
	{"XS", 1, 2048},
	{"S", 2, 4096},
	{"M", 4, 8192},
	{"L", 8, 16384},
	{"XL", 16, 32768},
	{"XXL", 32, 65536},
}

// ResourceTShirtSize classifies a resource's vcpu count and ramMB
// (memory, in megabytes) into one of resourceTShirtTiers' labels: the
// smallest tier whose own bounds cover both vcpu and ramMB. A resource
// whose vcpu fits a smaller tier than its ramMB does (or vice versa)
// is classified by whichever dimension needs the larger tier, so a
// high-memory/low-CPU (or the reverse) shape is not mischaracterized by
// looking at only one dimension. Returns "custom" for vcpu <= 0, ramMB
// <= 0, or a resource too large for every tier -- an honest "does not
// fit this table" answer rather than a guessed extrapolation.
func ResourceTShirtSize(vcpu, ramMB int) string {
	if vcpu <= 0 || ramMB <= 0 {
		return "custom"
	}
	for _, tier := range resourceTShirtTiers {
		if vcpu <= tier.vcpu && ramMB <= tier.ramMB {
			return tier.label
		}
	}
	return "custom"
}

// cloudRegionAliases is a small, explicitly curated lookup table mapping
// common region aliases, legacy names and casing variants to a canonical
// current provider region code. It needs periodic manual updates as
// providers add or rename regions -- it is not, and does not attempt to
// be, a live, always-current mirror of any provider's own region list.
// Lookup is case-insensitive against these keys (each already
// lowercase); a region not found here is returned unchanged (trimmed,
// original case preserved), since normalization only applies to a known
// alias -- an unrecognized value is not guessed at.
var cloudRegionAliases = map[string]string{
	"us-east":          "us-east-1",
	"useast1":          "us-east-1",
	"us-east-virginia": "us-east-1",
	"us-west":          "us-west-2",
	"uswest2":          "us-west-2",
	"eu-west":          "eu-west-1",
	"euwest1":          "eu-west-1",
	"eu-ireland":       "eu-west-1",
	"eu-central":       "eu-central-1",
	"eucentral1":       "eu-central-1",
	"eu-frankfurt":     "eu-central-1",
	"ap-southeast":     "ap-southeast-1",
	"ap-northeast":     "ap-northeast-1",
	"east-us":          "eastus",
	"westus":           "westus",
	"west-us":          "westus",
	"east-us-2":        "eastus2",
	"west-europe":      "westeurope",
	"north-europe":     "northeurope",
	"us-central":       "us-central1",
	"europe-west":      "europe-west1",
	"asia-east":        "asia-east1",
}

// NormalizeCloudRegion looks up region in cloudRegionAliases,
// case-insensitively and with surrounding whitespace trimmed, returning
// the canonical mapped value when found, or the trimmed (but otherwise
// unmodified) original when not. Returns "" if region exceeds
// MaxInputBytes.
func NormalizeCloudRegion(region string) string {
	if len(region) > MaxInputBytes {
		return ""
	}
	trimmed := strings.TrimSpace(region)
	if canonical, ok := cloudRegionAliases[strings.ToLower(trimmed)]; ok {
		return canonical
	}
	return trimmed
}

// iamPolicyDoc is the JSON shape IAMPolicyMerger reads and writes: an
// IAM policy document's Version and its list of Statement objects.
// Statement is decoded as json.RawMessage-free any so a single-object
// "Statement" (a policy with exactly one statement, valid IAM JSON that
// omits the array wrapper) and an array of objects are both accepted;
// iamStatementList normalizes either shape to a []any.
type iamPolicyDoc struct {
	Version   string `json:"Version,omitempty"`
	Statement any    `json:"Statement"`
}

// iamPolicyOut is IAMPolicyMerger's own output shape: a plain struct,
// rather than a map, so Version is always written before Statement
// regardless of Go map key ordering.
type iamPolicyOut struct {
	Version   string `json:"Version"`
	Statement []any  `json:"Statement"`
}

// defaultIAMPolicyVersion is the only IAM policy language version that
// has ever existed, used as IAMPolicyMerger's fallback when neither
// input document names one.
const defaultIAMPolicyVersion = "2012-10-17"

// IAMPolicyMerger merges two IAM policy documents' Statement lists into
// one, by concatenation and then structural deduplication: a statement
// from policyB that is an exact structural duplicate of one already
// taken from policyA (same keys and values once each is re-marshaled to
// canonical JSON, which encoding/json.Marshal already produces for a
// decoded map by sorting its keys) is dropped, keeping policyA's own
// copy and its position. This is a syntactic merge only -- it has no
// understanding of IAM action wildcards, resource ARN matching, or
// effect precedence, and never attempts to detect that two
// differently-written statements are semantically equivalent (an
// Allow on "s3:Get*" and one on "s3:GetObject" are never recognized as
// overlapping, for instance). Version in the output is policyA's own
// Version if non-empty, else policyB's, else defaultIAMPolicyVersion.
// Returns "" if either input exceeds MaxStructuredInputBytes, is not
// valid JSON, has no "Statement" key, has a "Statement" that is neither
// an object nor an array of objects, or nests past maxStructuredDepth.
func IAMPolicyMerger(policyA, policyB string) string {
	if len(policyA) > MaxStructuredInputBytes || len(policyB) > MaxStructuredInputBytes {
		return ""
	}
	docA, ok := decodeIAMPolicyDoc(policyA)
	if !ok {
		return ""
	}
	docB, ok := decodeIAMPolicyDoc(policyB)
	if !ok {
		return ""
	}
	stmtsA, ok := iamStatementList(docA.Statement)
	if !ok {
		return ""
	}
	stmtsB, ok := iamStatementList(docB.Statement)
	if !ok {
		return ""
	}

	version := docA.Version
	if version == "" {
		version = docB.Version
	}
	if version == "" {
		version = defaultIAMPolicyVersion
	}

	merged := make([]any, 0, len(stmtsA)+len(stmtsB))
	seen := make(map[string]bool, len(stmtsA)+len(stmtsB))
	for _, stmt := range append(stmtsA, stmtsB...) {
		// Every element of stmtsA/stmtsB was itself decoded from JSON by
		// decodeIAMPolicyDoc, so it is built entirely out of
		// map[string]any/[]any/string/float64/bool/nil -- exactly what
		// encoding/json.Marshal always succeeds on; the error return is
		// checked per this package's own convention, not because it can
		// actually trigger here.
		canon, err := json.Marshal(stmt)
		if err != nil {
			return ""
		}
		if seen[string(canon)] {
			continue
		}
		seen[string(canon)] = true
		merged = append(merged, stmt)
	}

	// merged holds only elements already proven json.Marshal-safe by the
	// loop above, and Version is a plain string, so this is provably
	// unreachable for the same reason that loop's own check is; checked
	// anyway for the same reason.
	out, err := json.Marshal(iamPolicyOut{Version: version, Statement: merged})
	if err != nil {
		return ""
	}
	return string(out)
}

// decodeIAMPolicyDoc parses raw as an iamPolicyDoc, refusing anything
// nested past maxStructuredDepth the same way YAMLToJSON/JSONToYAML do.
func decodeIAMPolicyDoc(raw string) (iamPolicyDoc, bool) {
	var doc iamPolicyDoc
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		return iamPolicyDoc{}, false
	}
	if !withinStructuredDepth(doc.Statement, 0) {
		return iamPolicyDoc{}, false
	}
	return doc, true
}

// iamStatementList normalizes an iamPolicyDoc.Statement value (decoded
// by encoding/json into either a map[string]any, for a policy with
// exactly one statement written without the array wrapper, or a []any
// of such maps) into a plain []any of statements. Reports false for
// anything else, including a missing Statement (Go's zero value for an
// any-typed struct field is nil, indistinguishable from "absent" here)
// or a list containing a non-object element.
func iamStatementList(stmt any) ([]any, bool) {
	switch t := stmt.(type) {
	case map[string]any:
		return []any{t}, true
	case []any:
		for _, e := range t {
			if _, ok := e.(map[string]any); !ok {
				return nil, false
			}
		}
		return t, true
	default:
		return nil, false
	}
}
