// Package awscloud_test: tests that an AWS SDK failure reads as AWS's words,
// bounded and quoted, with the SDK's own error still underneath.
package awscloud_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/smithy-go"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/awscloud"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/farside"
)

// farSideClient builds a real Client whose every call reaches handler, so
// the SDK parses an answer exactly as it parses AWS's own.
func farSideClient(t *testing.T, handler http.HandlerFunc) *awscloud.Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c, err := awscloud.New(testRegion, testAccessKeyID, testSecretAccessKey, "", awscloud.WithEndpoint(srv.URL))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

// TestSDKError_BoundsAndQuotesAWSsWordsAndKeepsTheSDKsError drives each
// service's real SDK client against an endpoint answering with AWS's own
// error documents, whose message is far longer than farside.MaxBytes and
// carries a terminal escape. The error must say what the call was doing,
// show the escape quoted rather than send it, stay bounded, and still let
// errors.As find the SDK's smithy.APIError with the code AWS sent.
func TestSDKError_BoundsAndQuotesAWSsWordsAndKeepsTheSDKsError(t *testing.T) {
	// XML forbids C0 control bytes such as ESC, so AWS's message carries the
	// C1 control CSI (U+009B, written as its UTF-8 bytes), which XML allows
	// and some terminals read as the start of an escape sequence.
	const csi = "\xc2\x9b"
	long := csi + "31mred" + csi + "0m " + strings.Repeat("the bucket you tried to delete is not empty ", 40)
	// quotedCSI is how strconv.Quote, and so farside, shows CSI.
	quotedCSI := "\\" + "u009b"
	for _, tc := range []struct {
		name   string
		status int
		body   string
		call   func(*awscloud.Client) error
		op     string
		code   string
	}{
		{
			name:   "S3",
			status: http.StatusConflict,
			body:   "<?xml version=\"1.0\" encoding=\"UTF-8\"?><Error><Code>BucketNotEmpty</Code><Message>" + long + "</Message><BucketName>b</BucketName></Error>",
			call:   func(c *awscloud.Client) error { return c.DeleteBucket(context.Background(), "b") },
			op:     `awscloud: deleting bucket "b": `,
			code:   "BucketNotEmpty",
		},
		{
			name:   "EC2",
			status: http.StatusBadRequest,
			body:   "<?xml version=\"1.0\" encoding=\"UTF-8\"?><Response><Errors><Error><Code>InvalidAMIID.Malformed</Code><Message>" + long + "</Message></Error></Errors><RequestID>r-1</RequestID></Response>",
			call: func(c *awscloud.Client) error {
				_, err := c.RunInstance(context.Background(), "web", "ami-x", "t3.micro")
				return err
			},
			op:   `awscloud: launching instance "web": `,
			code: "InvalidAMIID.Malformed",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := farSideClient(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/xml")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			})
			err := tc.call(c)
			if err == nil {
				t.Fatal("got nil error, want AWS's refusal")
			}
			msg := err.Error()
			if !strings.HasPrefix(msg, tc.op+`"`) {
				t.Errorf("error %q does not open with %q and a quote", msg, tc.op)
			}
			if strings.Contains(msg, csi) {
				t.Errorf("error %q sends a raw control character", msg)
			}
			if !strings.Contains(msg, quotedCSI+"31mred") {
				t.Errorf("error %q does not show AWS's words with the escape quoted", msg)
			}
			if got, max := len(msg), len(tc.op)+farside.MaxBytes+5; got > max {
				t.Errorf("error is %d bytes, want at most %d: %q", got, max, msg)
			}
			if !strings.HasSuffix(msg, `..."`) {
				t.Errorf("error %q is not marked as cut", msg)
			}
			var apiErr smithy.APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("errors.As found no smithy.APIError in %T", err)
			}
			if apiErr.ErrorCode() != tc.code {
				t.Errorf("ErrorCode() = %q, want %q", apiErr.ErrorCode(), tc.code)
			}
			// The SDK's error underneath keeps AWS's whole message: only
			// this package's text is bounded.
			if len(apiErr.ErrorMessage()) < len(long)-1 {
				t.Errorf("the SDK's own message was rewritten: %q", apiErr.ErrorMessage())
			}
		})
	}
}

// TestSDKError_AccessDeniedStillReadsThroughTheWrap holds AccessDenied to
// the wrapped error a real ListObjectVersions refusal returns, since a
// caller deciding whether it may only look reads the code through it.
func TestSDKError_AccessDeniedStillReadsThroughTheWrap(t *testing.T) {
	c := farSideClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`<Error><Code>AccessDenied</Code><Message>Access Denied</Message></Error>`))
	})
	_, err := c.BucketHoldsAnything(context.Background(), "b")
	if err == nil {
		t.Fatal("got nil error, want AccessDenied")
	}
	if !awscloud.AccessDenied(err) {
		t.Errorf("AccessDenied(%v) = false through the wrap", err)
	}
	if !strings.Contains(err.Error(), `awscloud: listing bucket "b": "`) {
		t.Errorf("error %q does not say what the call was doing", err)
	}
}

// TestSDKError_ACanceledCallIsStillCanceled holds errors.Is to the wrap: a
// call whose context was canceled before it ran reports context.Canceled,
// so a caller can tell giving up from AWS refusing.
func TestSDKError_ACanceledCallIsStillCanceled(t *testing.T) {
	c := farSideClient(t, func(w http.ResponseWriter, _ *http.Request) {
		t.Error("a canceled call reached the endpoint")
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := c.CreateBucket(ctx, "b")
	if err == nil {
		t.Fatal("got nil error from a canceled call")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("errors.Is(%v, context.Canceled) = false", err)
	}
	if !strings.HasPrefix(err.Error(), `awscloud: creating bucket "b": "`) {
		t.Errorf("error %q does not quote the SDK's text after what the call was doing", err)
	}
}
