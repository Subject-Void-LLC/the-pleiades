// What a credential's stored values are held to at the write, beyond its
// type's schema: what its kind reads from them, checked where they are saved
// rather than first at a launch.
package credtype

import (
	"fmt"
	"slices"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// snmpInputs are the inputs SNMPCredentialOf reads.
var snmpInputs = []string{
	SNMPInputCommunity, SNMPInputAllowShortCommunity, SNMPInputUser,
	SNMPInputAuthProtocol, SNMPInputAuthPassphrase, SNMPInputPrivProtocol, SNMPInputPrivPassphrase,
}

// CheckStoredValues refuses a credential's stored values that its kind could
// never use, at the write, the way CheckValues refuses what its schema does:
// an SNMP credential wire.SNMPCredential.Check refuses, and a client
// certificate input (a Kubernetes credential's client_certificate, a
// cryptography credential's certificate) holding a PEM block that is not a
// certificate, the shape a pasted "cert and key" bundle takes. The same rules
// run again at injection, since a value can also arrive later.
//
// unresolved names the inputs this write does not hold a value for, because
// an external secret source or a source credential supplies them at
// dispatch. An SNMP credential reading one of those is left to injection,
// where its whole value is known: its rules relate its fields to each other,
// so half of them cannot be judged alone. Like CheckValues, a refusal never
// repeats a value.
func CheckStoredValues(kind Kind, name string, values map[string]string, unresolved ...string) error {
	switch kind {
	case KindSNMP:
		for _, id := range snmpInputs {
			if slices.Contains(unresolved, id) {
				return nil
			}
		}
		_, err := SNMPCredentialOf(Credential{Name: name, Type: CredentialType{Kind: KindSNMP}, Inputs: values})
		return err
	case KindKubernetes:
		return certificateInputProblem(name, values, KubernetesInputClientCert, KubernetesInputClientKey)
	case KindCryptography:
		return certificateInputProblem(name, values, CertificateInputCertificate, CertificateInputPrivateKey)
	}
	return nil
}

// certificateInputProblem refuses a client certificate input holding a PEM
// block that is not a certificate (wire.ClientCertificateProblem), naming the
// input the key goes in instead. A missing or empty input is not its
// question.
func certificateInputProblem(name string, values map[string]string, certificateInput, keyInput string) error {
	if problem := wire.ClientCertificateProblem([]byte(values[certificateInput])); problem != "" {
		return fmt.Errorf("%w: credential %q: input %q %s: the private key goes in input %q, never beside the certificate",
			ErrInvalidCredential, name, certificateInput, problem, keyInput)
	}
	return nil
}
