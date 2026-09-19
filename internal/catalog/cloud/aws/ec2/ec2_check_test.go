// Package ec2_test: tests of the cloud.aws.ec2 checks, against the same
// real LocalStack container the rest of this package's tests use.
package ec2_test

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsec2 "github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// instanceStates answers from LocalStack itself, over a client of the
// test's own, with the state of every instance tagged name, so a check's
// claim to have launched or terminated nothing is read from the service
// rather than from the method under test.
func instanceStates(t *testing.T, endpoint, name string) map[string]string {
	t.Helper()
	client := awsec2.New(awsec2.Options{
		Region:       "us-east-1",
		Credentials:  credentials.NewStaticCredentialsProvider(testAccessKeyID, testSecretAccessKey, ""),
		BaseEndpoint: aws.String(endpoint),
	})
	out, err := client.DescribeInstances(context.Background(), &awsec2.DescribeInstancesInput{
		Filters: []ec2types.Filter{{Name: aws.String("tag:Name"), Values: []string{name}}},
	})
	if err != nil {
		t.Fatalf("describing instances: %v", err)
	}
	states := map[string]string{}
	for _, r := range out.Reservations {
		for _, inst := range r.Instances {
			states[aws.ToString(inst.InstanceId)] = string(inst.State.Name)
		}
	}
	return states
}

// diffOf returns a recorded diff's before and after halves.
func diffOf(t *testing.T, rc *ctxStub) (before, after map[string]any) {
	t.Helper()
	diff, ok := rc.stats[sdk.StatDiff].(map[string]any)
	if !ok {
		t.Fatalf("no diff recorded: %v", rc.stats)
	}
	before, _ = diff[sdk.DiffBefore].(map[string]any)
	after, _ = diff[sdk.DiffAfter].(map[string]any)
	return before, after
}

// TestChecks_AgainstLocalStack runs each method's check, then its real
// run, through an instance's life: the check launches and terminates
// nothing (LocalStack is asked directly), decides the change the real run
// makes, predicts only what it can know (a new instance's existence; a
// terminated one's identity, not its state), every key it states is the
// one the real run leaves, and it records no undo instruction.
func TestChecks_AgainstLocalStack(t *testing.T) {
	endpoint := requireLocalStack(t)
	name := uniqueName(t)
	create, terminate := lookup(t, "cloud.aws.ec2.create"), lookup(t, "cloud.aws.ec2.terminate")
	for _, d := range []collection.Descriptor{create, terminate} {
		if !d.Manifest.SupportsCheck || d.Check == nil {
			t.Fatalf("%s does not declare a check", d.Name)
		}
	}
	createParams := map[string]any{"name": name, "image_id": "ami-12345678", "instance_type": "t2.micro"}

	// compare runs d's check and then its real run, and fails t unless
	// the check changed nothing and predicted what the run did.
	compare := func(d collection.Descriptor, params map[string]any) (checked *ctxStub, changed bool) {
		t.Helper()
		before := instanceStates(t, endpoint, name)
		h := newHarness(t)
		result, err := d.Check(context.Background(), h.rc, h.device, params)
		if err != nil {
			t.Fatalf("%s check: %v", d.Name, err)
		}
		if after := instanceStates(t, endpoint, name); !sameStates(before, after) {
			t.Fatalf("%s's check changed the account: %v became %v", d.Name, before, after)
		}
		if _, ok := h.rc.stats[sdk.StatInverse]; ok {
			t.Errorf("%s's check recorded an undo instruction for a change it never made", d.Name)
		}
		real := newHarness(t)
		ran, err := d.Invoke(context.Background(), real.rc, real.device, params)
		if err != nil {
			t.Fatalf("%s real run: %v", d.Name, err)
		}
		if result.Changed != ran.Changed {
			t.Errorf("%s: the check predicted Changed = %v, the real run reported %v", d.Name, result.Changed, ran.Changed)
		}
		_, predicted := diffOf(t, h.rc)
		_, actual := diffOf(t, real.rc)
		for key, want := range predicted {
			if actual[key] != want {
				t.Errorf("%s: predicted %s = %v, the real run left %v", d.Name, key, want, actual[key])
			}
		}
		return h.rc, result.Changed
	}

	rc, changed := compare(create, createParams)
	if !changed {
		t.Fatal("the check of creating an absent instance predicted no launch")
	}
	if _, predicted := diffOf(t, rc); len(predicted) != 1 {
		t.Errorf("the predicted new instance is %v, want only that one would exist", predicted)
	}
	if _, stated := rc.stats["instance_id"]; stated {
		t.Error("the check stated an instance ID, which only AWS assigns")
	}
	ids := instanceStates(t, endpoint, name)
	if len(ids) != 1 {
		t.Fatalf("the real run left %v, want one instance", ids)
	}
	var id string
	for k := range ids {
		id = k
	}

	if _, changed := compare(create, createParams); changed {
		t.Error("the check of creating a present instance predicted a launch")
	}

	rc, changed = compare(terminate, map[string]any{"instance_id": id})
	if !changed {
		t.Fatal("the check of terminating a live instance predicted nothing")
	}
	if _, predicted := diffOf(t, rc); predicted["instance_id"] != id {
		t.Errorf("the predicted instance is %v, want %s", predicted, id)
	}
}

// sameStates reports whether two instance-state maps are equal.
func sameStates(a, b map[string]string) bool {
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return len(a) == len(b)
}

// TestChecks_FailWhenTheyCannotRecord covers a check that cannot record
// its answer, the instance ID or the diff, for an instance that exists and
// for a name that matches none: it fails naming the method, as the real
// run does, rather than reporting a decision with nothing behind it, and
// it launches and terminates nothing.
func TestChecks_FailWhenTheyCannotRecord(t *testing.T) {
	endpoint := requireLocalStack(t)
	name := uniqueName(t)
	create, terminate := lookup(t, "cloud.aws.ec2.create"), lookup(t, "cloud.aws.ec2.terminate")
	createParams := func(name string) map[string]any {
		return map[string]any{"name": name, "image_id": "ami-12345678", "instance_type": "t2.micro"}
	}
	if _, err := create.Invoke(context.Background(), newHarness(t).rc, newHarness(t).device, createParams(name)); err != nil {
		t.Fatalf("launching the instance the checks read: %v", err)
	}
	var id string
	for k := range instanceStates(t, endpoint, name) {
		id = k
	}
	if id == "" {
		t.Fatal("the launch left no instance")
	}

	for _, tc := range []struct {
		name    string
		d       collection.Descriptor
		params  map[string]any
		failKey string
	}{
		{"create, present, the instance ID", create, createParams(name), "instance_id"},
		// uniqueName is the test's own name, so the absent one is suffixed.
		{"create, absent, the diff", create, createParams(name + "-absent"), sdk.StatDiff},
		{"terminate, the instance ID", terminate, map[string]any{"instance_id": id}, "instance_id"},
		{"terminate, the diff", terminate, map[string]any{"instance_id": id}, sdk.StatDiff},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := instanceStates(t, endpoint, name)
			h := newHarness(t)
			h.rc.failOnKey = tc.failKey
			_, err := tc.d.Check(context.Background(), h.rc, h.device, tc.params)
			if err == nil || !strings.HasPrefix(err.Error(), tc.d.Name+": ") {
				t.Errorf("check = %v, want the failure named for %s", err, tc.d.Name)
			}
			if after := instanceStates(t, endpoint, name); !sameStates(before, after) {
				t.Errorf("the check changed the account: %v became %v", before, after)
			}
		})
	}
}
