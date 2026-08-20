package filters_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/filters"
)

// BenchmarkParseARN and BenchmarkBuildARN measure this phase's own
// identifier parse/build pair, the same "prove cost is proportional to
// input, not exploding" concern every document/identifier-shaped
// filter's own benchmark exists for elsewhere in this Part.
func BenchmarkParseARN(b *testing.B) {
	arn := "arn:aws:iam::123456789012:role/MyRealisticRoleName"
	for i := 0; i < b.N; i++ {
		filters.ParseARN(arn)
	}
}

func BenchmarkBuildARN(b *testing.B) {
	parts := map[string]any{
		"partition": "aws", "service": "iam", "region": "",
		"account_id": "123456789012", "resource": "role/MyRealisticRoleName",
	}
	for i := 0; i < b.N; i++ {
		filters.BuildARN(parts)
	}
}

func BenchmarkParseAzureResourceID(b *testing.B) {
	id := "/subscriptions/11111111-1111-1111-1111-111111111111/resourceGroups/prod-rg/providers/Microsoft.Network/virtualNetworks/my-vnet/subnets/my-subnet"
	for i := 0; i < b.N; i++ {
		filters.ParseAzureResourceID(id)
	}
}

func BenchmarkParseGCPSelfLink(b *testing.B) {
	link := "https://www.googleapis.com/compute/v1/projects/my-project/zones/us-central1-a/instances/my-vm"
	for i := 0; i < b.N; i++ {
		filters.ParseGCPSelfLink(link)
	}
}

// BenchmarkFormatCurrency measures the math/big.Rat-based rounding path
// this function deliberately chose over float64 (see FormatCurrency's
// own doc comment), establishing that the precision-safety trade-off
// does not come with a surprising cost.
func BenchmarkFormatCurrency(b *testing.B) {
	for i := 0; i < b.N; i++ {
		filters.FormatCurrency("1234567.89", "USD")
	}
}

// BenchmarkCloudInitWrap measures the MIME envelope + base64 encoding
// path against a realistic multi-line shell script payload.
func BenchmarkCloudInitWrap(b *testing.B) {
	script := "#!/bin/bash\napt-get update\napt-get install -y nginx\nsystemctl enable nginx\nsystemctl start nginx\n"
	for i := 0; i < b.N; i++ {
		filters.CloudInitWrap(script, "")
	}
}

// BenchmarkIAMPolicyMerger measures the JSON decode + canonical-JSON
// dedupe pass against two realistic multi-statement policy documents.
func BenchmarkIAMPolicyMerger(b *testing.B) {
	a := `{"Version":"2012-10-17","Statement":[` +
		`{"Effect":"Allow","Action":"s3:GetObject","Resource":"arn:aws:s3:::my-bucket/*"},` +
		`{"Effect":"Allow","Action":"s3:ListBucket","Resource":"arn:aws:s3:::my-bucket"}]}`
	c := `{"Version":"2012-10-17","Statement":[` +
		`{"Effect":"Allow","Action":"s3:GetObject","Resource":"arn:aws:s3:::my-bucket/*"},` +
		`{"Effect":"Deny","Action":"s3:DeleteObject","Resource":"arn:aws:s3:::my-bucket/*"}]}`
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		filters.IAMPolicyMerger(a, c)
	}
}
