package pki

import "context"

// PKIEngine handles the mTLS mesh. V1 is internal, V2 is HashiCorp Vault.
type PKIEngine interface {
	// SignCSR takes a Certificate Signing Request from a Runner and returns a 72-hour cert.
	SignCSR(ctx context.Context, csrPEM []byte) (certPEM []byte, error)
}
