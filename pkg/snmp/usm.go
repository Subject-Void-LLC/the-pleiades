// Package snmp: an SNMPv3 credential as gosnmp's User-based Security Model
// parameters, by a closed table from each stored protocol name to gosnmp's
// own, so no name is ever guessed at.
package snmp

import (
	"fmt"

	"github.com/gosnmp/gosnmp"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// authProtocols maps each stored authentication protocol to gosnmp's.
var authProtocols = map[wire.SNMPAuthProtocol]gosnmp.SnmpV3AuthProtocol{
	wire.SNMPAuthMD5:    gosnmp.MD5,
	wire.SNMPAuthSHA:    gosnmp.SHA,
	wire.SNMPAuthSHA224: gosnmp.SHA224,
	wire.SNMPAuthSHA256: gosnmp.SHA256,
	wire.SNMPAuthSHA384: gosnmp.SHA384,
	wire.SNMPAuthSHA512: gosnmp.SHA512,
}

// privProtocols maps each stored privacy protocol to gosnmp's. AES-192 and
// AES-256 are Blumenthal's key extension under their plain names and
// Reeder's under -reeder, gosnmp's AES192C and AES256C ("Cisco"). 3DES has
// no row: gosnmp does not implement it, and Settings.Check refuses it first.
var privProtocols = map[wire.SNMPPrivProtocol]gosnmp.SnmpV3PrivProtocol{
	wire.SNMPPrivDES:          gosnmp.DES,
	wire.SNMPPrivAES128:       gosnmp.AES,
	wire.SNMPPrivAES192:       gosnmp.AES192,
	wire.SNMPPrivAES256:       gosnmp.AES256,
	wire.SNMPPrivAES192Reeder: gosnmp.AES192C,
	wire.SNMPPrivAES256Reeder: gosnmp.AES256C,
}

// init turns off gosnmp's password cache, which is on by default: a map,
// global to the process and never emptied, keyed by every USM passphrase in
// the clear and holding the key derived from it. In a Runner or a Controller
// that would keep every device's passphrases in memory for the life of the
// process, after the task that was handed them ended, and grow with every
// credential it ever saw. Without it each client derives its keys afresh
// (RFC 3414's password-to-key, a megabyte of hashing per passphrase, a few
// milliseconds), once per client and not per request.
func init() { gosnmp.PasswordCaching(false) }

// usmParameters builds gosnmp's security parameters and message flags for
// cred's user at its security level.
func usmParameters(cred wire.SNMPCredential) (*gosnmp.UsmSecurityParameters, gosnmp.SnmpV3MsgFlags, error) {
	usm := &gosnmp.UsmSecurityParameters{UserName: cred.User, AuthenticationProtocol: gosnmp.NoAuth, PrivacyProtocol: gosnmp.NoPriv}
	flags := gosnmp.NoAuthNoPriv
	if cred.AuthProtocol != "" {
		auth, ok := authProtocols[cred.AuthProtocol]
		if !ok {
			return nil, 0, fmt.Errorf("authentication protocol %q is not one this client sends", cred.AuthProtocol)
		}
		usm.AuthenticationProtocol, usm.AuthenticationPassphrase = auth, cred.AuthPassphrase
		flags = gosnmp.AuthNoPriv
	}
	if cred.PrivProtocol != "" {
		priv, ok := privProtocols[cred.PrivProtocol]
		if !ok {
			if cred.PrivProtocol == wire.SNMPPriv3DES {
				return nil, 0, ErrUnsupportedPrivacy
			}
			return nil, 0, fmt.Errorf("privacy protocol %q is not one this client sends", cred.PrivProtocol)
		}
		usm.PrivacyProtocol, usm.PrivacyPassphrase = priv, cred.PrivPassphrase
		flags = gosnmp.AuthPriv
	}
	return usm, flags, nil
}
