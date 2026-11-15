// The one check a client certificate's PEM is held to before it is stored.
package wire

import "bytes"

// pemBegin opens every PEM block, whatever its type, and pemDashes closes the
// type that follows it.
var (
	pemBegin  = []byte("-----BEGIN ")
	pemDashes = []byte("-----")
)

// ClientCertificateProblem says why text cannot be stored as a client
// certificate (SecretCertificatePEM's value), or "" when it can: every PEM
// block it holds is a CERTIFICATE. The clause follows the field's name in
// each caller's message, and it says nothing of the text itself.
//
// A certificate is the one credential field that is not masked by value
// (it is public by construction, and internal/engine.CredentialSecrets
// leaves it readable), so a private key pasted into it beside the
// certificate, the shape an operator's "cert and key" bundle takes, would
// travel and print as public. Refusing it where it is stored is what keeps
// that field's public marker true.
//
// A block is judged by its BEGIN line wherever that line appears, not by
// what encoding/pem manages to decode: a key block too damaged to decode, or
// indented so the decoder skips it, is still a key in the wrong field.
// Every block the decoder reads has such a line, so this is the stricter of
// the two readings.
func ClientCertificateProblem(text []byte) string {
	for rest := text; ; {
		at := bytes.Index(rest, pemBegin)
		if at < 0 {
			return ""
		}
		rest = rest[at+len(pemBegin):]
		label, _, closed := bytes.Cut(rest, pemDashes)
		if !closed || string(label) != "CERTIFICATE" {
			return "holds a PEM block other than CERTIFICATE, such as a private key, and may hold certificates only"
		}
	}
}
