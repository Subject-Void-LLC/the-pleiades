package filters_test

import (
	"encoding/base64"
	"encoding/json"
	"mime"
	"mime/multipart"
	"reflect"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/filters"
)

func TestAWSTagListToMap(t *testing.T) {
	tags := []map[string]any{
		{"Key": "Name", "Value": "prod-web-1"},
		{"Key": "Environment", "Value": "production"},
	}
	want := map[string]any{"Name": "prod-web-1", "Environment": "production"}
	got := filters.AWSTagListToMap(tags)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("AWSTagListToMap(%#v) = %#v, want %#v", tags, got, want)
	}
}

func TestAWSTagListToMap_DuplicateKeyLastWins(t *testing.T) {
	tags := []map[string]any{
		{"Key": "Name", "Value": "first"},
		{"Key": "Name", "Value": "second"},
	}
	got := filters.AWSTagListToMap(tags)
	if got["Name"] != "second" {
		t.Errorf("AWSTagListToMap duplicate key: got %v, want \"second\"", got["Name"])
	}
}

func TestAWSTagListToMap_Empty(t *testing.T) {
	got := filters.AWSTagListToMap(nil)
	if got == nil || len(got) != 0 {
		t.Errorf("AWSTagListToMap(nil) = %#v, want empty non-nil map", got)
	}
}

func TestAWSTagListToMap_Malformed(t *testing.T) {
	cases := []struct {
		name string
		tags []map[string]any
	}{
		{"missing_key", []map[string]any{{"Value": "x"}}},
		{"missing_value", []map[string]any{{"Key": "x"}}},
		{"non_string_key", []map[string]any{{"Key": 1, "Value": "x"}}},
		{"non_string_value", []map[string]any{{"Key": "x", "Value": 1}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.AWSTagListToMap(tc.tags); got != nil {
				t.Errorf("AWSTagListToMap(%#v) = %#v, want nil", tc.tags, got)
			}
		})
	}
}

func TestMapToAWSTagList(t *testing.T) {
	m := map[string]any{"Name": "prod-web-1", "Environment": "production"}
	want := []map[string]any{
		{"Key": "Environment", "Value": "production"},
		{"Key": "Name", "Value": "prod-web-1"},
	}
	got := filters.MapToAWSTagList(m)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("MapToAWSTagList(%#v) = %#v, want %#v (sorted by key)", m, got, want)
	}
}

func TestMapToAWSTagList_Malformed(t *testing.T) {
	if got := filters.MapToAWSTagList(map[string]any{"Name": 1}); got != nil {
		t.Errorf("MapToAWSTagList with non-string value = %#v, want nil", got)
	}
}

func TestAWSTagListToMap_MapToAWSTagList_RoundTrip(t *testing.T) {
	m := map[string]any{"Name": "prod-web-1", "Environment": "production", "Owner": "platform-team"}
	list := filters.MapToAWSTagList(m)
	got := filters.AWSTagListToMap(list)
	if !reflect.DeepEqual(got, m) {
		t.Errorf("round trip: AWSTagListToMap(MapToAWSTagList(%#v)) = %#v, want %#v", m, got, m)
	}
}

func TestFormatCurrency(t *testing.T) {
	cases := []struct {
		name     string
		amount   string
		currency string
		want     string
	}{
		{"usd_basic", "1234.5", "USD", "$1,234.50"},
		{"usd_no_decimal_input", "5", "USD", "$5.00"},
		{"usd_negative", "-42.1", "USD", "-$42.10"},
		{"jpy_zero_decimals", "1500", "JPY", "¥1,500"},
		{"jpy_rounds_fraction", "1500.6", "JPY", "¥1,501"},
		{"kwd_three_decimals_suffix", "12.5", "KWD", "12.500 KWD"},
		{"unknown_code_falls_back", "9.995", "XYZ", "10.00 XYZ"},
		{"large_amount_grouped", "1234567.89", "USD", "$1,234,567.89"},
		{"exact_multiple_of_three_digits_grouped", "123456.78", "USD", "$123,456.78"},
		{"lowercase_code_normalized", "1", "usd", "$1.00"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.FormatCurrency(tc.amount, tc.currency); got != tc.want {
				t.Errorf("FormatCurrency(%q, %q) = %q, want %q", tc.amount, tc.currency, got, tc.want)
			}
		})
	}
}

