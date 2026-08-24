// Package netcli is the vendor-conventions layer over
// pkg/remoteexec.Shell: which vendor's prompt, paging, configuration-mode
// and error conventions a session uses is data (a Dialect value), not a
// type switch, matching internal/engine/action_ssh.go's own "data, not a
// type switch" reasoning for transport selection. Nothing in this
// package dials SSH itself; a Session is built directly on an already
// open *remoteexec.Shell, the same primitive Run/RunWithStdin use for a
// one-shot exec, so there is no second SSH implementation here to earn
// its cost against.
//
// This package's own prompt handling exists because a real Cisco IOS XE
// device (verified directly against the DevNet Catalyst 8000 Always-On
// sandbox, not assumed) sends its prompt exactly once per command as
// long as the client submits each line with a single "\r" -- see
// remoteexec.Shell.WriteLine's own doc comment for the "\r\n" mistake
// that made it look, briefly, like the device was printing every prompt
// twice.
package netcli

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
)

// Dialect describes one vendor's CLI conventions: how to recognize its
// prompt in either mode, how to silence its pager, how to enter and
// leave configuration mode, and how to recognize a rejected line.
type Dialect struct {
	// Name identifies this dialect in error messages.
	Name string

	// Prompt returns the pattern that marks the end of one command's
	// output: the exec-mode prompt when configMode is false, or the
	// configuration-mode prompt (any sub-mode: "(config)#",
	// "(config-if)#", "(config-router)#", and so on) when true. A nil
	// Prompt means this Dialect cannot bound a read at all; Open
	// refuses rather than guessing.
	Prompt func(configMode bool) *regexp.Regexp

	// DisablePaging is the command Open sends once, immediately after
	// reading the first prompt, to stop a pager from interrupting a
	// long response. Empty means this Dialect names no such command,
	// and Open sends nothing.
	DisablePaging string

	// EnterConfigMode and ExitConfigMode are the commands Config sends
	// to bracket a batch of configuration lines. Both empty means this
	// Dialect has no configuration-mode convention to drive; Config
	// refuses outright rather than guessing at one.
	EnterConfigMode string
	ExitConfigMode  string

	// ErrorPattern matches one line of this vendor's own convention for
	// reporting a rejected command. Nil means no convention is shared
	// across every device this Dialect could describe, and Config never
	// treats any line as a rejection.
	ErrorPattern *regexp.Regexp
}

var (
	// iosExecPrompt and iosConfigPrompt are compiled once rather than
	// inside IOS's own Prompt closure, so a Session that calls Command
	// many times in a batch is not recompiling the same pattern on every
	// line.
	//
	// Both require a real line start ((?m)^) rather than matching
	// "hostname#" as a bare substring anywhere in the buffer: a
	// coincidental occurrence of that exact text in the middle of a
	// command's own real output (unlikely for IOS's actual conventions,
	// checked directly against a real "show version" and "show
	// running-config" response, but not provably impossible) would
	// otherwise end a read early. iosConfigPrompt's "\([^)]*\)" accepts
	// any parenthesized sub-mode name, not just the literal word
	// "config": IOS reports "(config-if)#", "(config-router)#" and
	// others depending on what the last configuration line entered, and
	// a Dialect that only recognized bare "(config)#" would hang the
	// moment a runbook's own lines (net.ios.config's Release Gate
	// included: it creates a loopback interface, which is exactly a
	// "(config-if)#" sub-mode) drove the device into one.
	iosExecPrompt   = regexp.MustCompile(`(?m)^[\w.\-]+#\s*$`)
	iosConfigPrompt = regexp.MustCompile(`(?m)^[\w.\-]+\([^)]*\)#\s*$`)

	// iosErrorPattern matches IOS's own line-start "% " error
	// convention ("% Invalid input detected at '^' marker.", "%
	// Incomplete command.", "% Ambiguous command: ..."), verified
	// directly against a real device rather than assumed from memory.
	iosErrorPattern = regexp.MustCompile(`(?m)^% `)
)

// IOS is the one concrete vendor Dialect this package ships, covering
// Cisco IOS and IOS-XE. Every literal string and pattern below was
// verified against a real device (the DevNet Catalyst 8000 Always-On
// sandbox, IOS XE 17.15.04c) rather than assumed: see
// pkg/netcli/live_probe_test.go, this package's own diagnostic, not a
// unit test that runs in CI.
var IOS = Dialect{
	Name: "ios",
	Prompt: func(configMode bool) *regexp.Regexp {
		if configMode {
			return iosConfigPrompt
		}
		return iosExecPrompt
	},
	DisablePaging:   "terminal length 0",
	EnterConfigMode: "configure terminal",
	ExitConfigMode:  "end",
	ErrorPattern:    iosErrorPattern,
}

