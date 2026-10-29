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
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/farside"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcfg"
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

	// DisableWrapping is the command Open sends after DisablePaging to
	// stop the device wrapping a long line at the terminal's width. IOS
	// otherwise echoes a command longer than 80 columns scrolled
	// sideways, "show archive config differences unix:a.cfg system:runn$",
	// so the echo no longer matches the line sent and is left in the
	// output (measured on IOS XE 17.15, 2026-10-23). Empty means Open
	// sends nothing.
	DisableWrapping string

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

	// Notices are lines ErrorPattern matches that report rather than
	// reject: the device says the line changed nothing, or how it went.
	// Config collects them instead of failing on them.
	Notices []*regexp.Regexp

	// Unprivileged matches an exec prompt that is not privileged (IOS's
	// "R1>"), and Enable is the command that asks to leave it (IOS's
	// "enable"). Both empty means this Dialect has one exec mode. When
	// Open meets an unprivileged prompt it sends Enable and answers the
	// secret Options.EnableSecret holds; with none, the session stays
	// unprivileged, where commands run and Config refuses.
	Unprivileged *regexp.Regexp
	Enable       string

	// AsyncMessage matches a line the device prints on its own, between
	// commands rather than in answer to one: IOS's syslog on a console
	// line ("*Oct 26 04:51:29.496: %SEC_LOGIN-5-LOGIN_SUCCESS: ..."),
	// which arrives after the login and is followed by a prompt of its
	// own. Nil means the dialect knows of none.
	AsyncMessage *regexp.Regexp
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
	//
	// iosExecPrompt accepts user mode's ">" as well as privileged "#": an
	// account below privilege 15 lands at "R1>" (the DevNet CML sandbox's
	// routers, over Telnet, 2026-10-23), and a read waiting for "#" there
	// never ended.
	iosExecPrompt   = regexp.MustCompile(`(?m)^[\w.\-]+[>#]\s*$`)
	iosUserPrompt   = regexp.MustCompile(`(?m)^[\w.\-]+>\s*$`)
	iosConfigPrompt = regexp.MustCompile(`(?m)^[\w.\-]+\([^)]*\)#\s*$`)

	// iosAsyncMessage matches one IOS syslog line as a console prints it:
	// an optional sequence number, a timestamp (marked * or . when the
	// clock is not synchronized), then %FACILITY-SEVERITY-MNEMONIC:.
	// Measured on IOL XE 17.15 through the CML console (2026-10-26, Phase
	// 129d), where the login's own %SEC_LOGIN-5-LOGIN_SUCCESS and the
	// prompt after it were read as the next command's whole answer.
	// The sequence number and timestamp are each optional: with no
	// service timestamps log, the line is the mnemonic alone.
	iosAsyncMessage = regexp.MustCompile(`^(?:\d+:\s*)?(?:[*.]?[A-Za-z0-9:. ]*:\s*)?%[A-Z][A-Z0-9_]*-[0-7]-[A-Z0-9_]+:`)

	// iosErrorPattern matches IOS's own line-start "% " error
	// convention ("% Invalid input detected at '^' marker.", "%
	// Incomplete command.", "% Ambiguous command: ..."), verified
	// directly against a real device rather than assumed from memory,
	// and the "%Error ..." a file operation prints when it fails ("%Error
	// opening flash:x (No such file or directory)"), with no space after
	// the percent sign. A "%Warning:" line is not a failure, and neither
	// is a syslog line ("%SYS-5-CONFIG_I: ...").
	iosErrorPattern = regexp.MustCompile(`(?m)^%(?: |Error\b)`)

	// iosNotices are the "% " lines IOS prints for a configuration line it
	// accepted: one already in the configuration (an access list entry, an
	// IKEv2 proposal or a profile's match), or a key pair being made.
	// Each was measured on IOL XE 17.15 in the blind CVD lab (2026-10-24),
	// where failing on them kept ordinary configuration from being run
	// twice. Only these: every other "% " line still rejects, "% Duplicate
	// sequence number" and "% Failed to add ace to access-list" among them,
	// since those say the line was not applied.
	iosNotices = []*regexp.Regexp{
		regexp.MustCompile(`^% Duplicate (?:permit|deny) statement ignored\b`),
		regexp.MustCompile(`^% Policy already has same proposal set\b`),
		// match fvrf <vrf> under an IKEv2 policy that already has it: seen 30
		// times in one second configuration run in the blind CVD lab (runs 5
		// and 6, 2026-10-27), and in runs 4 and 5 before that.
		regexp.MustCompile(`^% Policy already has the same matching fvrf statement\b`),
		regexp.MustCompile(`^% Already found same '[^']*' statement in this profile\b`),
		regexp.MustCompile(`^% Key pair was generated at: `),
		// crypto key generate rsa ... modulus <n>, made and made again
		// (measured through net.ios.config on the same lab's BR3): IOS
		// names the modulus, says it is generating, and, for a label it
		// already has, replaces that key pair with no question asked.
		regexp.MustCompile(`^% The key modulus size is [0-9]+ bits$`),
		regexp.MustCompile(`^% Generating [0-9]+ bit RSA keys, keys will be non-exportable\.\.\.`),
		regexp.MustCompile(`^% You already have RSA keys defined named \S+\.$`),
		regexp.MustCompile(`^% They will be replaced\.$`),
		// crypto key zeroize rsa <label>: what it says before asking
		// "Do you really want to remove these keys? [yes/no]:", which a
		// task answers with prompts. A label with no key pair ("% The
		// specified keypair does not exist") still rejects: a mistyped
		// label must not pass for a key already gone.
		regexp.MustCompile(`^% Keys to be removed are named '[^']*'\.$`),
		regexp.MustCompile(`^% All router certs issued using these keys will also be removed\.$`),
		// Measured through net.ios.config on IOL XE 17.15.1 (2026-10-25,
		// Phase 129a), each from a line that left the configuration as it
		// was or did what it said: entering a certificate map an IKEv2 profile
		// already uses, naming a keyring a profile already has, and what
		// removing an enrolled trustpoint says before its "[yes/no]:", which
		// a task answers with prompts (FAILURE_PATTERNS 646). Removing what is
		// not there ("% Specified proposal NOPE does not exist") still
		// rejects: a mistyped name must not pass for one already gone.
		regexp.MustCompile(`^% certificate map already used in ikev2/isakmp profiles$`),
		regexp.MustCompile(`^% Profile already contains this keyring$`),
		regexp.MustCompile(`^% Removing an enrolled trustpoint will destroy all certificates`),
		regexp.MustCompile(`^% Be sure to ask the CA administrator to revoke your certificates\.$`),
	}
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
	DisableWrapping: "terminal width 512",
	EnterConfigMode: "configure terminal",
	ExitConfigMode:  "end",
	ErrorPattern:    iosErrorPattern,
	Notices:         iosNotices,
	Unprivileged:    iosUserPrompt,
	Enable:          "enable",
	AsyncMessage:    iosAsyncMessage,
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
	// A terminal control sequence may follow the prompt: BusyBox's ash,
	// behind a real telnetd, prints "lab$ " and then ESC[6n, asking where
	// its cursor is (measured 2026-10-23). It is the shell talking to the
	// terminal, not output, so it does not keep the prompt from matching.
	pattern := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(prompt) + `\s*(?:\x1b\[[0-9;?]*[A-Za-z]\s*)*$`)
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

	// EnableSecret answers the Dialect's Enable command when the device
	// lands in an unprivileged mode. It is written to the device and to
	// nothing else: never an error, never a result.
	EnableSecret string
}

