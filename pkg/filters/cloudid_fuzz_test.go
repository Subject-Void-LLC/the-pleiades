package filters_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/filters"
)

// This file fuzzes every parser in cloudid.go against malformed input,
// per this phase's own Fuzz/Stress Test checklist item naming the ARN,
// Azure Resource ID and GCP self-link parsers explicitly.
// ParseGCPIAMMember is included too, the same "every non-trivial parser
// gets a fuzz target" convention pki_fuzz_test.go's own comment states.

func FuzzParseARN(f *testing.F) {
	seeds := []string{
		"", "arn:aws:iam::123456789012:role/MyRole",
		"arn:aws:rds:us-east-1:123456789012:db:mydatabase",
		"arn:aws:s3:::my-bucket",
		"not-an-arn", "arn:", "arn:aws:iam::123456789012",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, arn string) {
		filters.ParseARN(arn)
	})
}

func FuzzBuildARN(f *testing.F) {
	f.Add("aws", "iam", "us-east-1", "123456789012", "role/MyRole")
	f.Fuzz(func(t *testing.T, partition, service, region, accountID, resource string) {
		filters.BuildARN(map[string]any{
			"partition": partition, "service": service, "region": region,
			"account_id": accountID, "resource": resource,
		})
	})
}

func FuzzParseAzureResourceID(f *testing.F) {
	seeds := []string{
		"", "/subscriptions/sub-1/resourceGroups/my-rg/providers/Microsoft.Compute/virtualMachines/my-vm",
		"/subscriptions/sub-1/resourceGroups/my-rg/providers/Microsoft.Network/virtualNetworks/my-vnet/subnets/my-subnet",
		"not-a-resource-id", "/subscriptions/", "/subscriptions/sub-1/resourceGroups",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, id string) {
		filters.ParseAzureResourceID(id)
	})
}

func FuzzParseGCPSelfLink(f *testing.F) {
	seeds := []string{
		"", "https://www.googleapis.com/compute/v1/projects/my-project/zones/us-central1-a/instances/my-vm",
		"https://www.googleapis.com/compute/v1/projects/my-project/global/networks/my-network",
		"not-a-url", "https://example.com/no/projects/here",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, selfLink string) {
		filters.ParseGCPSelfLink(selfLink)
	})
}

func FuzzParseGCPIAMMember(f *testing.F) {
	seeds := []string{
		"", "user:alice@example.com", "allUsers", "allAuthenticatedUsers",
		"deleted:user:alice@example.com?uid=123", "not-a-member", "user:",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, member string) {
		filters.ParseGCPIAMMember(member)
	})
}
