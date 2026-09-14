// Package project_test's credential-shape coverage: which credentials may
// authenticate a clone, and which only look as though they can.
package project_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	"github.com/Subject-Void-LLC/the-pleiades/internal/project"
)

// TestAuthenticatesGit is the rule a project's credential chooser and both
// of its write paths share.
//
// The kind check is the one that matters and the table leads with it: a
// project sync must not be reachable with a credential issued for something
// else, whatever that credential happens to carry.
func TestAuthenticatesGit(t *testing.T) {
	const marker = "$encrypted$"

	for _, tc := range []struct {
		name     string
		kind     credtype.Kind
		inputs   map[string]string
		external map[string]string
		want     bool
	}{
		// The shapes a Source Control credential legitimately takes.
		{"scm with a token", credtype.KindSCM,
			map[string]string{project.InputPassword: marker}, nil, true},
		{"scm with a username and password", credtype.KindSCM,
			map[string]string{project.InputUsername: "someone", project.InputPassword: marker}, nil, true},
		{"scm with an ssh key", credtype.KindSCM,
			map[string]string{project.InputPrivateKey: marker}, nil, true},
		{"scm with an ssh key and passphrase", credtype.KindSCM,
			map[string]string{project.InputPrivateKey: marker, project.InputPassphrase: marker}, nil, true},

		// An externally sourced secret is supplied, and is absent from
		// Inputs entirely: credstore only lists inputs it has a stored
		// value for, so consulting that alone would refuse every
		// credential whose password lives in a secret manager.
		{"scm with a password from a secret manager", credtype.KindSCM,
			nil, map[string]string{project.InputPassword: "vault://kv/data/git#token"}, true},
		{"scm with a key from a secret manager", credtype.KindSCM,
			map[string]string{project.InputUsername: "git"},
			map[string]string{project.InputPrivateKey: "vault://kv/data/git#key"}, true},

		// The whole point of the kind check. Each of these carries
		// material that WOULD work, and none of them may be used: a
		// Machine credential opens shells on managed devices, a cloud one
		// spends money, and neither becomes a source-control credential by
		// declaring a field with a familiar name.
		{"a machine credential with a password", credtype.KindSSH,
			map[string]string{project.InputPassword: marker}, nil, false},
		{"a machine credential with an ssh key", credtype.KindSSH,
			map[string]string{project.InputPrivateKey: marker}, nil, false},
		{"a cloud credential declaring a password", credtype.KindCloud,
			map[string]string{project.InputPassword: marker}, nil, false},
		{"a vault credential", credtype.KindVault,
			map[string]string{project.InputPassword: marker}, nil, false},
		{"no kind at all", "",
			map[string]string{project.InputPassword: marker}, nil, false},

		// The right kind carrying nothing usable. Git will attempt a clone
		// with a bare username and fail on the far side with a message
		// that never says the credential was empty.
		{"scm with a username alone", credtype.KindSCM,
			map[string]string{project.InputUsername: "someone"}, nil, false},
		{"scm with nothing", credtype.KindSCM, map[string]string{}, nil, false},
		{"scm with nil maps", credtype.KindSCM, nil, nil, false},

		// Present but empty is not supplied. A stored secret reads back as
		// a marker, so an empty string means the input is absent rather
		// than blank.
		{"scm with an empty password", credtype.KindSCM,
			map[string]string{project.InputPassword: ""}, nil, false},
		{"scm with an empty external reference", credtype.KindSCM,
			nil, map[string]string{project.InputPassword: ""}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := project.AuthenticatesGit(tc.kind, tc.inputs, tc.external)
			if got != tc.want {
				t.Errorf("AuthenticatesGit(%q, %v, %v) = %v, want %v",
					tc.kind, tc.inputs, tc.external, got, tc.want)
			}
		})
	}
}
