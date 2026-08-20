package filters_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/filters"
)

func TestParseARN(t *testing.T) {
	cases := []struct {
		name string
		arn  string
		want map[string]any
	}{
		{
			"iam_role_slash_resource",
			"arn:aws:iam::123456789012:role/MyRole",
			map[string]any{
				"partition": "aws", "service": "iam", "region": "", "account_id": "123456789012",
				"resource": "role/MyRole", "resource_type": "role", "resource_id": "MyRole", "resource_delimiter": "/",
			},
		},
		{
			"rds_colon_resource",
			"arn:aws:rds:us-east-1:123456789012:db:mydatabase",
			map[string]any{
				"partition": "aws", "service": "rds", "region": "us-east-1", "account_id": "123456789012",
				"resource": "db:mydatabase", "resource_type": "db", "resource_id": "mydatabase", "resource_delimiter": ":",
			},
		},
		{
			"s3_bucket_only",
			"arn:aws:s3:::my-bucket",
			map[string]any{
				"partition": "aws", "service": "s3", "region": "", "account_id": "",
				"resource": "my-bucket", "resource_type": "", "resource_id": "my-bucket", "resource_delimiter": "",
			},
		},
		{
			"gov_partition",
			"arn:aws-us-gov:ec2:us-gov-west-1:123456789012:instance/i-0abc",
			map[string]any{
				"partition": "aws-us-gov", "service": "ec2", "region": "us-gov-west-1", "account_id": "123456789012",
				"resource": "instance/i-0abc", "resource_type": "instance", "resource_id": "i-0abc", "resource_delimiter": "/",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := filters.ParseARN(tc.arn)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("ParseARN(%q) = %#v, want %#v", tc.arn, got, tc.want)
			}
		})
	}
}

func TestParseARN_Malformed(t *testing.T) {
	cases := []struct {
		name string
		arn  string
	}{
		{"no_arn_prefix", "aws:iam::123456789012:role/MyRole"},
		{"too_few_fields", "arn:aws:iam::123456789012"},
		{"empty", ""},
		{"over_cap", strings.Repeat("a", filters.MaxInputBytes+1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.ParseARN(tc.arn); got != nil {
				t.Errorf("ParseARN(%q) = %#v, want nil", tc.arn, got)
			}
		})
	}
}

func TestBuildARN(t *testing.T) {
	cases := []struct {
		name  string
		parts map[string]any
		want  string
	}{
		{
			"from_raw_resource",
			map[string]any{"partition": "aws", "service": "iam", "region": "", "account_id": "123456789012", "resource": "role/MyRole"},
			"arn:aws:iam::123456789012:role/MyRole",
		},
		{
			"from_type_and_id_default_slash",
			map[string]any{"partition": "aws", "service": "ec2", "region": "us-east-1", "account_id": "123456789012", "resource_type": "instance", "resource_id": "i-0abc"},
			"arn:aws:ec2:us-east-1:123456789012:instance/i-0abc",
		},
		{
			"from_type_and_id_explicit_colon_delimiter",
			map[string]any{"partition": "aws", "service": "rds", "region": "us-east-1", "account_id": "123456789012", "resource_type": "db", "resource_id": "mydatabase", "resource_delimiter": ":"},
			"arn:aws:rds:us-east-1:123456789012:db:mydatabase",
		},
		{
			"from_type_and_id_empty_delimiter_falls_back_to_slash",
			map[string]any{"partition": "aws", "service": "ec2", "region": "us-east-1", "account_id": "123456789012", "resource_type": "instance", "resource_id": "i-0abc", "resource_delimiter": ""},
			"arn:aws:ec2:us-east-1:123456789012:instance/i-0abc",
		},
		{
			"from_id_only_no_type",
			map[string]any{"partition": "aws", "service": "s3", "region": "", "account_id": "", "resource_id": "my-bucket"},
			"arn:aws:s3:::my-bucket",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.BuildARN(tc.parts); got != tc.want {
				t.Errorf("BuildARN(%#v) = %q, want %q", tc.parts, got, tc.want)
			}
		})
	}
}