func TestFormatCurrency_Malformed(t *testing.T) {
	cases := []struct {
		name   string
		amount string
	}{
		{"empty", ""},
		{"not_a_number", "abc"},
		{"exponent_form_rejected", "1e10"},
		{"trailing_dot", "12."},
		{"comma_separated_rejected", "1,234"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.FormatCurrency(tc.amount, "USD"); got != "" {
				t.Errorf("FormatCurrency(%q, \"USD\") = %q, want \"\"", tc.amount, got)
			}
		})
	}
}

func TestFormatCurrency_OverCap(t *testing.T) {
	if got := filters.FormatCurrency(strings.Repeat("1", filters.MaxInputBytes+1), "USD"); got != "" {
		t.Errorf("FormatCurrency over cap = %q, want \"\"", got)
	}
}

func TestCloudInitWrap(t *testing.T) {
	script := "#!/bin/bash\necho hello world\n"
	got := filters.CloudInitWrap(script, "")

	mediaType, params, err := mime.ParseMediaType(strings.SplitN(strings.SplitN(got, "\n", 2)[0], ": ", 2)[1])
	if err != nil {
		t.Fatalf("parsing outer Content-Type header: %v", err)
	}
	if mediaType != "multipart/mixed" {
		t.Fatalf("outer media type = %q, want multipart/mixed", mediaType)
	}
	boundary := params["boundary"]
	if boundary == "" {
		t.Fatal("outer Content-Type carries no boundary parameter")
	}

	bodyStart := strings.Index(got, "\n\n")
	if bodyStart < 0 {
		t.Fatal("no blank line separating outer header from multipart body")
	}
	mr := multipart.NewReader(strings.NewReader(got[bodyStart+2:]), boundary)
	part, err := mr.NextPart()
	if err != nil {
		t.Fatalf("reading first MIME part: %v", err)
	}
	if ct := part.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/x-shellscript") {
		t.Errorf("part Content-Type = %q, want it to start with text/x-shellscript", ct)
	}
	if part.Header.Get("Content-Transfer-Encoding") != "base64" {
		t.Errorf("part Content-Transfer-Encoding = %q, want base64", part.Header.Get("Content-Transfer-Encoding"))
	}
	raw := make([]byte, 0, 256)
	buf := make([]byte, 256)
	for {
		n, err := part.Read(buf)
		raw = append(raw, buf[:n]...)
		if err != nil {
			break
		}
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(string(raw), "\n", ""))
	if err != nil {
		t.Fatalf("decoding part body as base64: %v", err)
	}
	if string(decoded) != script {
		t.Errorf("decoded part body = %q, want %q", decoded, script)
	}
}

func TestCloudInitWrap_CustomContentType(t *testing.T) {
	got := filters.CloudInitWrap("packages:\n  - nginx\n", "text/cloud-config")
	if !strings.Contains(got, "text/cloud-config") {
		t.Errorf("CloudInitWrap with explicit contentType: got %q, want it to contain text/cloud-config", got)
	}
}

func TestCloudInitWrap_DoesNotValidateContent(t *testing.T) {
	got := filters.CloudInitWrap("this is not a valid script at all {{{", "")
	if got == "" {
		t.Error("CloudInitWrap rejected syntactically invalid script content, want it wrapped unconditionally")
	}
}

func TestCloudInitWrap_OverCap(t *testing.T) {
	if got := filters.CloudInitWrap(strings.Repeat("a", filters.MaxStructuredInputBytes+1), ""); got != "" {
		t.Errorf("CloudInitWrap over cap = %q, want \"\"", got)
	}
	if got := filters.CloudInitWrap("echo hi", strings.Repeat("a", filters.MaxInputBytes+1)); got != "" {
		t.Errorf("CloudInitWrap with over-cap contentType = %q, want \"\"", got)
	}
}

