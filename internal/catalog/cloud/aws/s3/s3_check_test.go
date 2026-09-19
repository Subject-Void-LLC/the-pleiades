// Package s3_test: tests of the cloud.aws.s3 checks, against the same real
// LocalStack container the rest of this package's tests use.
package s3_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// bucketExists answers from LocalStack itself, over a client of the
// test's own, so a check's claim to have changed nothing is read from the
// service rather than from the method under test.
func bucketExists(t *testing.T, endpoint, bucket string) bool {
	t.Helper()
	_, err := testS3(endpoint).HeadBucket(context.Background(), &awss3.HeadBucketInput{Bucket: aws.String(bucket)})
	return err == nil
}

// testS3 is an S3 client of the test's own, pointed at LocalStack.
func testS3(endpoint string) *awss3.Client {
	return awss3.New(awss3.Options{
		Region:       "us-east-1",
		Credentials:  credentials.NewStaticCredentialsProvider(testAccessKeyID, testSecretAccessKey, ""),
		BaseEndpoint: aws.String(endpoint),
		UsePathStyle: true,
	})
}

// predictedExists returns a recorded diff's after half's exists flag.
func predictedExists(t *testing.T, rc *ctxStub) any {
	t.Helper()
	diff, ok := rc.stats[sdk.StatDiff].(map[string]any)
	if !ok {
		t.Fatalf("no diff recorded: %v", rc.stats)
	}
	after, _ := diff[sdk.DiffAfter].(map[string]any)
	return after["exists"]
}

// TestChecks_AgainstLocalStack runs each method's check, then its real
// run, from each state a bucket passes through: the check creates or
// deletes nothing (LocalStack is asked directly), predicts the change and
// the existence the real run then leaves, and records no undo
// instruction. A bucket that holds an object is unchecked rather than
// predicted deleted, and the real run shows why: S3 refuses it.
func TestChecks_AgainstLocalStack(t *testing.T) {
	endpoint := requireLocalStack(t)
	bucket := uniqueBucket(t)
	create, remove := lookup(t, "cloud.aws.s3.create_bucket"), lookup(t, "cloud.aws.s3.delete_bucket")
	for _, d := range []collection.Descriptor{create, remove} {
		if !d.Manifest.SupportsCheck || d.Check == nil {
			t.Fatalf("%s does not declare a check", d.Name)
		}
	}
	params := map[string]any{"bucket": bucket}

	step := func(d collection.Descriptor, existsBefore, wantChange bool) {
		t.Helper()
		h := newHarness(t)
		checked, err := d.Check(context.Background(), h.rc, h.device, params)
		if err != nil {
			t.Fatalf("%s check: %v", d.Name, err)
		}
		if got := bucketExists(t, endpoint, bucket); got != existsBefore {
			t.Fatalf("%s's check changed the bucket: it exists = %v, want %v", d.Name, got, existsBefore)
		}
		if _, ok := h.rc.stats[sdk.StatInverse]; ok {
			t.Errorf("%s's check recorded an undo instruction for a change it never made", d.Name)
		}
		real := newHarness(t)
		ran, err := d.Invoke(context.Background(), real.rc, real.device, params)
		if err != nil {
			t.Fatalf("%s real run: %v", d.Name, err)
		}
		if checked.Changed != wantChange || ran.Changed != wantChange {
			t.Errorf("%s: the check predicted Changed = %v and the real run reported %v, want %v", d.Name, checked.Changed, ran.Changed, wantChange)
		}
		if got, want := predictedExists(t, h.rc), bucketExists(t, endpoint, bucket); got != want {
			t.Errorf("%s: predicted exists = %v, the real run left %v", d.Name, got, want)
		}
	}

	step(create, false, true)
	step(create, true, false)

	if _, err := testS3(endpoint).PutObject(context.Background(), &awss3.PutObjectInput{
		Bucket: aws.String(bucket), Key: aws.String("kept"), Body: strings.NewReader("x"),
	}); err != nil {
		t.Fatalf("putting an object: %v", err)
	}
	h := newHarness(t)
	_, err := remove.Check(context.Background(), h.rc, h.device, params)
	var cannot *collection.CannotCheckError
	if !errors.As(err, &cannot) || !strings.Contains(cannot.Reason, "holds objects") {
		t.Fatalf("a check of deleting a bucket that holds an object = %v, want a CannotCheckError", err)
	}
	if _, err := remove.Invoke(context.Background(), newHarness(t).rc, h.device, params); err == nil {
		t.Fatal("the real run deleted a bucket that holds an object, so the check declined for nothing")
	}
	if _, err := testS3(endpoint).DeleteObject(context.Background(), &awss3.DeleteObjectInput{Bucket: aws.String(bucket), Key: aws.String("kept")}); err != nil {
		t.Fatalf("deleting the object: %v", err)
	}

	step(remove, true, true)
	step(remove, false, false)
}