func TestBuildARN_Malformed(t *testing.T) {
	if got := filters.BuildARN(map[string]any{"partition": "aws", "service": "s3"}); got != "" {
		t.Errorf("BuildARN with no resource = %q, want \"\"", got)
	}
}

func TestParseARN_BuildARN_RoundTrip(t *testing.T) {
	inputs := []string{
		"arn:aws:iam::123456789012:role/MyRole",
		"arn:aws:rds:us-east-1:123456789012:db:mydatabase",
		"arn:aws:s3:::my-bucket",
		"arn:aws-us-gov:ec2:us-gov-west-1:123456789012:instance/i-0abc",
		"arn:aws:sns:us-east-1:123456789012:MyTopic",
	}
	for _, in := range inputs {
		parsed := filters.ParseARN(in)
		if parsed == nil {
			t.Fatalf("ParseARN(%q) = nil, want a real result", in)
		}
		got := filters.BuildARN(parsed)
		if got != in {
			t.Errorf("round trip: BuildARN(ParseARN(%q)) = %q, want %q", in, got, in)
		}
	}
}

func TestParseAzureResourceID(t *testing.T) {
	id := "/subscriptions/sub-1/resourceGroups/my-rg/providers/Microsoft.Compute/virtualMachines/my-vm"
	got := filters.ParseAzureResourceID(id)
	want := map[string]any{
		"subscription_id": "sub-1",
		"resource_group":  "my-rg",
		"provider":        "Microsoft.Compute",
		"resource_types":  []any{"virtualMachines"},
		"resource_names":  []any{"my-vm"},
		"resource_type":   "virtualMachines",
		"resource_name":   "my-vm",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParseAzureResourceID(%q) = %#v, want %#v", id, got, want)
	}
}

func TestParseAzureResourceID_NestedChild(t *testing.T) {
	id := "/subscriptions/sub-1/resourceGroups/my-rg/providers/Microsoft.Network/virtualNetworks/my-vnet/subnets/my-subnet"
	got := filters.ParseAzureResourceID(id)
	want := map[string]any{
		"subscription_id": "sub-1",
		"resource_group":  "my-rg",
		"provider":        "Microsoft.Network",
		"resource_types":  []any{"virtualNetworks", "subnets"},
		"resource_names":  []any{"my-vnet", "my-subnet"},
		"resource_type":   "subnets",
		"resource_name":   "my-subnet",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParseAzureResourceID(%q) = %#v, want %#v", id, got, want)
	}
}

func TestParseAzureResourceID_Malformed(t *testing.T) {
	cases := []struct {
		name string
		id   string
	}{
		{"not_a_subscription_path", "/foo/bar"},
		{"missing_resource_group_marker", "/subscriptions/sub-1/groups/my-rg/providers/Microsoft.Compute/virtualMachines/my-vm"},
		{"odd_trailing_segment_count", "/subscriptions/sub-1/resourceGroups/my-rg/providers/Microsoft.Compute/virtualMachines"},
		{"empty", ""},
		{"over_cap", strings.Repeat("a", filters.MaxInputBytes+1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.ParseAzureResourceID(tc.id); got != nil {
				t.Errorf("ParseAzureResourceID(%q) = %#v, want nil", tc.id, got)
			}
		})
	}
}

func TestBuildAzureResourceID(t *testing.T) {
	parts := map[string]any{
		"subscription_id": "sub-1",
		"resource_group":  "my-rg",
		"provider":        "Microsoft.Compute",
		"resource_types":  []any{"virtualMachines"},
		"resource_names":  []any{"my-vm"},
	}
	want := "/subscriptions/sub-1/resourceGroups/my-rg/providers/Microsoft.Compute/virtualMachines/my-vm"
	if got := filters.BuildAzureResourceID(parts); got != want {
		t.Errorf("BuildAzureResourceID(%#v) = %q, want %q", parts, got, want)
	}
}