func TestExtractPaginationToken(t *testing.T) {
	cases := []struct {
		name     string
		response map[string]any
		want     string
	}{
		{"next_token", map[string]any{"next_token": "tok-1", "items": []any{}}, "tok-1"},
		{"next_page_token", map[string]any{"nextPageToken": "tok-2"}, "tok-2"},
		{"NextToken", map[string]any{"NextToken": "tok-3"}, "tok-3"},
		{"odata_next_link_with_skiptoken", map[string]any{"@odata.nextLink": "https://graph.example.com/v1.0/users?$skiptoken=abc123"}, "abc123"},
		{"odata_next_link_with_skip_only", map[string]any{"@odata.nextLink": "https://graph.example.com/v1.0/users?$skip=10"}, "10"},
		{"odata_next_link_no_query", map[string]any{"@odata.nextLink": "https://graph.example.com/v1.0/users/page2"}, "https://graph.example.com/v1.0/users/page2"},
		{"header_azure_continuation", map[string]any{"headers": map[string]any{"x-ms-continuation": "tok-4"}}, "tok-4"},
		{"header_case_insensitive", map[string]any{"headers": map[string]any{"X-Next-Token": "tok-5"}}, "tok-5"},
		{"header_skips_unknown_key_before_known_one", map[string]any{"headers": map[string]any{"content-type": "application/json", "x-ms-continuation": "tok-6"}}, "tok-6"},
		{"no_known_shape", map[string]any{"items": []any{1, 2, 3}}, ""},
		{"empty_response", map[string]any{}, ""},
		{"priority_next_token_over_others", map[string]any{"next_token": "tok-a", "NextToken": "tok-b"}, "tok-a"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.ExtractPaginationToken(tc.response); got != tc.want {
				t.Errorf("ExtractPaginationToken(%#v) = %q, want %q", tc.response, got, tc.want)
			}
		})
	}
}

func TestResourceTShirtSize(t *testing.T) {
	cases := []struct {
		name  string
		vcpu  int
		ramMB int
		want  string
	}{
		{"xs", 1, 1024, "XS"},
		{"s", 2, 4096, "S"},
		{"m", 4, 8192, "M"},
		{"l", 8, 16384, "L"},
		{"xl", 16, 32768, "XL"},
		{"xxl", 32, 65536, "XXL"},
		{"too_large", 64, 131072, "custom"},
		{"high_ram_low_cpu_takes_larger_tier", 1, 32768, "XL"},
		{"high_cpu_low_ram", 16, 1024, "XL"},
		{"zero_vcpu", 0, 4096, "custom"},
		{"negative_ram", 2, -1, "custom"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.ResourceTShirtSize(tc.vcpu, tc.ramMB); got != tc.want {
				t.Errorf("ResourceTShirtSize(%d, %d) = %q, want %q", tc.vcpu, tc.ramMB, got, tc.want)
			}
		})
	}
}

func TestNormalizeCloudRegion(t *testing.T) {
	cases := []struct {
		name   string
		region string
		want   string
	}{
		{"known_alias", "us-east", "us-east-1"},
		{"case_insensitive", "US-EAST", "us-east-1"},
		{"whitespace_trimmed", "  us-east  ", "us-east-1"},
		{"azure_alias", "east-us", "eastus"},
		{"already_canonical_passthrough", "us-central1", "us-central1"},
		{"unknown_passthrough_unchanged", "Mars-Colony-1", "Mars-Colony-1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.NormalizeCloudRegion(tc.region); got != tc.want {
				t.Errorf("NormalizeCloudRegion(%q) = %q, want %q", tc.region, got, tc.want)
			}
		})
	}
}

func TestNormalizeCloudRegion_OverCap(t *testing.T) {
	if got := filters.NormalizeCloudRegion(strings.Repeat("a", filters.MaxInputBytes+1)); got != "" {
		t.Errorf("NormalizeCloudRegion over cap = %q, want \"\"", got)
	}
}

const iamPolicyA = `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"s3:GetObject","Resource":"*"}]}`
const iamPolicyB = `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"s3:GetObject","Resource":"*"},{"Effect":"Deny","Action":"s3:DeleteObject","Resource":"*"}]}`

