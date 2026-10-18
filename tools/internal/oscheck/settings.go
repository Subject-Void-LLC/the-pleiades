// Where a check's settings come from (the environment, or a gitignored
// file in this worktree or the clone's main worktree), the private
// directory under .git it builds pleiades in, and the commit it tests.
package oscheck

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

// Settings is a check's settings and the file they came from.
type Settings struct {
	// Values holds each setting by name; one not set is "".
	Values map[string]string
	// File is the settings file that held them, or "" when the environment
	// did (or nothing named any).
	File string
}

// Load reads keys from the environment when it sets any of them, else from
// the first of envFiles(envFile) that exists, else nothing. One source
// gives every key, so a run never mixes an environment value with a file's.
func Load(envFile string, keys ...string) (Settings, error) {
	fromEnv := map[string]string{}
	set := false
	for _, k := range keys {
		fromEnv[k] = strings.TrimSpace(os.Getenv(k))
		set = set || fromEnv[k] != ""
	}
	if set {
		return Settings{Values: fromEnv}, nil
	}
	files, err := EnvFiles(envFile)
	if err != nil {
		return Settings{}, err
	}
	for _, path := range files {
		data, err := os.ReadFile(path) // #nosec G304 -- this repository's own settings file
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return Settings{}, err
		}
		values, err := ParseEnv(data, keys...)
		if err != nil {
			return Settings{}, fmt.Errorf("%s: %v", path, err)
		}
		return Settings{Values: values, File: path}, nil
	}
	return Settings{Values: map[string]string{}}, nil
}

// EnvFiles is where the settings file rel (relative to the repository's
// top level) may be, in order: this worktree's, then, in a linked
// worktree, the clone's main worktree's, so one file serves every worktree
// of a clone.
func EnvFiles(rel string) ([]string, error) {
	top, err := GitPath("--show-toplevel")
	if err != nil {
		return nil, fmt.Errorf("finding the repository's top level: %v", err)
	}
	files := []string{filepath.Join(top, rel)}
	common, err := GitPath("--git-common-dir")
	if err != nil {
		return nil, fmt.Errorf("finding the clone's git directory: %v", err)
	}
	if main := filepath.Dir(common); filepath.Base(common) == ".git" && main != top {
		files = append(files, filepath.Join(main, rel))
	}
	return files, nil
}

// ParseEnv reads KEY=VALUE lines, skipping blank lines and # comments, with
// a value optionally in matching quotes. A key other than keys is refused,
// so a misspelled one is not silently ignored.
func ParseEnv(data []byte, keys ...string) (map[string]string, error) {
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
		if !slices.Contains(keys, key) {
			if len(keys) == 1 {
				return nil, fmt.Errorf("line %d sets %s; the only setting is %s", n+1, key, keys[0])
			}
			return nil, fmt.Errorf("line %d sets %s; the settings are %s", n+1, key, strings.Join(keys, " and "))
		}
		if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
			value = value[1 : len(value)-1]
		}
		out[key] = value
	}
	return out, nil
}

// GitPath is the path git rev-parse prints for flag, absolute and with
// symbolic links resolved, so two spellings of one directory compare equal.
func GitPath(flag string) (string, error) {
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

// PrivateDir is the directory name under this worktree's own git
// directory, never committed and never shared with another worktree, so
// two worktrees running at once never build over each other's binary.
func PrivateDir(name string) (string, error) {
	dir, err := GitPath("--absolute-git-dir")
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name), nil
}

// ResolveCommit is commit's full hash, and whether the working tree holds
// changes it does not.
func ResolveCommit(commit string) (string, bool, error) {
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

// ExpandHome expands a leading ~/ the way a shell does.
func ExpandHome(p string) string {
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, rest)
		}
	}
	return p
}

// FileExists reports whether p is an existing regular file.
func FileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.Mode().IsRegular()
}

// FirstOf is the first non-empty of values.
func FirstOf(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// Short is a commit's first twelve hex digits.
func Short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}
