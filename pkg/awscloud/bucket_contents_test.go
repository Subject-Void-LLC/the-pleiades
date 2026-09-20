// Package awscloud_test: tests of BucketHoldsAnything and AccessDenied.
package awscloud_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/awscloud"
)

// TestClient_BucketHoldsAnything covers the question DeleteBucket's
// refusal turns on, against LocalStack: an empty bucket holds nothing, one
// with an object holds something, and emptying it again answers nothing,
// so the answer tracks the bucket rather than its history. The object is
// put through a client of the test's own.
func TestClient_BucketHoldsAnything(t *testing.T) {
	skipInShortMode(t)
	c := testClient(t)
	ctx := context.Background()
	bucket := "pleiades-test-bucket-holds"
	if err := c.CreateBucket(ctx, bucket); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	raw := s3.New(s3.Options{
		Region:       testRegion,
		Credentials:  credentials.NewStaticCredentialsProvider(testAccessKeyID, testSecretAccessKey, ""),
		BaseEndpoint: aws.String(requireLocalStack(t)),
		UsePathStyle: true,
	})

	holds := func() bool {
		t.Helper()
		got, err := c.BucketHoldsAnything(ctx, bucket)
		if err != nil {
			t.Fatalf("BucketHoldsAnything: %v", err)
		}
		return got
	}
	if holds() {
		t.Error("a new bucket holds something")
	}
	if _, err := raw.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(bucket), Key: aws.String("k"), Body: strings.NewReader("x")}); err != nil {
		t.Fatalf("PutObject: %v", err)
	}
	if !holds() {
		t.Error("a bucket holding an object holds nothing")
	}
	if _, err := raw.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(bucket), Key: aws.String("k")}); err != nil {
		t.Fatalf("DeleteObject: %v", err)
	}
	if holds() {
		t.Error("an emptied bucket still holds something")
	}
	if err := c.DeleteBucket(ctx, bucket); err != nil {
		t.Fatalf("DeleteBucket of the emptied bucket: %v", err)
	}
}

func TestClient_BucketHoldsAnything_ConnectionFailure(t *testing.T) {
	c := unreachableClient(t)
	if _, err := c.BucketHoldsAnything(context.Background(), "whatever"); err == nil {
		t.Error("BucketHoldsAnything against an unreachable endpoint: got nil error, want one")
	}
}

// TestAccessDenied covers the one refusal a caller that only wanted to
// look may treat differently: AccessDenied, however wrapped, and nothing
// else, not even another AWS error code.
func TestAccessDenied(t *testing.T) {
	denied := &smithy.GenericAPIError{Code: "AccessDenied", Message: "no"}
	for _, tc := range []struct {
		err  error
		want bool
	}{
		{denied, true},
		{fmt.Errorf("listing: %w", denied), true},
		{&smithy.GenericAPIError{Code: "NoSuchBucket"}, false},
		{errors.New("AccessDenied"), false},
		{nil, false},
	} {
		if got := awscloud.AccessDenied(tc.err); got != tc.want {
			t.Errorf("AccessDenied(%v) = %v, want %v", tc.err, got, tc.want)
		}
	}
}
