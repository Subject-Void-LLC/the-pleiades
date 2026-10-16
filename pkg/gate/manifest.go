// Package gate is the core of a gate run: a manifest of named checks, the
// rules a manifest must meet before anything runs, which checks a run
// selects, a scheduler that runs them with bounded parallelism, and the
// typed result each one yields. It knows nothing of where a check runs:
// the gate Collection (gate.run, gate.list) supplies a runner for a device,
// and tools/testgate shares its report helpers (the skip ledger and the
// bounded tail).
package gate

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"time"

	"go.yaml.in/yaml/v3"
)

// MaxManifestBytes bounds a manifest file. A gate manifest lists a
// project's checks; one past this is not a manifest anyone wrote by hand.
const MaxManifestBytes = 1 << 20

// MaxChecks bounds how many checks one manifest declares.
const MaxChecks = 500

// MaxParallelism bounds how many checks run at once, whatever a manifest
// or task asks for.
const MaxParallelism = 16

// Built-in kinds. A manifest may declare more (Manifest.Kinds).
const (
	KindPure    = "pure"
	KindDB      = "db"
	KindNetwork = "network"
	KindModel   = "model"
)

// BuiltinKinds is every kind a manifest may use without declaring it.
func BuiltinKinds() []string { return []string{KindDB, KindModel, KindNetwork, KindPure} }

// Manifest is a gate manifest as written.
type Manifest struct {
	// Version is the manifest format's version; 1 is the only one.
	Version int `yaml:"version"`
	// Kinds declares kinds beyond the built-in ones.
	Kinds []KindDecl `yaml:"kinds"`
	// Defaults apply to every check that does not set its own.
	Defaults Defaults `yaml:"defaults"`
	// Checks are the named checks, in the order a run starts them.
	Checks []Check `yaml:"checks"`
}

// KindDecl declares one kind beyond the built-in ones.
type KindDecl struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

// Defaults are the manifest-wide settings a check may override.
type Defaults struct {
	// Timeout bounds a check that sets none of its own. A check must have
	// one or the other: there is no implicit timeout.
	Timeout Duration `yaml:"timeout"`
	// Parallelism is how many checks run at once, 1 when unset.
	Parallelism int `yaml:"parallelism"`
}

// Check is one named check.
type Check struct {
	// Name identifies the check in selection and results.
	Name string `yaml:"name"`
	// Kind classifies it (a built-in kind or a declared one); required.
	Kind string `yaml:"kind"`
	// Command is the check as an argument vector, quoted by the runner.
	Command []string `yaml:"command"`
	// Shell is the check as one shell string, run by sh -c. Exactly one
	// of Command and Shell is set.
	Shell string `yaml:"shell"`
	// Dir is where the check runs, relative to the manifest's directory;
	// that directory itself when empty. Not with Image.
	Dir string `yaml:"dir"`
	// Image runs the check in a fresh container from this digest-pinned
	// image instead of in Dir.
	Image string `yaml:"image"`
	// Network names the network an Image check joins; none when empty.
	Network string `yaml:"network"`
	// Writable gives an Image check a writable root filesystem.
	Writable bool `yaml:"writable"`
	// Pull lets the runner pull an absent Image.
	Pull bool `yaml:"pull"`
	// Heavy checks run one at a time.
	Heavy bool `yaml:"heavy"`
	// Serial checks run with nothing else running.
	Serial bool `yaml:"serial"`
	// Timeout bounds this check; Defaults.Timeout when zero.
	Timeout Duration `yaml:"timeout"`
	// SkipExit lists the exit codes this check uses to mean "skipped".
	SkipExit []int `yaml:"skip_exit"`
	// Requires are preconditions tested before the check starts.
	Requires Requires `yaml:"requires"`
}

// Requires are a check's preconditions. An unmet one skips the check,
// naming it, rather than running it to fail.
type Requires struct {
	// Commands must be on the PATH of the shell the check runs in.
	Commands []string `yaml:"commands"`
	// Env names variables that must be set, by name; a value is never read.
	Env []string `yaml:"env"`
	// Files must exist, relative to the check's directory.
	Files []string `yaml:"files"`
	// OS is the operating system the device must run: linux, darwin or
	// freebsd.
	OS string `yaml:"os"`
	// Engine requires the device's container engine to answer.
	Engine bool `yaml:"engine"`
	// TCP lists host:port endpoints that must accept a connection from the
	// device.
	TCP []string `yaml:"tcp"`
}

// Empty reports whether r states no precondition.
func (r Requires) Empty() bool {
	return len(r.Commands) == 0 && len(r.Env) == 0 && len(r.Files) == 0 && r.OS == "" && !r.Engine && len(r.TCP) == 0
}

// TimeoutOf is the timeout check c runs under in m: its own, or the
// manifest's default.
func (m *Manifest) TimeoutOf(c Check) time.Duration {
	if c.Timeout > 0 {
		return time.Duration(c.Timeout)
	}
	return time.Duration(m.Defaults.Timeout)
}

// Duration is a time.Duration written in a manifest as Go writes one
// ("90s", "30m", "1h30m").
type Duration time.Duration

// UnmarshalYAML reads a duration string.
func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	var text string
	if err := node.Decode(&text); err != nil {
		return fmt.Errorf("line %d: a duration is written like 90s or 30m", node.Line)
	}
	parsed, err := time.ParseDuration(text)
	if err != nil {
		return fmt.Errorf("line %d: %q is not a duration (write it like 90s or 30m)", node.Line, text)
	}
	*d = Duration(parsed)
	return nil
}

// Parse decodes a manifest strictly and validates it (Validate). A field
// the format does not have, a key written twice, a second YAML document
// and a manifest past MaxManifestBytes are each refused.
func Parse(data []byte) (*Manifest, error) {
	if len(data) > MaxManifestBytes {
		return nil, fmt.Errorf("the manifest is %d bytes, over the %d a manifest may have", len(data), MaxManifestBytes)
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var m Manifest
	if err := dec.Decode(&m); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("the manifest is empty")
		}
		return nil, fmt.Errorf("the manifest is not a valid gate manifest: %w", err)
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("the manifest holds more than one YAML document; a gate manifest is one")
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}
