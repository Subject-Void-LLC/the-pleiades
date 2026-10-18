// Where the Mac comes from (PLEIADES_MAC) and the private project's place
// under .git.
package main

import (
	"github.com/Subject-Void-LLC/the-pleiades/tools/internal/oscheck"
)

// configured is the Mac and the settings file that named it: envKey from
// the environment (file ""), else from the first envFile that exists (see
// oscheck.EnvFiles), else "" when none names one.
func configured() (mac, file string, err error) {
	s, err := oscheck.Load(envFile, envKey)
	if err != nil {
		return "", "", err
	}
	return s.Values[envKey], s.File, nil
}

// privateProject is the private Pleiades project's directory, under this
// worktree's own git directory, so two worktrees running at once never
// build over each other's binary or share a project.
func privateProject() (string, error) {
	return oscheck.PrivateDir("pleiades-maccheck")
}
