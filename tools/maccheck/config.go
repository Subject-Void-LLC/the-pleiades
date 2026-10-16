// Where the Mac and this run's settings come from: PLEIADES_MAC, the
// private project's place under .git, and the commit to test.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// expandHome expands a leading ~/ the way ssh does.
func expandHome(p string) string {
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, rest)
		}
	}
	return p
}

// fileExists reports whether p is an existing regular file.
func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.Mode().IsRegular()
}

// firstOf is the first non-empty of values.
func firstOf(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// configured is the Mac and the settings file that named it: envKey from
// the environment (file ""), else from the first envFile that exists (see
// envFiles), else "" when none names one.
func configured() (mac, file string, err error) {
	if v := strings.TrimSpace(os.Getenv(envKey)); v != "" {
		return v, "", nil
	}
	files, err := envFiles()
	if err != nil {
		return "", "", err
	}
	for _, path := range files {
		data, err := os.ReadFile(path) // #nosec G304 -- this repository's own settings file
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", "", err
		}
		settings, err := parseEnv(data)
		if err != nil {
			return "", "", fmt.Errorf("%s: %v", path, err)
		}
		return settings[envKey], path, nil
	}
	return "", "", nil
}

// envFiles is where envFile may be, in order: this worktree's top level,
// then, in a linked worktree, the clone's main worktree's, so one file
// names the Mac for every worktree of a clone.
func envFiles() ([]string, error) {
	top, err := gitPath("--show-toplevel")
	if err != nil {
		return nil, fmt.Errorf("finding the repository's top level: %v", err)
	}
	files := []string{filepath.Join(top, envFile)}
	common, err := gitPath("--git-common-dir")
	if err != nil {
		return nil, fmt.Errorf("finding the clone's git directory: %v", err)
	}
	if main := filepath.Dir(common); filepath.Base(common) == ".git" && main != top {
		files = append(files, filepath.Join(main, envFile))
	}
	return files, nil
}

// gitPath is the path git rev-parse prints for flag, absolute and with
// symbolic links resolved, so two spellings of one directory compare equal.
func gitPath(flag string) (string, error) {
	out, err := exec.Command("git", "rev-parse", flag).Output() // #nosec G204 -- a fixed rev-parse flag
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(strings.TrimSpace(string(out)))
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

// parseEnv reads KEY=VALUE lines, skipping blank lines and # comments, with
// a value optionally in matching quotes. A key other than envKey is
// refused, so a misspelled one is not silently ignored.
func parseEnv(data []byte) (map[string]string, error) {
	out := map[string]string{}
	for n, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if !ok {
			return nil, fmt.Errorf("line %d is not KEY=VALUE", n+1)
		}
		if key != envKey {
			return nil, fmt.Errorf("line %d sets %s; the only setting is %s", n+1, key, envKey)
		}
		if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
			value = value[1 : len(value)-1]
		}
		out[key] = value
	}
	return out, nil
}

// privateProject is the private Pleiades project's directory, under this
// worktree's own git directory, so two worktrees running at once never
// build over each other's binary or share a project.
func privateProject() (string, error) {
	dir, err := gitPath("--absolute-git-dir")
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "pleiades-maccheck"), nil
}

// resolveCommit is commit's full hash, and whether the working tree holds
// changes it does not.
func resolveCommit(commit string) (string, bool, error) {
	out, err := exec.Command("git", "rev-parse", "--verify", commit+"^{commit}").Output() // #nosec G204 -- a revision the developer named
	if err != nil {
		return "", false, fmt.Errorf("%q is not a commit: %v", commit, err)
	}
	status, err := exec.Command("git", "status", "--porcelain").Output()
	if err != nil {
		return "", false, err
	}
	return strings.TrimSpace(string(out)), len(bytes.TrimSpace(status)) > 0, nil
}
