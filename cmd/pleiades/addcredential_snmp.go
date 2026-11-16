// Package main: add-credential's SNMP flags, which store a device's SNMP
// family (a community, or an SNMPv3 user with its protocols and
// passphrases) apart from its login, and the one reader of standard input
// every secret add-credential reads shares.
package main

import (
	"errors"
	"flag"
	"fmt"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/prompt"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// loginFlags are add-credential's flags that name a login: given any of
// them, the login family is saved.
var loginFlags = []string{
	"username", "password", "password-stdin", "password-file", "key", "certificate", "pfx",
	"passphrase", "passphrase-stdin", "generate", "replace", "token-stdin", "enable-password", "enable-password-stdin",
}

// snmpFlags are add-credential's SNMP flags. No SNMP secret is ever a flag
// value: each is prompted for with no echo, or read from standard input
// when a -stdin flag asks for it.
type snmpFlags struct {
	fs              *flag.FlagSet
	promptCommunity *bool
	stdinCommunity  *bool
	allowShort      *bool
	user            *string
	auth            *string
	priv            *string
	stdinPassphrase *bool
}

// addSNMPFlags registers the SNMP flags on fs.
func addSNMPFlags(fs *flag.FlagSet) *snmpFlags {
	return &snmpFlags{
		fs:              fs,
		promptCommunity: fs.Bool("snmp-community", false, "prompt for an SNMPv1/v2c community, stored apart from the login"),
		stdinCommunity:  fs.Bool("snmp-community-stdin", false, "read an SNMPv1/v2c community as one line on standard input"),
		allowShort: fs.Bool("allow-short-community", false, fmt.Sprintf("store a community shorter than %d characters, which every run then masks by shape only (the configuration lines that hold it), never by value, and warns about",
			wire.MinSNMPCommunityLength)),
		user:            fs.String("snmp-user", "", "the SNMPv3 USM user name, which --snmp-auth and --snmp-priv belong to; alone it is a noAuthNoPriv user"),
		auth:            fs.String("snmp-auth", "", "the SNMPv3 authentication protocol, its passphrase prompted for: "+protocolList(wire.SNMPAuthProtocols())),
		priv:            fs.String("snmp-priv", "", "the SNMPv3 privacy protocol, its passphrase prompted for after the authentication one: "+protocolList(wire.SNMPPrivProtocols())),
		stdinPassphrase: fs.Bool("snmp-passphrase-stdin", false, "read the SNMPv3 passphrases from standard input instead, one line each, the authentication one first"),
	}
}

// protocolList names a protocol vocabulary as a comma list.
func protocolList[P ~string](protocols []P) string {
	names := make([]string, 0, len(protocols))
	for _, p := range protocols {
		names = append(names, string(p))
	}
	return strings.Join(names, ", ")
}

// familiesGiven reports which families fs's set flags name: the login for any
// login flag, SNMP for any SNMP flag, and the login when neither is given,
// which is add-credential's prompt for a password.
func familiesGiven(fs *flag.FlagSet) credential.Families {
	var families credential.Families
	fs.Visit(func(f *flag.Flag) {
		for _, name := range loginFlags {
			if f.Name == name {
				families |= credential.LoginFamily
			}
		}
		if strings.HasPrefix(f.Name, "snmp-") || f.Name == "allow-short-community" {
			families |= credential.SNMPFamily
		}
	})
	if families == 0 {
		families = credential.LoginFamily
	}
	return families
}

// community reports whether a community is asked for, either way.
func (s *snmpFlags) community() bool { return *s.promptCommunity || *s.stdinCommunity }

// stdinLines is how many lines of standard input the SNMP flags read.
func (s *snmpFlags) stdinLines() int {
	n := 0
	if *s.stdinCommunity {
		n++
	}
	if *s.stdinPassphrase {
		n++
		if *s.priv != "" {
			n++
		}
	}
	return n
}

// check refuses a combination of SNMP flags before any secret is read, so a
// refusal consumes no input. The user and the protocols are held to
// wire.SNMPCredential.Check, the rule the store applies, with placeholders
// standing in for passphrases not read yet.
func (s *snmpFlags) check() error {
	v3 := *s.user != "" || *s.auth != "" || *s.priv != "" || *s.stdinPassphrase
	switch {
	case *s.promptCommunity && *s.stdinCommunity:
		return errors.New("--snmp-community and --snmp-community-stdin are mutually exclusive: a community is read one way")
	case s.community() && v3:
		return errors.New("a community is SNMPv1 and v2c's and --snmp-user is SNMPv3's: a credential holds one or the other, since a client could not tell which version the device answers")
	case *s.allowShort && !s.community():
		return errors.New("--allow-short-community applies only to --snmp-community or --snmp-community-stdin")
	case *s.stdinPassphrase && *s.auth == "":
		return errors.New("--snmp-passphrase-stdin applies only to --snmp-auth, and --snmp-priv with it")
	}
	if !v3 {
		return nil
	}
	placeholder := strings.Repeat("x", wire.MinSNMPPassphraseLength)
	preflight := wire.SNMPCredential{User: *s.user, AuthProtocol: wire.SNMPAuthProtocol(*s.auth), PrivProtocol: wire.SNMPPrivProtocol(*s.priv)}
	if *s.auth != "" {
		preflight.AuthPassphrase = placeholder
	}
	if *s.priv != "" {
		preflight.PrivPassphrase = placeholder
	}
	return snmpFlagError(preflight.Check(false))
}

// read reads the SNMP family's secrets, from in when a -stdin flag asks and
// otherwise by a prompt with no echo, onto cred, and holds the result to
// wire.SNMPCredential.Check in the flags' own words.
func (s *snmpFlags) read(in *stdinQueue, cred credential.Credential) (credential.Credential, error) {
	snmp := wire.SNMPCredential{User: *s.user, AuthProtocol: wire.SNMPAuthProtocol(*s.auth), PrivProtocol: wire.SNMPPrivProtocol(*s.priv)}
	var err error
	switch {
	case *s.stdinCommunity:
		snmp.Community, err = in.next()
	case *s.promptCommunity:
		snmp.Community, err = promptSecret("SNMP community: ")
	}
	if err != nil {
		return cred, err
	}
	for _, p := range []struct {
		protocol, what string
		into           *string
	}{
		{*s.auth, "SNMP authentication passphrase", &snmp.AuthPassphrase},
		{*s.priv, "SNMP privacy passphrase", &snmp.PrivPassphrase},
	} {
		if p.protocol == "" {
			continue
		}
		if *s.stdinPassphrase {
			*p.into, err = in.next()
		} else {
			*p.into, err = promptSecret(p.what + ": ")
		}
		if err != nil {
			return cred, err
		}
	}
	if err := snmpFlagError(snmp.Check(*s.allowShort)); err != nil {
		return cred, err
	}
	return cred.WithSNMP(snmp), nil
}

// snmpFlagError rewords an *wire.SNMPProblem in add-credential's own terms:
// the flag that set the field, or the secret by what it is, and for a short
// community the flag that allows one. Any other error is returned as it is.
func snmpFlagError(err error) error {
	var problem *wire.SNMPProblem
	if !errors.As(err, &problem) {
		return err
	}
	subject := map[string]string{
		wire.SecretSNMPCommunity:      "the SNMP community",
		wire.SecretSNMPUser:           "--snmp-user",
		wire.SecretSNMPAuthProtocol:   "--snmp-auth",
		wire.SecretSNMPAuthPassphrase: "the SNMP authentication passphrase",
		wire.SecretSNMPPrivProtocol:   "--snmp-priv",
		wire.SecretSNMPPrivPassphrase: "the SNMP privacy passphrase",
	}[problem.Key]
	text := subject + " " + problem.Problem
	if problem.Key == wire.SecretSNMPCommunity && strings.HasPrefix(problem.Problem, "is shorter") {
		text += " (--allow-short-community)"
	}
	return errors.New(text)
}

// stdinQueue is add-credential's one reader of standard input: every secret
// any flag asks to read from it is one line, in a fixed order (the login's
// secret, the enable secret, then the SNMP community or passphrases), read
// by one reader at the first ask, since two readers of one pipe would lose a
// later line to the first one's buffer.
type stdinQueue struct {
	n     int
	lines []string
	read  bool
}

// next returns the next line of standard input.
func (q *stdinQueue) next() (string, error) {
	if !q.read {
		lines, err := prompt.SecretsFromStdin(q.n)
		if err != nil {
			return "", err
		}
		q.lines, q.read = lines, true
	}
	if len(q.lines) == 0 {
		// Unreachable while every reader is counted in n, and reported rather
		// than read from a second reader, which would lose a line.
		return "", errors.New("more secrets were asked of standard input than add-credential counted; this is a bug")
	}
	line := q.lines[0]
	q.lines = q.lines[1:]
	return line, nil
}

// familyNote is what a stored message adds for a save that replaced the SNMP
// family alone, and so kept the device's login.
func familyNote(opts credential.SaveOptions) string {
	if opts.Families == credential.SNMPFamily {
		return " (its SNMP fields; a login stored for it is kept)"
	}
	return ""
}

// shortCommunityNote is what a save of a short community prints to stderr:
// that every run will mask it by shape only.
func shortCommunityNote(name string, cred credential.Credential) string {
	if !cred.SNMP().ShortCommunity() {
		return ""
	}
	return fmt.Sprintf("WARNING: the SNMP community stored for %q is shorter than %d characters, so every run masks it only by its shape (the configuration lines that hold it), never by value, and says so: "+
		"output that shows it any other way, such as show snmp community, shows it in the clear",
		name, wire.MinSNMPCommunityLength)
}
