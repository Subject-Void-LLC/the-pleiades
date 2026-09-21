// Client certificate authentication for WinRM, and the credential
// vocabulary every caller builds an Auth from.
//
// # Why this is a separate transport rather than another option
//
// WinRM certificate authentication is not password authentication with a
// certificate attached. The WS-Man profile it uses
// ("http://schemas.dmtf.org/wbem/wsman/1/wsman/secprofile/https/mutual")
// sends no Basic authorization header and carries no username at all: the
// client proves possession of a private key during the TLS handshake, and
// the Windows side maps the presented certificate to a local account. So
// the account is named by the certificate, not by the request, and the two
// mechanisms are mutually exclusive rather than composable.
//
// # What the TARGET has to be configured with, which is not this platform
//
// This is a deployment cost worth knowing before a failed handshake gets
// read as a defect here. The Windows host needs an HTTPS WinRM listener,
// the issuing certificate authority in its trusted roots, and an explicit
// certificate-to-account mapping (New-Item -Path WSMan:\localhost\
// ClientCertificate). The client certificate must carry a UPN in its
// subject alternative name and Client Authentication in its extended key
// usage, or the mapping cannot match it. None of that is something this
// platform can do for an operator, and docs/10-running-in-production.md
// says so in the same words.
package winrmexec

