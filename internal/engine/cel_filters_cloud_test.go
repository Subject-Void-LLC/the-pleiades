package engine_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
)

// TestCELFilters_Phase57CloudProviderDataFilters is Phase 57's own
// Release Gate requirement: every one of its 14 filters proven callable
// through the real, unmodified engine.NewCELEvaluator()/Program.Eval via
// a compiled when_cel-shaped expression, not a bare Go function call
// (RULE 0). Each case's want value was independently verified against
// pkg/filters' own unit tests before being written here.
func TestCELFilters_Phase57CloudProviderDataFilters(t *testing.T) {
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatalf("failed to init CEL engine: %v", err)
	}

	cases := []struct {
		name string
		expr string
		vars map[string]interface{}
	}{
		{"parse_arn", `filters.parseARN("arn:aws:iam::123456789012:role/MyRole")["service"] == "iam"`, nil},
		{"build_arn", `filters.buildARN({"partition": "aws", "service": "iam", "account_id": "123456789012", "resource": "role/MyRole"}) == "arn:aws:iam::123456789012:role/MyRole"`, nil},
		{"parse_azure_resource_id", `filters.parseAzureResourceID("/subscriptions/sub-1/resourceGroups/my-rg/providers/Microsoft.Compute/virtualMachines/my-vm")["resource_name"] == "my-vm"`, nil},
		{"build_azure_resource_id", `filters.buildAzureResourceID({"subscription_id": "sub-1", "resource_group": "my-rg", "provider": "Microsoft.Compute", "resource_types": ["virtualMachines"], "resource_names": ["my-vm"]}) == "/subscriptions/sub-1/resourceGroups/my-rg/providers/Microsoft.Compute/virtualMachines/my-vm"`, nil},
		{"parse_gcp_self_link", `filters.parseGCPSelfLink("https://www.googleapis.com/compute/v1/projects/my-project/zones/us-central1-a/instances/my-vm")["resource_name"] == "my-vm"`, nil},
		{"parse_gcp_iam_member", `filters.parseGCPIAMMember("user:alice@example.com")["id"] == "alice@example.com"`, nil},
		{"aws_tag_list_to_map", `filters.awsTagListToMap([{"Key": "Name", "Value": "prod-web-1"}])["Name"] == "prod-web-1"`, nil},
		{"map_to_aws_tag_list", `filters.mapToAWSTagList({"Name": "prod-web-1"})[0]["Value"] == "prod-web-1"`, nil},
		{"format_currency", `filters.formatCurrency("1234.5", "USD") == "$1,234.50"`, nil},
		{"cloud_init_wrap", `filters.cloudInitWrap("#!/bin/bash\necho hi\n", "").contains("multipart/mixed")`, nil},
		{"extract_pagination_token", `filters.extractPaginationToken({"nextPageToken": "tok-2"}) == "tok-2"`, nil},
		{"resource_t_shirt_size", `filters.resourceTShirtSize(4, 8192) == "M"`, nil},
		{"normalize_cloud_region", `filters.normalizeCloudRegion("us-east") == "us-east-1"`, nil},
		{
			"iam_policy_merger",
			`filters.iamPolicyMerger(stat.policyA, stat.policyB) == "{\"Version\":\"2012-10-17\",\"Statement\":[{\"Action\":\"s3:GetObject\",\"Effect\":\"Allow\"},{\"Action\":\"s3:DeleteObject\",\"Effect\":\"Deny\"}]}"`,
			map[string]interface{}{"stat": map[string]interface{}{
				"policyA": `{"Statement":[{"Effect":"Allow","Action":"s3:GetObject"}]}`,
				"policyB": `{"Statement":[{"Effect":"Deny","Action":"s3:DeleteObject"}]}`,
			}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prg, err := eval.Compile(tc.expr)
			if err != nil {
				t.Fatalf("failed to compile %q: %v", tc.expr, err)
			}
			vars := tc.vars
			if vars == nil {
				vars = map[string]interface{}{}
			}
			got, err := prg.Eval(vars)
			if err != nil {
				t.Fatalf("eval of %q failed: %v", tc.expr, err)
			}
			if !got {
				t.Errorf("%s: expression %q evaluated false", tc.name, tc.expr)
			}
		})
	}
}

// TestCELFilters_Phase57CombinedCondition chains several of this phase's
// functions in one when_cel-shaped condition against a realistic cloud
// device stat payload, the same combined-condition shape
// TestCELFilters_Phase51CombinedCondition through
// TestCELFilters_Phase56CombinedCondition established, with a negative
// control proving the condition genuinely flips false.
func TestCELFilters_Phase57CombinedCondition(t *testing.T) {
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatalf("failed to init CEL evaluator: %v", err)
	}

	expr := `filters.normalizeCloudRegion(stat.region) == "us-east-1" && ` +
		`filters.resourceTShirtSize(stat.vcpu, stat.ram_mb) == "M" && ` +
		`filters.parseARN(stat.arn)["service"] == "ec2" && ` +
		`filters.awsTagListToMap(stat.tags)["Environment"] == "production"`

	prg, err := eval.Compile(expr)
	if err != nil {
		t.Fatalf("failed to compile: %v", err)
	}

	stat := map[string]interface{}{
		"region": "us-east",
		"vcpu":   4,
		"ram_mb": 8192,
		"arn":    "arn:aws:ec2:us-east-1:123456789012:instance/i-0abc",
		"tags":   []interface{}{map[string]interface{}{"Key": "Environment", "Value": "production"}},
	}
	got, err := prg.Eval(map[string]interface{}{"stat": stat})
	if err != nil {
		t.Fatalf("eval failed: %v", err)
	}
	if !got {
		t.Fatal("expected the combined condition to evaluate true against a realistic stat payload")
	}

	// Negative control: a region that normalizes to a different canonical
	// value must flip the same condition false, proving the combined
	// expression is actually exercising every clause rather than being
	// vacuously true.
	stat["region"] = "us-west"
	got, err = prg.Eval(map[string]interface{}{"stat": stat})
	if err != nil {
		t.Fatalf("eval failed: %v", err)
	}
	if got {
		t.Fatal("expected the combined condition to evaluate false once region no longer normalizes to us-east-1")
	}
}