// FromPrompt builds a best-effort generic Dialect from a device's own
// declared CLI prompt string (capability.NetworkCLICapable's
// CLIPrompt()). It is what net.cli.command and net.cli.config run
// against: neither carries vendor-specific knowledge of its own, so the
// only prompt convention available to either is whatever the device's
// own inventory record states.
//
// An empty prompt returns a Dialect Open refuses to use, rather than
// falling back to a heuristic pattern: a generic "ends in > or #" guess
// would also match a configuration-mode prompt like
// "hostname(config)#" partway through its own text, truncating output
// silently instead of failing loudly. A wrong prompt pattern hangs a
// read until its bound trips; a refusal at Open time is the honest
// alternative.
//
// The returned Dialect names no paging-disable command (unknown for a
// generic device), no configuration-mode commands (Config refuses on a
// Dialect with none, rather than guessing at IOS's), and no error
// pattern (no rejection convention is shared across vendors). Each gap
// is a real limit, not an oversight.
func FromPrompt(prompt string) Dialect {
	if prompt == "" {
		return Dialect{Name: "generic"}
	}
	pattern := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(prompt) + `\s*$`)
	return Dialect{
		Name: "generic",
		Prompt: func(configMode bool) *regexp.Regexp {
			return pattern
		},
	}
}

// defaultMaxBytes bounds every read a Session makes when Options leaves
// MaxBytes at zero. It follows pkg/remoteexec's own maxResponseBytes-style
// precedent (pkg/catalystcenter/client.go): a device is a trusted-ish
// upstream, but "trusted" is not "allowed to exhaust this process's
// memory." 1 MiB comfortably covers a full "show running-config" on an
// ordinarily sized device while still refusing to buffer forever against
// a wrong prompt pattern or an undisabled pager.
const defaultMaxBytes = 1 << 20

// Options configures a Session.
type Options struct {
	// MaxBytes bounds every read this Session makes. Zero uses
	// defaultMaxBytes.
	MaxBytes int
}

// Session pairs one remoteexec.Shell with a Dialect: it is what turns
// the raw byte primitive into "run one command" and "apply a batch of
// configuration," bounded and interpreted according to that Dialect's
// own conventions.
//
// A Session is not safe for concurrent use, the same restriction its
// underlying Shell already carries.
type Session struct {
	shell    *remoteexec.Shell
	dialect  Dialect
	maxBytes int
}

// Open reads the device's first prompt and, when dialect names one,
// sends its paging-disable command before returning a ready Session.
//
// It refuses outright when dialect.Prompt is nil, rather than reading
// with no way to know where a response ends: see FromPrompt's own doc
// comment for why a device with no declared prompt is a refusal, never
// a guess.
func Open(ctx context.Context, shell *remoteexec.Shell, dialect Dialect, opts Options) (*Session, error) {
	if dialect.Prompt == nil {
		return nil, fmt.Errorf("netcli: dialect %q declares no prompt pattern; set the device's cli_prompt property so netcli.FromPrompt can build one, or use a named vendor Dialect", dialectName(dialect))
	}

	maxBytes := opts.MaxBytes
	if maxBytes == 0 {
		maxBytes = defaultMaxBytes
	}

	s := &Session{shell: shell, dialect: dialect, maxBytes: maxBytes}

	if _, err := shell.ReadUntil(ctx, dialect.Prompt(false), maxBytes); err != nil {
		return nil, fmt.Errorf("netcli: reading %s's first prompt: %w", dialectName(dialect), err)
	}

	if dialect.DisablePaging != "" {
		if _, err := s.Command(ctx, dialect.DisablePaging); err != nil {
			return nil, fmt.Errorf("netcli: disabling paging on %s: %w", dialectName(dialect), err)
		}
	}

	return s, nil
}

// Command sends one line and reads its response back to the next
// exec-mode prompt, with the echoed line and the trailing prompt itself
// stripped so the returned string is just what the device printed in
// between.
func (s *Session) Command(ctx context.Context, line string) (string, error) {
	return s.command(ctx, line, false)
}