import (
	"encoding/pem"
	"fmt"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/pfx"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// AuthFromSecrets builds an Auth from the flattened credential map a
// Collection method receives from sdk.RunbookContext.InjectSecrets.
//
// It exists so the key vocabulary is read in ONE place rather than at
// every call site. Before Phase 78d the three catalog packages that reach
// a Windows host each wrote `Auth{Username: secrets["username"], Password:
// secrets["password"]}` with the keys as literals, so adding a credential
// form meant editing all three and a fourth reader could silently disagree
// with them. pkg/remoteexec.AuthFromSecrets is the same function for SSH
// and the same argument; this is that pattern applied here.
//
// A map carrying neither credential form yields a zero Auth and no error.
// Reporting that here would be reporting it too early: whether a device
// needs a credential at all is the caller's question, and Validate is
// where an unusable one is refused, at the point of use.
func AuthFromSecrets(secrets map[string]string) (Auth, error) {
	auth := Auth{
		Username:       secrets[wire.SecretUsername],
		Password:       secrets[wire.SecretPassword],
		CertificatePEM: []byte(secrets[wire.SecretCertificatePEM]),
		PrivateKeyPEM:  []byte(secrets[wire.SecretPrivateKeyPEM]),
	}

	// The bundle is unlocked HERE, which is the point of shipping it sealed.
	//
	// This function runs inside the Runner's per-task child process, so the
	// PKCS#12 bundle and its passphrase are what crossed the message broker
	// and the unlocked private key exists only in this short-lived process.
	// PLAN.md Section 17.4 asks for exactly that, and doing it on the
	// Controller instead would put an unlocked key on a JetStream stream for
	// the whole of its retention window.
	if bundle := secrets[wire.SecretPFXBase64]; bundle != "" {
		// Every other way of supplying an identity is refused alongside a
		// bundle rather than quietly losing to it. A credential carrying two
		// is a credential nobody can predict the behaviour of, and picking one
		// would mean a run authenticating as something the operator did not
		// choose.
		//
		// Note the passphrase is NOT one of these: it arrives under its own
		// key and is what opens the bundle, so a bundle and a passphrase is
		// the ordinary case rather than a conflict.
		if len(auth.CertificatePEM) != 0 || len(auth.PrivateKeyPEM) != 0 {
			return Auth{}, fmt.Errorf("winrm: credential carries a PKCS#12 bundle and a separate certificate or " +
				"private key, and those are two ways to supply one identity rather than a pair of fallbacks")
		}
		if auth.usesPassword() {
			return Auth{}, fmt.Errorf("winrm: credential carries a PKCS#12 bundle and a username or password, and " +
				"those are different authentication mechanisms rather than a fallback pair")
		}
		certPEM, keyPEM, err := pfx.Decode(bundle, secrets[wire.SecretPassphrase])
		if err != nil {
			return Auth{}, fmt.Errorf("winrm: %w", err)
		}
		auth.CertificatePEM = certPEM
		auth.PrivateKeyPEM = keyPEM
	}
	// An absent key converts to a zero-length slice rather than nil, and
	// every check below asks len(...) != 0, so normalizing is not needed
	// for correctness. It is done anyway so a caller comparing an Auth it
	// built here against one it built literally is not surprised.
	if len(auth.CertificatePEM) == 0 {
		auth.CertificatePEM = nil
	}
	if len(auth.PrivateKeyPEM) == 0 {
		auth.PrivateKeyPEM = nil
	}
	return auth, nil
}

// usesCertificate reports whether this credential authenticates with a
// client certificate.
//
// It asks about the certificate alone rather than about the complete pair,
// so that a credential carrying a certificate and no key is routed to the
// certificate path and refused there with an error naming the missing
// half. Asking for both here would instead route it to the password path,
// where it would be refused for having no password, which is true and
// useless.
func (a Auth) usesCertificate() bool { return len(a.CertificatePEM) != 0 }

// usesPassword reports whether this credential authenticates with a
// username and password.
func (a Auth) usesPassword() bool { return a.Username != "" || a.Password != "" }

// Validate refuses any credential that is not exactly one complete form.
//
// The guard this replaced asked only whether a password was present, which
// was correct while NTLM was the only mechanism and became wrong the
// moment certificate authentication landed: it refused a valid certificate
// credential before anything was attempted, and its error text explained
// that a key is not a usable credential, which had stopped being true.
//
// Each refusal is separate on purpose. "You supplied nothing", "you
// supplied half a certificate" and "you supplied both forms" send an
// operator to three different places, and collapsing them into one message
// about needing a username and a password sends all three to the wrong
// one.
func (a Auth) Validate() error {
	switch {
	case a.usesCertificate() && a.usesPassword():
		return fmt.Errorf("winrm: credential carries both a client certificate and a password, and the two are " +
			"different authentication mechanisms rather than a fallback pair: certificate authentication sends no " +
			"password and lets the target map the certificate to an account, so nothing here can decide which one you meant")

	case a.usesCertificate():
		if len(a.PrivateKeyPEM) == 0 {
			return fmt.Errorf("winrm: credential has a client certificate but no private key, and a certificate " +
				"alone cannot complete a TLS handshake: supply the matching key")
		}
		if encryptedPrivateKey(a.PrivateKeyPEM) {
			// Refused rather than attempted, because the alternative is worse
			// than a refusal. crypto/tls cannot decrypt a private key, so the
			// passphrase this credential carries would be read, sent across
			// the broker and then silently discarded, and the run would fail
			// at client construction with the library's own "failed to find
			// any PEM data in key input" naming neither the credential nor the
			// passphrase that was ignored.
			return fmt.Errorf("winrm: the client certificate's private key is passphrase protected, and this " +
				"platform does not decrypt a loose private key: supply the key unencrypted, or supply the " +
				"certificate and key together as a PKCS#12 bundle, which IS unlocked with its passphrase at " +
				"the point of use")
		}
		return nil

	case a.usesPassword():
		if a.Username == "" {
			return fmt.Errorf("winrm: credential has a password but no username")
		}
		if a.Password == "" {
			return fmt.Errorf("winrm: credential has a username but no password: WinRM password authentication " +
				"speaks NTLM, which has no key-only form, so authenticate with a client certificate instead if " +
				"that is what you have")
		}
		return nil

	default:
		if len(a.PrivateKeyPEM) != 0 {
			return fmt.Errorf("winrm: credential has a private key but no client certificate: WinRM has no " +
				"key-only authentication, so the key is usable only as the other half of a certificate")
		}
		return fmt.Errorf("winrm: credential is empty: supply a username and password, or a client certificate and its private key")
	}
}

// resolve returns the options a run actually uses, given the credential it
// will use them with.
//
// Certificate authentication implies HTTPS rather than requiring the
// caller to ask for it, because there is no other possibility: the
// transport sends no Basic authorization header, so over plain HTTP it
// would present no credential whatsoever and the request would be refused
// by the service for being anonymous. Making the caller set a flag whose
// only correct value is true would be a way to get it wrong, not a choice.
//
// It returns a copy. Mutating the caller's Options would make a retry
// behave differently from the first attempt.
func (o Options) resolve(auth Auth) Options {
	if auth.usesCertificate() {
		o.HTTPS = true
	}
	return o
}

// checkCertificatePort refuses certificate authentication aimed at the
// cleartext WinRM listener.
//
// This is a real case rather than a defensive one, and it is why the
// refusal is written out. A Windows device's port here defaults to 5985,
// the listener Enable-PSRemoting creates, and that default is
// indistinguishable from an operator choosing 5985. So the ordinary way to
// arrive at certificate authentication is against a device nobody has
// repointed, where a TLS handshake would be attempted against a plain HTTP
// listener. That fails with a transport error about a malformed record,
// which reads like a broken certificate and sends whoever gets it to
// inspect the one thing that is fine.
func checkCertificatePort(port int) error {
	if port == DefaultPort {
		return fmt.Errorf(
			"winrm: certificate authentication needs the HTTPS listener but this device is set to port %d, the "+
				"cleartext one: set the device's port property to %d (the port a Windows HTTPS listener uses)",
			DefaultPort, DefaultPortHTTPS)
	}
	return nil
}

// encryptedPrivateKey reports whether a PEM private key is passphrase
// protected, in either of the two ways one can be.
//
// PKCS#8 says so in the block type ("ENCRYPTED PRIVATE KEY"). The older
// OpenSSL form says so in a Proc-Type header on an otherwise ordinary
// block, which is why the headers are checked as well as the type: a
// legacy encrypted key is labelled "RSA PRIVATE KEY" exactly like an
// unencrypted one, so the type alone would miss it.
//
// Only the first block is examined. A file whose first block is the key is
// the shape every tool produces, and a caller who concatenated something
// unusual gets the ordinary parse failure from crypto/tls rather than a
// second opinion from here.
func encryptedPrivateKey(keyPEM []byte) bool {
	block, _ := pem.Decode(keyPEM)
	if block == nil {
		// Not PEM at all. Not this function's question: the keypair check
		// in the transport reports it, with the library's own message.
		return false
	}
	if strings.Contains(block.Type, "ENCRYPTED") {
		return true
	}
	if procType, ok := block.Headers["Proc-Type"]; ok && strings.Contains(procType, "ENCRYPTED") {
		return true
	}
	return false
}