func TestBuildAzureResourceID_StringSliceAccepted(t *testing.T) {
	parts := map[string]any{
		"subscription_id": "sub-1",
		"resource_group":  "my-rg",
		"provider":        "Microsoft.Compute",
		"resource_types":  []string{"virtualMachines"},
		"resource_names":  []string{"my-vm"},
	}
	want := "/subscriptions/sub-1/resourceGroups/my-rg/providers/Microsoft.Compute/virtualMachines/my-vm"
	if got := filters.BuildAzureResourceID(parts); got != want {
		t.Errorf("BuildAzureResourceID(%#v) = %q, want %q", parts, got, want)
	}
}

func TestBuildAzureResourceID_Malformed(t *testing.T) {
	cases := []struct {
		name  string
		parts map[string]any
	}{
		{"missing_subscription", map[string]any{"resource_group": "my-rg", "provider": "Microsoft.Compute", "resource_types": []any{"virtualMachines"}, "resource_names": []any{"my-vm"}}},
		{"mismatched_lengths", map[string]any{"subscription_id": "sub-1", "resource_group": "my-rg", "provider": "Microsoft.Compute", "resource_types": []any{"virtualMachines", "extensions"}, "resource_names": []any{"my-vm"}}},
		{"empty_types", map[string]any{"subscription_id": "sub-1", "resource_group": "my-rg", "provider": "Microsoft.Compute", "resource_types": []any{}, "resource_names": []any{}}},
		{"non_string_element", map[string]any{"subscription_id": "sub-1", "resource_group": "my-rg", "provider": "Microsoft.Compute", "resource_types": []any{123}, "resource_names": []any{"my-vm"}}},
		{"resource_types_wrong_shape", map[string]any{"subscription_id": "sub-1", "resource_group": "my-rg", "provider": "Microsoft.Compute", "resource_types": "not-a-list", "resource_names": []any{"my-vm"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.BuildAzureResourceID(tc.parts); got != "" {
				t.Errorf("BuildAzureResourceID(%#v) = %q, want \"\"", tc.parts, got)
			}
		})
	}
}

func TestParseAzureResourceID_BuildAzureResourceID_RoundTrip(t *testing.T) {
	inputs := []string{
		"/subscriptions/sub-1/resourceGroups/my-rg/providers/Microsoft.Compute/virtualMachines/my-vm",
		"/subscriptions/sub-1/resourceGroups/my-rg/providers/Microsoft.Network/virtualNetworks/my-vnet/subnets/my-subnet",
		"/subscriptions/11111111-1111-1111-1111-111111111111/resourceGroups/prod-rg/providers/Microsoft.Storage/storageAccounts/mystorageacct",
	}
	for _, in := range inputs {
		parsed := filters.ParseAzureResourceID(in)
		if parsed == nil {
			t.Fatalf("ParseAzureResourceID(%q) = nil, want a real result", in)
		}
		got := filters.BuildAzureResourceID(parsed)
		if got != in {
			t.Errorf("round trip: BuildAzureResourceID(ParseAzureResourceID(%q)) = %q, want %q", in, got, in)
		}
	}
}

func TestParseGCPSelfLink(t *testing.T) {
	cases := []struct {
		name string
		link string
		want map[string]any
	}{
		{
			"zonal_instance",
			"https://www.googleapis.com/compute/v1/projects/my-project/zones/us-central1-a/instances/my-vm",
			map[string]any{"project": "my-project", "scope": "zone", "location": "us-central1-a", "resource_type": "instances", "resource_name": "my-vm", "api": "compute/v1"},
		},
		{
			"regional_subnetwork",
			"https://compute.googleapis.com/compute/v1/projects/my-project/regions/us-central1/subnetworks/my-subnet",
			map[string]any{"project": "my-project", "scope": "region", "location": "us-central1", "resource_type": "subnetworks", "resource_name": "my-subnet", "api": "compute/v1"},
		},
		{
			"global_network",
			"https://www.googleapis.com/compute/v1/projects/my-project/global/networks/my-network",
			map[string]any{"project": "my-project", "scope": "global", "location": "", "resource_type": "networks", "resource_name": "my-network", "api": "compute/v1"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := filters.ParseGCPSelfLink(tc.link)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("ParseGCPSelfLink(%q) = %#v, want %#v", tc.link, got, tc.want)
			}
		})
	}
}

