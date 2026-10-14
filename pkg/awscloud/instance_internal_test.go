// Tests for how an EC2 instance description becomes an Instance, with no
// AWS endpoint: every field instanceFrom reads, present and absent.
package awscloud

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// TestInstanceFrom_ReadsEachFieldAndTheWindowsPlatform covers a Windows
// instance with every field set and its Name tag after another tag, and an
// instance with nothing but an ID, whose platform stays empty (EC2 reports
// no platform for Linux).
func TestInstanceFrom_ReadsEachFieldAndTheWindowsPlatform(t *testing.T) {
	got := instanceFrom(ec2types.Instance{
		InstanceId:       aws.String("i-0abc"),
		PublicIpAddress:  aws.String("203.0.113.7"),
		PrivateIpAddress: aws.String("10.0.0.7"),
		InstanceType:     ec2types.InstanceTypeT3Micro,
		ImageId:          aws.String("ami-0123"),
		State:            &ec2types.InstanceState{Name: ec2types.InstanceStateNameRunning},
		Placement:        &ec2types.Placement{AvailabilityZone: aws.String("us-east-1a")},
		Platform:         ec2types.PlatformValuesWindows,
		Tags:             []ec2types.Tag{{Key: aws.String("team"), Value: aws.String("ops")}, {Key: aws.String("Name"), Value: aws.String("dc1")}},
	})
	want := Instance{ID: "i-0abc", PublicIP: "203.0.113.7", PrivateIP: "10.0.0.7", InstanceType: "t3.micro", ImageID: "ami-0123",
		State: "running", AvailabilityZone: "us-east-1a", Platform: "windows", Name: "dc1"}
	if *got != want {
		t.Errorf("instanceFrom = %+v, want %+v", *got, want)
	}

	if bare := instanceFrom(ec2types.Instance{InstanceId: aws.String("i-0def")}); *bare != (Instance{ID: "i-0def"}) {
		t.Errorf("a bare instance = %+v, want only its ID", *bare)
	}
}