// Session pairs one remoteexec.Shell with a Dialect: it is what turns
// the raw byte primitive into "run one command" and "apply a batch of
// configuration," bounded and interpreted according to that Dialect's
// own conventions.
//
// A Session is not safe for concurrent use, the same restriction its
// underlying Shell already carries.
type Session struct {
	shell    Shell
	dialect  Dialect
	maxBytes int

	// unprivileged is set when the device left the session in a mode
	// its Dialect calls unprivileged, so Config refuses at once.
	unprivileged bool

	// echoes is set once the device has echoed a line back, which every
	// IOS device does: only then is a read with no echo taken for the
	// device's own message (asyncOnly), so one that never echoes is never
	// waited on.
	echoes bool
}

// Open reads the device's first prompt and, when dialect names one,
// sends its paging-disable command before returning a ready Session.
//
// It refuses outright when dialect.Prompt is nil, rather than reading
// with no way to know where a response ends: see FromPrompt's own doc
// comment for why a device with no declared prompt is a refusal, never
// a guess.
func Open(ctx context.Context, shell Shell, dialect Dialect, opts Options) (*Session, error) {
	if dialect.Prompt == nil {
		return nil, fmt.Errorf("netcli: dialect %q declares no prompt pattern; set the device's cli_prompt property so netcli.FromPrompt can build one, or use a named vendor Dialect", dialectName(dialect))
	}

	maxBytes := opts.MaxBytes
	if maxBytes == 0 {
		maxBytes = defaultMaxBytes
	}

	s := &Session{shell: shell, dialect: dialect, maxBytes: maxBytes}

	first, err := shell.ReadUntil(ctx, dialect.Prompt(false), maxBytes)
	if err != nil {
		return nil, fmt.Errorf("netcli: reading %s's first prompt: %w", dialectName(dialect), err)
	}
	if dialect.Unprivileged != nil && dialect.Unprivileged.MatchString(first) {
		s.unprivileged = true
		if opts.EnableSecret != "" {
			if err := s.enable(ctx, opts.EnableSecret); err != nil {
				return nil, err
			}
		}
	}

	if dialect.DisablePaging != "" {
		if _, err := s.Command(ctx, dialect.DisablePaging); err != nil {
			return nil, fmt.Errorf("netcli: disabling paging on %s: %w", dialectName(dialect), err)
		}
	}
	if dialect.DisableWrapping != "" {
		if _, err := s.Command(ctx, dialect.DisableWrapping); err != nil {
			return nil, fmt.Errorf("netcli: disabling line wrapping on %s: %w", dialectName(dialect), err)
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
// ExitConfigMode commands, sending them one line at a time and reading
// each one back to the CONFIGURATION-mode prompt, not the exec-mode one:
// reading to the wrong prompt is what hangs a configuration session,
// because the device genuinely never sends the pattern being waited for
// until the caller leaves configuration mode. It is ConfigWith with no
// answers, so a line that asks a question fails at once, naming it.
func (s *Session) Config(ctx context.Context, lines []string) error {
	_, err := s.ConfigWith(ctx, lines, ConfigOptions{})
	return err
}

// ConfigOptions are what ConfigWith answers and accepts.
type ConfigOptions struct {
	// Dialog answers the questions a configuration line asks, as Dialog
	// does a command's. ExpectDisconnect does not apply: a configuration
	// line that ends the session is a failure.
	Dialog DialogOptions
	// Accept are further lines the Dialect's ErrorPattern matches that
	// the caller takes as notices rather than rejections, beside the
	// Dialect's own Notices.
	Accept []*regexp.Regexp
}

// ConfigResult is what a configuration batch came to.
type ConfigResult struct {
	// Exchanges are the questions its lines asked and how each was
	// answered, in order, across the whole batch.
	Exchanges []Exchange
	// Notices are the lines the device printed that ErrorPattern matches
	// and a notice or Accept pattern let through, in order.
	Notices []string
}

// ConfigWith is Config answering the questions a line asks as
// opts.Dialog says (a question nothing answers fails the line at once,
// UnansweredShape) and taking the Dialect's Notices, and opts.Accept, as
// notices rather than rejections.
//
// It aborts on the first line whose output holds any other line matching
// the Dialect's own ErrorPattern, still attempting to leave configuration
// mode before returning that error: a runbook that fails partway through
// a batch should not also leave the device sitting in configuration mode
// for whatever runs next. A refused or unanswered question closes the
// session instead, which the device takes as abandoning the line.
//
// It refuses outright when the Dialect names no EnterConfigMode: a
// Dialect with no configuration-mode convention (FromPrompt's own
// generic Dialect, always) has nothing to bracket lines with, and
// guessing at one is exactly the mistake this method exists to avoid.
func (s *Session) ConfigWith(ctx context.Context, lines []string, opts ConfigOptions) (ConfigResult, error) {
	var res ConfigResult
	if s.dialect.EnterConfigMode == "" {
		return res, fmt.Errorf("netcli: dialect %q has no configuration-mode commands; send each line through Command instead", dialectName(s.dialect))
	}
	if s.unprivileged {
		return res, fmt.Errorf("netcli: the device left this session in %s's unprivileged mode, where configuration is refused; store the device's enable secret with its credential (pleiades add-credential --enable-password)", dialectName(s.dialect))
	}
	if opts.Dialog.ExpectDisconnect {
		return res, errors.New("netcli: a configuration line cannot end by the device closing the session")
	}
	if err := checkDialog(opts.Dialog); err != nil {
		return res, err
	}

	if _, err := s.command(ctx, s.dialect.EnterConfigMode, true); err != nil {
		return res, fmt.Errorf("netcli: entering configuration mode: %w", err)
	}

	asked := 0
	for _, line := range lines {
		out, err := s.converse(ctx, line, s.dialect.Prompt(true), opts.Dialog, true, &asked)
		res.Exchanges = append(res.Exchanges, out.Exchanges...)
		if err != nil {
			return res, fmt.Errorf("netcli: sending %q: %w", line, err)
		}
		notices, rejected := s.judge(out.Output, out.Exchanges, opts.Accept)
		res.Notices = append(res.Notices, notices...)
		if rejected {
			if _, exitErr := s.command(ctx, s.dialect.ExitConfigMode, false); exitErr != nil {
				return res, fmt.Errorf("netcli: device rejected %q: %s (and leaving configuration mode afterward failed too: %w)", line, rejection(out.Output), exitErr)
			}
			return res, fmt.Errorf("netcli: device rejected %q: %s", line, rejection(out.Output))
		}
	}

	if _, err := s.command(ctx, s.dialect.ExitConfigMode, false); err != nil {
		return res, fmt.Errorf("netcli: exiting configuration mode: %w", err)
	}
	return res, nil
}

// rejection is a device's answer to a line it rejected, fit for an error:
// masked as configuration is first (the device can echo the line, and a
// configuration line can hold a secret), then bounded and quoted through
// pkg/farside, so a long answer or a control byte never reaches the error
// raw.
func rejection(out string) string {
	return farside.MaskedOutput(out, netcfg.Mask)
}

// judge reads one line's output: each line the Dialect's ErrorPattern
// matches is a notice when a Notices or accept pattern matches it too, and
// otherwise a rejection. A question the line asked and the task answered
// is neither, though IOS may begin one with "% " ("% Do you really want
// to replace them? [yes/no]:"): it stays in the output with its answer.
func (s *Session) judge(out string, asked []Exchange, accept []*regexp.Regexp) (notices []string, rejected bool) {
	if s.dialect.ErrorPattern == nil {
		return nil, false
	}
	for l := range strings.SplitSeq(out, "\n") {
		l = strings.TrimRight(l, "\r \t")
		if !s.dialect.ErrorPattern.MatchString(l) || slices.ContainsFunc(asked, func(ex Exchange) bool { return ex.Question != "" && strings.Contains(l, ex.Question) }) {
			continue
		}
		if matchesAny(s.dialect.Notices, l) || matchesAny(accept, l) {
			notices = append(notices, l)
			continue
		}
		rejected = true
	}
	return notices, rejected
}

// matchesAny reports whether any of patterns matches line.
func matchesAny(patterns []*regexp.Regexp, line string) bool {
	for _, p := range patterns {
		if p.MatchString(line) {
			return true
		}
	}
	return false
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
	prompt := s.dialect.Prompt(configMode)
	raw, err := s.shell.ReadUntil(ctx, prompt, s.maxBytes)
	// A read that ended on the prompt the device printed after a message
	// of its own, before the line's echo, is not the line's answer: that
	// is still coming, to the next prompt.
	for tries := 0; err == nil && tries < maxAsyncReads && s.asyncOnly(raw, line, prompt); tries++ {
		raw, err = s.shell.ReadUntil(ctx, prompt, s.maxBytes)
	}
	if err != nil {
		return "", readFailure(err, "", "")
	}
	s.noteEcho(raw, line)
	return stripEcho(raw, line, prompt), nil
}

// noteEcho records that the device echoed line, when raw begins with it.
func (s *Session) noteEcho(raw, line string) {
	if line != "" && strings.HasPrefix(raw, line) {
		s.echoes = true
	}
}

// maxAsyncReads bounds how many reads of the device's own messages one
// command passes over before taking what it reads as its answer.
const maxAsyncReads = 3

// asyncOnly reports whether raw, read for line, holds nothing before its
// prompt but the device's own messages (Dialect.AsyncMessage): no echo of
// line, and at least one message, on a session whose device echoes. A read
// that is empty, or holds anything else, is the line's answer as it always
// was, and a device that has never echoed is never waited on.
func (s *Session) asyncOnly(raw, line string, prompt *regexp.Regexp) bool {
	if s.dialect.AsyncMessage == nil || !s.echoes || strings.HasPrefix(raw, line) {
		return false
	}
	body := raw
	if locs := prompt.FindAllStringIndex(body, -1); len(locs) > 0 {
		body = body[:locs[len(locs)-1][0]]
	}
	messages := 0
	for l := range strings.SplitSeq(body, "\n") {
		l = strings.TrimSpace(l)
		switch {
		case l == "":
		case s.dialect.AsyncMessage.MatchString(l):
			messages++
		default:
			return false
		}
	}
	return messages > 0
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

// Rejection is the first line of out in d's error form that none of d's
// Notices matches, and whether there is one. A command's output can carry
// a notice as well as a rejection: show crypto key mypubkey rsa prints "%
// Key pair was generated at: ..." for each key pair (measured on IOL XE
// 17.15), which no reader should take as the command failing.
func (d Dialect) Rejection(out string) (string, bool) {
	return d.RejectionWith(out, nil)
}

// RejectionWith is Rejection taking each line accept matches as a notice
// too: a task's own list of "% " lines it expects (net.ios.command's
// accept, as net.ios.config's).
func (d Dialect) RejectionWith(out string, accept []*regexp.Regexp) (string, bool) {
	if d.ErrorPattern == nil {
		return "", false
	}
	for l := range strings.SplitSeq(out, "\n") {
		l = strings.TrimRight(l, "\r \t")
		if d.ErrorPattern.MatchString(l) && !matchesAny(d.Notices, l) && !matchesAny(accept, l) {
			return l, true
		}
	}
	return "", false
}

// IOSCommandError matches the lines IOS prints when it did not run an
// exec-mode command at all: the parser's own refusals of the line ("%
// Invalid input detected at '^' marker.", "% Incomplete command.", "%
// Ambiguous command: ...", "% Unrecognized command"), a ping or traceroute IOS
// could not send ("% Unrecognized host or address, or protocol not running.",
// "% Bad IP address"), and a file operation's
// "%Error ..." (more or dir of a file that is not there). A command that only
// reads is held to these alone: what it prints is the device's data, and a "%
// " line inside it ("% Self-signed CA certificate:" in show crypto pki
// certificates pem, measured in the blind CVD lab, 2026-10-25) is output, not
// a refusal.
var IOSCommandError = regexp.MustCompile(`^%(?: (?:Invalid input detected|Incomplete command|Ambiguous command|Unrecognized command|Unrecognized host|Bad IP address)|Error\b)`)

// CommandRejection returns the first line of out matching IOSCommandError.
func CommandRejection(out string) (string, bool) {
	for l := range strings.SplitSeq(out, "\n") {
		l = strings.TrimRight(l, "\r \t")
		if IOSCommandError.MatchString(l) {
			return l, true
		}
	}
	return "", false
}
