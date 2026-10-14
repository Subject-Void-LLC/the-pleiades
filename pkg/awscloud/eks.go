// EKS authentication tokens, minted in Go from an AWS key: the presigned
// STS GetCallerIdentity request EKS documents (and aws-iam-authenticator
// and `aws eks get-token` produce), with no aws program run.
package awscloud

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
)

// EKS token constants, as EKS's authenticator reads them.
const (
	// eksTokenPrefix begins every EKS token; the rest is the presigned URL,
	// base64url without padding.
	eksTokenPrefix = "k8s-aws-v1."
	// eksClusterHeader binds a token to one cluster: it is signed, so a
	// token minted for one cluster is refused by another.
	eksClusterHeader = "x-k8s-aws-id"
	// eksPresignSeconds is how long the presigned request itself is valid,
	// what `aws eks get-token` asks for.
	eksPresignSeconds = 60
	// EKSTokenLifetime is how long EKS accepts a token after it is signed,
	// less a minute so a token is never sent at its edge.
	EKSTokenLifetime = 14 * time.Minute
	// emptyPayloadHash is the SHA-256 of an empty body, a GET's payload.
	emptyPayloadHash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
)

// Validation of what a record names. A cluster name is EKS's own rule; a
// region is checked for shape, so a record cannot point the signed request
// at a host that is not STS.
var (
	eksClusterName = regexp.MustCompile(`^[0-9A-Za-z][A-Za-z0-9_-]{0,99}$`)
	awsRegion      = regexp.MustCompile(`^[a-z]{2}(-[a-z]+)+-[0-9]+$`)
)

// ValidEKSClusterName reports whether name is an EKS cluster name.
func ValidEKSClusterName(name string) bool { return eksClusterName.MatchString(name) }

// ValidRegion reports whether region has the shape of an AWS region.
func ValidRegion(region string) bool { return awsRegion.MatchString(region) }

// stsHost is region's STS endpoint: the regional one, in the partition the
// region belongs to.
func stsHost(region string) string {
	if strings.HasPrefix(region, "cn-") {
		return "sts." + region + ".amazonaws.com.cn"
	}
	return "sts." + region + ".amazonaws.com"
}

// EKSToken mints a bearer token for EKS cluster in region from an access
// key (and an optional STS session token), signed at now. It returns the
// token and when to stop sending it. Nothing is sent anywhere: the token is
// a signed request EKS replays to STS to learn who presented it.
func EKSToken(ctx context.Context, region, cluster, accessKeyID, secretAccessKey, sessionToken string, now time.Time) (string, time.Time, error) {
	switch {
	case !ValidEKSClusterName(cluster):
		return "", time.Time{}, fmt.Errorf("awscloud: %q is not an EKS cluster name", cluster)
	case !ValidRegion(region):
		return "", time.Time{}, fmt.Errorf("awscloud: %q is not an AWS region", region)
	case accessKeyID == "" || secretAccessKey == "":
		return "", time.Time{}, errors.New("awscloud: an EKS token needs an access key ID and its secret access key")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("https://%s/?Action=GetCallerIdentity&Version=2011-06-15&X-Amz-Expires=%d", stsHost(region), eksPresignSeconds), nil)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("awscloud: building the STS request: %w", err)
	}
	req.Header.Set(eksClusterHeader, cluster)
	creds := aws.Credentials{AccessKeyID: accessKeyID, SecretAccessKey: secretAccessKey, SessionToken: sessionToken}
	signed, _, err := v4.NewSigner().PresignHTTP(ctx, creds, req, emptyPayloadHash, "sts", region, now.UTC())
	if err != nil {
		return "", time.Time{}, fmt.Errorf("awscloud: signing the STS request: %w", err)
	}
	return eksTokenPrefix + base64.RawURLEncoding.EncodeToString([]byte(signed)), now.Add(EKSTokenLifetime), nil
}
