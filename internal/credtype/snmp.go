// The SNMP credential kind (pleiades_snmp): a community or an SNMPv3 user,
// read directly by the native path into the same keys a stored device
// credential flattens into, and given to the Ansible path not at all.
//
// No SNMP client exists yet; Phase 97 builds it and proves each protocol
// against a real agent. Until then this kind is stored, carried to a native
// Runner and masked there, and nothing sends it anywhere.
package credtype

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// The input ids pleiades_snmp declares. They are this platform's own, as the
// type is, and each maps onto one wire key (snmpInputKeys).
const (
	SNMPInputCommunity           = "community"
	SNMPInputAllowShortCommunity = "allow_short_community"
	SNMPInputUser                = "username"
	SNMPInputAuthProtocol        = "auth_protocol"
	SNMPInputAuthPassphrase      = "auth_passphrase"
	SNMPInputPrivProtocol        = "priv_protocol"
	SNMPInputPrivPassphrase      = "priv_passphrase"
)

// snmpInputKeys names the input each SNMP wire key is read from, so a
// refusal from wire.SNMPCredential.Check names the input an operator edits.
var snmpInputKeys = map[string]string{
	wire.SecretSNMPCommunity:      SNMPInputCommunity,
	wire.SecretSNMPUser:           SNMPInputUser,
	wire.SecretSNMPAuthProtocol:   SNMPInputAuthProtocol,
	wire.SecretSNMPAuthPassphrase: SNMPInputAuthPassphrase,
	wire.SecretSNMPPrivProtocol:   SNMPInputPrivProtocol,
	wire.SecretSNMPPrivPassphrase: SNMPInputPrivPassphrase,
}

// SNMPCredentialOf reads an SNMP kind credential's inputs into the wire's
// SNMP credential, held to wire.SNMPCredential.Check: the one reading of
// those inputs, which the injector and the store's write share. Every input
// is taken as written, as add-credential takes its flags: a user or
// protocol name with a space at either end is refused by Check, never
// trimmed, so the two paths read one alike.
func SNMPCredentialOf(cred Credential) (wire.SNMPCredential, error) {
	if cred.Type.Kind != KindSNMP {
		return wire.SNMPCredential{}, fmt.Errorf("%w: credential %q is a %s credential, not an SNMP one", ErrInjection, cred.Name, cred.Type.Kind)
	}
	in := func(id string) string { return cred.Inputs[id] }
	snmp := wire.SNMPCredential{
		Community:      in(SNMPInputCommunity),
		User:           in(SNMPInputUser),
		AuthProtocol:   wire.SNMPAuthProtocol(in(SNMPInputAuthProtocol)),
		AuthPassphrase: in(SNMPInputAuthPassphrase),
		PrivProtocol:   wire.SNMPPrivProtocol(in(SNMPInputPrivProtocol)),
		PrivPassphrase: in(SNMPInputPrivPassphrase),
	}
	if snmp.Empty() {
		return wire.SNMPCredential{}, fmt.Errorf("%w: credential %q: an SNMP credential needs a %s or a %s", ErrInvalidCredential, cred.Name, SNMPInputCommunity, SNMPInputUser)
	}
	// Lenient on the way in, as a boolean input's rendering is (RenderVars):
	// whatever spelling reached storage, an unparsable one allows nothing.
	allow, _ := strconv.ParseBool(strings.TrimSpace(in(SNMPInputAllowShortCommunity)))
	var problem *wire.SNMPProblem
	if err := snmp.Check(allow); errors.As(err, &problem) {
		text := problem.Problem
		if problem.Key == wire.SecretSNMPCommunity && snmp.ShortCommunity() && !allow {
			text += " (set " + SNMPInputAllowShortCommunity + ")"
		}
		return wire.SNMPCredential{}, fmt.Errorf("%w: credential %q: input %q %s", ErrInvalidCredential, cred.Name, snmpInputKeys[problem.Key], text)
	}
	return snmp, nil
}

// snmpTarget turns an SNMP kind's inputs into the artifact's one SNMP
// credential. Like kubernetesTarget it is code rather than data, and its
// injector document is empty: its output is a Go value the native path
// merges into a device's secrets (internal/dispatch.ApplySNMP), which no
// injector document can produce, and the Ansible path is given nothing.
type snmpTarget struct{}

// Key names the target.
func (snmpTarget) Key() string { return "snmp" }

// Phase runs the target with the other value targets.
func (snmpTarget) Phase() Phase { return PhaseValues }

// Apply maps an SNMP kind's inputs onto the artifact's SNMP credential
// (SNMPCredentialOf), refusing one wire.SNMPCredential.Check refuses.
func (snmpTarget) Apply(req Request, art *Artifact) error {
	if req.Credential.Type.Kind != KindSNMP {
		return nil
	}
	snmp, err := SNMPCredentialOf(req.Credential)
	if err != nil {
		return err
	}
	art.snmp = &snmp
	return nil
}

// SNMP returns the SNMP credential this artifact carries, and false when it
// carries none.
func (a Artifact) SNMP() (wire.SNMPCredential, bool) {
	if a.snmp == nil {
		return wire.SNMPCredential{}, false
	}
	return *a.snmp, true
}
