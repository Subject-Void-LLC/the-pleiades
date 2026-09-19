// Package s3_test: tests of the delete_bucket check when listing a
// bucket's versions is refused or fails, which LocalStack, enforcing no
// IAM policy, cannot be made to do.
package s3_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	inventorytest "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
)

// listingRefusedS3 serves the two calls a delete_bucket check makes, the
// way S3 answers them: the bucket exists, and listing its versions is
// answered with status and an S3 error document carrying code. The real
// SDK decodes that document, so the check classifies a real response.
func listingRefusedS3(t *testing.T, status int, code string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodHead:
			w.WriteHeader(http.StatusOK)
		case r.URL.Query().Has("versions"):
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><Error><Code>` + code + `</Code><Message>refused by the test</Message></Error>`))
		default:
			t.Errorf("the check sent %s %s, which is neither of its two reads", r.Method, r.URL)
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// TestDeleteBucketCheck_WhenListingIsRefusedOrFails covers the two answers
// a check gives when it cannot learn whether the bucket is empty. A policy
// that allows deleting a bucket without allowing listing its versions is
// ordinary, so AccessDenied makes the call unchecked, naming why; any
// other failure is a failure, named for the method, and never read as
// "empty". Neither sends a delete, which the server would report.
func TestDeleteBucketCheck_WhenListingIsRefusedOrFails(t *testing.T) {
	d := lookup(t, "cloud.aws.s3.delete_bucket")
	for _, tc := range []struct {
		name   string
		status int
		code   string
	}{
		{"access denied", http.StatusForbidden, "AccessDenied"},
		{"another error", http.StatusBadRequest, "InvalidArgument"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rc := &ctxStub{secrets: map[string]string{"username": testAccessKeyID, "password": testSecretAccessKey}, stats: map[string]any{}}
			device := &awsAccount{
				Stub:     &inventorytest.Stub{StubName: "test-account", Caps: []capability.Name{capability.NameAWSAPI}},
				region:   "us-east-1",
				endpoint: listingRefusedS3(t, tc.status, tc.code),
			}
			_, err := d.Check(context.Background(), rc, device, map[string]any{"bucket": "kept"})
			var cannot *collection.CannotCheckError
			isCannot := errors.As(err, &cannot)
			if tc.code == "AccessDenied" {
				if !isCannot || !strings.Contains(cannot.Reason, "may not list bucket kept's versions") {
					t.Errorf("check = %v, want a CannotCheckError saying the versions cannot be listed", err)
				}
				return
			}
			if err == nil || isCannot || !strings.HasPrefix(err.Error(), "cloud.aws.s3.delete_bucket: ") || !strings.Contains(err.Error(), tc.code) {
				t.Errorf("check = %v, want a failure named for the method carrying %s", err, tc.code)
			}
		})
	}
}