func TestParseGCPSelfLink_Malformed(t *testing.T) {
	cases := []struct {
		name string
		link string
	}{
		{"no_scheme", "www.googleapis.com/compute/v1/projects/my-project/zones/us-central1-a/instances/my-vm"},
		{"no_projects_segment", "https://www.googleapis.com/compute/v1/zones/us-central1-a/instances/my-vm"},
		{"no_project_name", "https://www.googleapis.com/compute/v1/projects"},
		{"zone_missing_resource", "https://www.googleapis.com/compute/v1/projects/my-project/zones/us-central1-a"},
		{"global_missing_resource_name", "https://www.googleapis.com/compute/v1/projects/my-project/global/networks"},
		{"unrecognized_scope", "https://www.googleapis.com/compute/v1/projects/my-project/unknown/foo/instances/my-vm"},
		{"empty", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.ParseGCPSelfLink(tc.link); got != nil {
				t.Errorf("ParseGCPSelfLink(%q) = %#v, want nil", tc.link, got)
			}
		})
	}
}

func TestParseGCPIAMMember(t *testing.T) {
	cases := []struct {
		name   string
		member string
		want   map[string]any
	}{
		{"user", "user:alice@example.com", map[string]any{"type": "user", "id": "alice@example.com", "deleted": false, "uid": ""}},
		{"service_account", "serviceAccount:svc@my-project.iam.gserviceaccount.com", map[string]any{"type": "serviceAccount", "id": "svc@my-project.iam.gserviceaccount.com", "deleted": false, "uid": ""}},
		{"group", "group:admins@example.com", map[string]any{"type": "group", "id": "admins@example.com", "deleted": false, "uid": ""}},
		{"domain", "domain:example.com", map[string]any{"type": "domain", "id": "example.com", "deleted": false, "uid": ""}},
		{"all_users", "allUsers", map[string]any{"type": "allUsers", "id": "", "deleted": false, "uid": ""}},
		{"all_authenticated_users", "allAuthenticatedUsers", map[string]any{"type": "allAuthenticatedUsers", "id": "", "deleted": false, "uid": ""}},
		{"deleted_user", "deleted:user:alice@example.com?uid=123456789", map[string]any{"type": "user", "id": "alice@example.com", "deleted": true, "uid": "123456789"}},
		{"deleted_singleton", "deleted:allUsers", map[string]any{"type": "allUsers", "id": "", "deleted": true, "uid": ""}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := filters.ParseGCPIAMMember(tc.member)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("ParseGCPIAMMember(%q) = %#v, want %#v", tc.member, got, tc.want)
			}
		})
	}
}

func TestParseGCPIAMMember_Malformed(t *testing.T) {
	cases := []struct {
		name   string
		member string
	}{
		{"unrecognized_type", "principal:alice@example.com"},
		{"no_colon", "aliceexample.com"},
		{"empty_id", "user:"},
		{"empty", ""},
		{"over_cap", strings.Repeat("a", filters.MaxInputBytes+1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.ParseGCPIAMMember(tc.member); got != nil {
				t.Errorf("ParseGCPIAMMember(%q) = %#v, want nil", tc.member, got)
			}
		})
	}
}
