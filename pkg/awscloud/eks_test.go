// Tests that an EKS token is the presigned STS request EKS documents, with
// its signature recomputed here from the published SigV4 algorithm rather
// than by the signer that made it.
package awscloud

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/url"
	"sort"
	"strings"
	"testing"
	"time"
)

// decodeEKSToken returns the presigned URL inside token.
func decodeEKSToken(t *testing.T, token string) *url.URL {
	t.Helper()
	body, ok := strings.CutPrefix(token, eksTokenPrefix)
	if !ok {
		t.Fatalf("token %q does not begin %q", token, eksTokenPrefix)
	}
	raw, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		t.Fatalf("the token is not base64url without padding: %v", err)
	}
	u, err := url.Parse(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// awsEscape is SigV4's URI encoding: every byte but the unreserved ones.
func awsEscape(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(url.QueryEscape(s), "+", "%20"), "%7E", "~")
}

func hmacSHA256(key []byte, data string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(data))
	return m.Sum(nil)
}

// sigV4 recomputes a presigned GET's signature from AWS's published
// algorithm: the canonical request, the string to sign, and the derived
// signing key.
func sigV4(u *url.URL, headers map[string]string, secret, region, service string) string {
	q := u.Query()
	keys := make([]string, 0, len(q))
	for k := range q {
		if k != "X-Amz-Signature" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(keys))
	for _, k := range keys {
		pairs = append(pairs, awsEscape(k)+"="+awsEscape(q.Get(k)))
	}
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	sort.Strings(names)
	var canonicalHeaders strings.Builder
	for _, name := range names {
		canonicalHeaders.WriteString(name + ":" + headers[name] + "\n")
	}
	canonical := strings.Join([]string{"GET", "/", strings.Join(pairs, "&"), canonicalHeaders.String(), strings.Join(names, ";"), emptyPayloadHash}, "\n")
	digest := sha256.Sum256([]byte(canonical))
	amzDate := q.Get("X-Amz-Date")
	scope := amzDate[:8] + "/" + region + "/" + service + "/aws4_request"
	toSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + hex.EncodeToString(digest[:])
	key := hmacSHA256([]byte("AWS4"+secret), amzDate[:8])
	for _, part := range []string{region, service, "aws4_request"} {
		key = hmacSHA256(key, part)
	}
	return hex.EncodeToString(hmacSHA256(key, toSign))
}

// TestEKSToken_IsThePresignedRequestEKSReads: the token is k8s-aws-v1. and
// a presigned GetCallerIdentity against the region's STS, valid sixty
// seconds, signed over the cluster header, with a signature that matches an
// independent SigV4 computation.
func TestEKSToken_IsThePresignedRequestEKSReads(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, region, session, host string
	}{
		{"static key", "us-east-1", "", "sts.us-east-1.amazonaws.com"},
		{"with a session token", "eu-west-2", "session-token-value", "sts.eu-west-2.amazonaws.com"},
		{"a China region", "cn-north-1", "", "sts.cn-north-1.amazonaws.com.cn"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			token, expires, err := EKSToken(context.Background(), tc.region, "prod-cluster", "AKIDEXAMPLE", "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY", tc.session, now)
			if err != nil {
				t.Fatal(err)
			}
			if !expires.Equal(now.Add(EKSTokenLifetime)) {
				t.Errorf("expires %s, want %s", expires, now.Add(EKSTokenLifetime))
			}
			u := decodeEKSToken(t, token)
			q := u.Query()
			for key, want := range map[string]string{
				"Action": "GetCallerIdentity", "Version": "2011-06-15", "X-Amz-Expires": "60",
				"X-Amz-Algorithm": "AWS4-HMAC-SHA256", "X-Amz-Date": "20261014T120000Z",
				"X-Amz-Credential":    "AKIDEXAMPLE/20261014/" + tc.region + "/sts/aws4_request",
				"X-Amz-SignedHeaders": "host;x-k8s-aws-id", "X-Amz-Security-Token": tc.session,
			} {
				if got := q.Get(key); got != want {
					t.Errorf("%s = %q, want %q", key, got, want)
				}
			}
			if u.Scheme != "https" || u.Host != tc.host || u.Path != "/" {
				t.Errorf("the request goes to %s://%s%s, want https://%s/", u.Scheme, u.Host, u.Path, tc.host)
			}
			want := sigV4(u, map[string]string{"host": tc.host, "x-k8s-aws-id": "prod-cluster"}, "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY", tc.region, "sts")
			if got := q.Get("X-Amz-Signature"); got != want {
				t.Errorf("signature %s, want %s from the published algorithm", got, want)
			}
			// The cluster is part of what is signed: the same signature does
			// not hold for another cluster.
			if other := sigV4(u, map[string]string{"host": tc.host, "x-k8s-aws-id": "other-cluster"}, "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY", tc.region, "sts"); other == q.Get("X-Amz-Signature") {
				t.Error("the signature does not depend on the cluster it is for")
			}
		})
	}
}

// TestEKSToken_Refusals: what cannot sign a token for a cluster is refused
// before anything is signed.
func TestEKSToken_Refusals(t *testing.T) {
	now := time.Now()
	for name, args := range map[string][5]string{
		"no access key":        {"us-east-1", "prod", "", "secret", ""},
		"no secret":            {"us-east-1", "prod", "AKID", "", ""},
		"a host as region":     {"evil.example.com/x", "prod", "AKID", "secret", ""},
		"a bad cluster name":   {"us-east-1", "prod cluster", "AKID", "secret", ""},
		"an empty region":      {"", "prod", "AKID", "secret", ""},
		"a long cluster name":  {"us-east-1", strings.Repeat("c", 101), "AKID", "secret", ""},
		"a header in the name": {"us-east-1", "prod\r\nX: y", "AKID", "secret", ""},
	} {
		if _, _, err := EKSToken(context.Background(), args[0], args[1], args[2], args[3], args[4], now); err == nil {
			t.Errorf("%s: a token was minted", name)
		}
	}
	// A caller with no context gets an error naming the request it could
	// not build, never a panic.
	var none context.Context
	if _, _, err := EKSToken(none, "us-east-1", "prod", "AKID", "secret", "", now); err == nil || !strings.Contains(err.Error(), "building the STS request") {
		t.Errorf("no context: %v, want the request refused", err)
	}
}