// Config brackets lines between the Dialect's EnterConfigMode and
// ExitConfigMode commands, sending them through Command one line at a
// time and reading each one back to the CONFIGURATION-mode prompt, not
// the exec-mode one: reading to the wrong prompt is what hangs a
// configuration session, because the device genuinely never sends the
// pattern being waited for until the caller leaves configuration mode.
//
// It aborts on the first line whose output matches the Dialect's own
// ErrorPattern, still attempting to leave configuration mode before
// returning that error: a runbook that fails partway through a batch
// should not also leave the device sitting in configuration mode for
// whatever runs next.
//
// It refuses outright when the Dialect names no EnterConfigMode: a
// Dialect with no configuration-mode convention (FromPrompt's own
// generic Dialect, always) has nothing to bracket lines with, and
// guessing at one is exactly the mistake this method exists to avoid.
func (s *Session) Config(ctx context.Context, lines []string) error {
	if s.dialect.EnterConfigMode == "" {
		return fmt.Errorf("netcli: dialect %q has no configuration-mode commands; send each line through Command instead", dialectName(s.dialect))
	}

	if _, err := s.command(ctx, s.dialect.EnterConfigMode, true); err != nil {
		return fmt.Errorf("netcli: entering configuration mode: %w", err)
	}

	for _, line := range lines {
		out, err := s.command(ctx, line, true)
		if err != nil {
			return fmt.Errorf("netcli: sending %q: %w", line, err)
		}
		if s.dialect.ErrorPattern != nil && s.dialect.ErrorPattern.MatchString(out) {
			if _, exitErr := s.command(ctx, s.dialect.ExitConfigMode, false); exitErr != nil {
				return fmt.Errorf("netcli: device rejected %q: %s (and leaving configuration mode afterward failed too: %w)", line, strings.TrimSpace(out), exitErr)
			}
			return fmt.Errorf("netcli: device rejected %q: %s", line, strings.TrimSpace(out))
		}
	}

	if _, err := s.command(ctx, s.dialect.ExitConfigMode, false); err != nil {
		return fmt.Errorf("netcli: exiting configuration mode: %w", err)
	}
	return nil
}

// Close closes the underlying Shell. It is safe to call once; a Session
// is not reusable afterward.
func (s *Session) Close() error {
	return s.shell.Close()
}

// command is Command and Config's shared implementation: it sends line
// and reads back to whichever prompt configMode selects.
func (s *Session) command(ctx context.Context, line string, configMode bool) (string, error) {
	if err := s.shell.WriteLine(ctx, line); err != nil {
		return "", fmt.Errorf("netcli: %w", err)
	}
	raw, err := s.shell.ReadUntil(ctx, s.dialect.Prompt(configMode), s.maxBytes)
	if err != nil {
		return "", fmt.Errorf("netcli: %w", err)
	}
	return stripEcho(raw, line, s.dialect.Prompt(configMode)), nil
}

// stripEcho removes WriteLine's own echoed line and the trailing prompt
// match from raw, leaving just what the device printed in between.
//
// A real device echoes the submitted line back verbatim, immediately
// followed by its own line break, before producing any real output
// (verified directly against a real Cisco IOS XE device); raw's own
// trailing bytes are, by construction, exactly the prompt match that
// ended the read. The trailing prompt is located by its LAST occurrence
// in the body rather than its first: multiline prompt patterns can, in
// principle, coincidentally match an earlier line of real output too
// (see iosExecPrompt's own doc comment), and only the final occurrence
// is guaranteed to be the one that actually terminated the read.
func stripEcho(raw, sentLine string, prompt *regexp.Regexp) string {
	body := raw
	if strings.HasPrefix(body, sentLine) {
		body = strings.TrimPrefix(body[len(sentLine):], "\r\n")
	}
	if locs := prompt.FindAllStringIndex(body, -1); len(locs) > 0 {
		body = body[:locs[len(locs)-1][0]]
	}
	return strings.TrimRight(body, "\r\n")
}

// dialectName returns d.Name, or a stand-in for a bare Dialect{}
// literal with no Name set, so an error message never prints an empty
// pair of quotes. Neither IOS nor FromPrompt's own Dialect actually
// hits this fallback (both always set Name), but Open's error path
// takes an arbitrary caller-supplied Dialect, and this is what keeps
// that path honest against one that skipped it.
func dialectName(d Dialect) string {
	if d.Name == "" {
		return "unnamed"
	}
	return d.Name
}
