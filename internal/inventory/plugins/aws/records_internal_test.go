package aws

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/awscloud"
)

// These are white-box tests of the two small property-derivation
// functions instanceRecord/instanceName delegate to, covering the
// fallback branches (no public IP, no Name tag) a real LocalStack
// instance's default networking never produces, so the external
// LocalStack-backed suite (aws_localstack_test.go) cannot exercise them
// without inventing scope (this plugin does not modify or reach into
// networking/tagging) purely to force a test case.

func TestInstanceRecord_FallsBackToPrivateIP(t *testing.T) {
	rec := instanceRecord(awscloud.Instance{ID: "i-1", PrivateIP: "10.0.0.5"})
	if got := rec.Properties[propHost]; got != "10.0.0.5" {
		t.Errorf("Properties[host] = %v, want the private IP when no public IP is set", got)
	}
	if got := rec.Properties[propIP]; got != "10.0.0.5" {
		t.Errorf("Properties[ip] = %v, want the private IP when no public IP is set", got)
	}
}

func TestInstanceRecord_NoIPAtAll(t *testing.T) {
	rec := instanceRecord(awscloud.Instance{ID: "i-1"})
	if _, ok := rec.Properties[propHost]; ok {
		t.Errorf("Properties[host] = %v, want the key absent when neither IP is set", rec.Properties[propHost])
	}
}

func TestInstanceName_FallsBackToID(t *testing.T) {
	if got := instanceName(awscloud.Instance{ID: "i-1"}); got != "i-1" {
		t.Errorf("instanceName with no Name tag = %q, want the instance id %q", got, "i-1")
	}
}