func TestIAMPolicyMerger(t *testing.T) {
	got := filters.IAMPolicyMerger(iamPolicyA, iamPolicyB)
	if got == "" {
		t.Fatal("IAMPolicyMerger returned \"\", want a merged policy")
	}

	var doc struct {
		Version   string
		Statement []map[string]any
	}
	if err := json.Unmarshal([]byte(got), &doc); err != nil {
		t.Fatalf("merged output is not valid JSON: %v", err)
	}
	if doc.Version != "2012-10-17" {
		t.Errorf("Version = %q, want 2012-10-17", doc.Version)
	}
	// policyA and policyB share one exact-duplicate statement; the merge
	// must dedupe it, leaving the shared Allow plus the one Deny unique
	// to policyB: two statements total, not three.
	if len(doc.Statement) != 2 {
		t.Errorf("Statement has %d entries, want 2 (duplicate deduped): %#v", len(doc.Statement), doc.Statement)
	}
}

func TestIAMPolicyMerger_SingleObjectStatementAccepted(t *testing.T) {
	a := `{"Version":"2012-10-17","Statement":{"Effect":"Allow","Action":"ec2:Describe*","Resource":"*"}}`
	b := `{"Version":"2012-10-17","Statement":{"Effect":"Deny","Action":"ec2:Terminate*","Resource":"*"}}`
	got := filters.IAMPolicyMerger(a, b)
	var doc struct{ Statement []map[string]any }
	if err := json.Unmarshal([]byte(got), &doc); err != nil {
		t.Fatalf("merged output is not valid JSON: %v", err)
	}
	if len(doc.Statement) != 2 {
		t.Errorf("Statement has %d entries, want 2: %#v", len(doc.Statement), doc.Statement)
	}
}

func TestIAMPolicyMerger_VersionFallback(t *testing.T) {
	a := `{"Statement":[{"Effect":"Allow","Action":"s3:GetObject","Resource":"*"}]}`
	b := `{"Statement":[{"Effect":"Deny","Action":"s3:DeleteObject","Resource":"*"}]}`
	got := filters.IAMPolicyMerger(a, b)
	var doc struct{ Version string }
	if err := json.Unmarshal([]byte(got), &doc); err != nil {
		t.Fatalf("merged output is not valid JSON: %v", err)
	}
	if doc.Version != "2012-10-17" {
		t.Errorf("Version fallback = %q, want 2012-10-17", doc.Version)
	}
}

func TestIAMPolicyMerger_Malformed(t *testing.T) {
	cases := []struct {
		name string
		a, b string
	}{
		{"invalid_json_a", "not json", iamPolicyA},
		{"invalid_json_b", iamPolicyA, "not json"},
		{"missing_statement", `{"Version":"2012-10-17"}`, iamPolicyA},
		{"statement_not_object_or_array", `{"Statement":"oops"}`, iamPolicyA},
		{"statement_array_with_non_object", `{"Statement":["oops"]}`, iamPolicyA},
		{"policy_b_statement_malformed", iamPolicyA, `{"Statement":"oops"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.IAMPolicyMerger(tc.a, tc.b); got != "" {
				t.Errorf("IAMPolicyMerger(%q, %q) = %q, want \"\"", tc.a, tc.b, got)
			}
		})
	}
}

func TestIAMPolicyMerger_DepthCap(t *testing.T) {
	deep := `{"Statement":` + strings.Repeat(`{"a":`, 50) + "1" + strings.Repeat("}", 50) + `}`
	if got := filters.IAMPolicyMerger(deep, iamPolicyA); got != "" {
		t.Errorf("IAMPolicyMerger(50-deep Statement, _) = %q, want \"\" (past maxStructuredDepth)", got)
	}
}

func TestIAMPolicyMerger_OverCap(t *testing.T) {
	huge := `{"Statement":[{"a":"` + strings.Repeat("x", filters.MaxStructuredInputBytes+1) + `"}]}`
	if got := filters.IAMPolicyMerger(huge, iamPolicyA); got != "" {
		t.Errorf("IAMPolicyMerger over cap = %q, want \"\"", got)
	}
}
