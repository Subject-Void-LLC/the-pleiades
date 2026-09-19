// Package remotefile: predicting what Apply would change, for a check.
package remotefile

import "strconv"

// This file holds the comparison Apply acts on and the prediction a check
// makes from it.
//
// # One comparison, two callers
//
// A method run for real asks Apply to change whatever differs. A method
// run as a check (collection.ModeCheck) asks the same question and does
// nothing with the answer except report it. If those were two separate
// comparisons they could disagree, and a check that says "no change" for
// a path a real run would then chmod is worse than no check at all: it is
// a dry run that lies. So both go through Differs and the two helpers
// below it, and Apply decides what to send with exactly the same
// functions a check uses to decide what it would have sent.
//
// # A prediction says only what it can know
//
// What a real run leaves behind is mostly the request itself. Some of it
// is not: a new directory gets a mode from the device's umask and an owner
// from whichever account connected, and neither is visible before the
// directory exists. A prediction that filled those in would be a guess
// dressed as a fact, and the diff it lands in is exactly the record an
// operator reads to decide whether to run the change for real. So a
// Prediction leaves out every key it cannot know, rather than writing an
// empty string or a zero that a reader would take as the answer.

// The diff map keys an Info renders under. They are named once here so
// Info.Map, which writes them, and Prediction.Map, which leaves some of
// them out, cannot disagree about a spelling.
const (
	keyExists = "exists"
	keyKind   = "kind"
	keyMode   = "mode"
	keyOwner  = "owner"
	keyGroup  = "group"
	keySize   = "size"
	keyMtime  = "mtime"
	keyTarget = "target"
)

// specialBits are the setuid and setgid bits: the two a kernel clears on
// its own when a regular file changes hands. The sticky bit is not among
// them, since no ownership change touches it.
const specialBits = 0o6000

// Differs reports whether Apply would change anything about a path found
// as before: whether a real run would send a chown, a chgrp or a chmod.
//
// It is the whole of Apply's decision, exposed so a check can make the
// same decision without acting on it. An empty field of want is "leave
// this alone" here exactly as it is there, so it never counts as a
// difference.
func Differs(want Attributes, before Info) bool {
	return ownershipDiffers(want, before) || modeDiffers(want, before)
}

// ownershipDiffers reports whether the owner or group want names differs
// from what before carries.
//
// Each field is compared only when want names it. Apply sends a single
// chown for both when both are named, and a chown or chgrp for one when
// only one is, but in every one of those three shapes the question is the
// same one: does a named field differ.
func ownershipDiffers(want Attributes, before Info) bool {
	return (want.Owner != "" && want.Owner != before.Owner) ||
		(want.Group != "" && want.Group != before.Group)
}

// modeDiffers reports whether want names a mode different from the one
// before carries.
//
// Both sides are compared in the four-digit form, because stat prints 644
// and a runbook writes 0644, and without the padding every run would
// report a change forever.
func modeDiffers(want Attributes, before Info) bool {
	return want.Mode != "" && NormalizeMode(want.Mode) != before.Mode
}

// ownershipChangeClearsMode reports whether changing the ownership of a
// path found as before can clear mode's setuid or setgid bits.
//
// Linux clears those bits on anything but a directory whenever its owner
// or group changes (verified on this repository's own test machine:
// chmod 2755 then chgrp leaves 0755 on a file and 2755 on a directory).
// It is a deliberate kernel protection, since a setuid program that
// changed hands would otherwise run as its new owner. It matters to Apply
// only when the mode asked for carries one of those bits, because that is
// the only case where the kernel's clearing leaves the path different
// from the request.
func ownershipChangeClearsMode(before Info, mode string) bool {
	return before.Kind != KindDirectory && hasSpecialBits(mode)
}

// hasSpecialBits reports whether an octal mode string carries the setuid
// or setgid bit. A string that is not octal carries neither, which is the
// safe answer: callers only use it to decide whether to send one more
// chmod.
func hasSpecialBits(mode string) bool {
	bits, err := strconv.ParseUint(NormalizeMode(mode), 8, 32)
	if err != nil {
		return false
	}
	return bits&specialBits != 0
}

// chmodArgument is the mode string Apply hands to chmod: the requested
// mode in its four-digit form with one more leading zero.
//
// The extra zero is load-bearing, and only on GNU coreutils. GNU chmod
// PRESERVES a directory's setuid and setgid bits when given a numeric mode
// of four digits or fewer, so `chmod 0755` on a directory carrying 2755
// leaves 2755 and exits 0. Without the fifth digit a task asking for 0755
// on such a directory would send a chmod that did nothing, read back
// 2755, report changed, and do the same on every run forever. GNU's own
// documentation names the five-digit form as the way to say "exactly
// these bits", and BusyBox, toybox and the BSD chmod all read it as the
// same octal number, since each parses the whole string with strtol in
// base 8 and accepts any value up to 07777.
func chmodArgument(mode string) string {
	return "0" + NormalizeMode(mode)
}

// Prediction is what a change is expected to leave at a path, worked out
// without making it. A check records its Map as the After half of a
// diff.
type Prediction struct {
	// info holds every predicted value, including zero values standing in
	// for the ones that cannot be predicted.
	info Info

	// unknown names the Map keys whose value only the device can decide,
	// so Map leaves them out rather than rendering info's zero values as
	// if they were answers.
	unknown []string
}

// Map renders the Prediction as the map a diff's After half records: the
// same keys Info.Map writes, minus every one this prediction cannot know.
func (p Prediction) Map() map[string]any {
	m := p.info.Map()
	for _, key := range p.unknown {
		delete(m, key)
	}
	return m
}

// PredictApply returns what Apply(want) would leave at a path found as
// before, assuming every command it sends succeeds.
//
// before must describe something that exists; a path that does not is
// PredictCreate's question, since Apply alone never creates anything.
//
// Every field want names is predicted to be exactly what it names, which
// is what Apply makes true: ownership first, then the mode, then the mode
// again if the ownership change could have cleared a special bit the
// request carries. Size, modification time, kind and a symlink's target
// are unchanged, since chmod and chown move none of them.
//
// One case is left unknown rather than predicted. A task that changes the
// owner or group of a regular file carrying setuid or setgid, and names
// no mode, leaves the mode to the kernel. Linux clears setuid always and
// setgid when the group-execute bit is set, and other kernels have their
// own rules, some depending on whether the account is root. Apply
// deliberately does not put the bits back, since the task never asked for
// them and the clearing is a protection, so the honest prediction is that
// the mode is not known.
func PredictApply(want Attributes, before Info) Prediction {
	p := Prediction{info: before}
	if want.Owner != "" {
		p.info.Owner = want.Owner
	}
	if want.Group != "" {
		p.info.Group = want.Group
	}
	switch {
	case want.Mode != "":
		p.info.Mode = NormalizeMode(want.Mode)
	case ownershipDiffers(want, before) && ownershipChangeClearsMode(before, before.Mode):
		p.unknown = append(p.unknown, keyMode)
	}
	return p
}

// PredictCreate returns what creating a path of the given kind, and then
// applying want to it, would leave behind.
//
// Only what the task names can be predicted. A mode the task leaves unset
// comes from the device's umask, an owner or group it leaves unset comes
// from the connecting account (or from a setgid parent directory), and a
// new path's size and modification time are the filesystem's to choose.
// All of those are left out of Map rather than guessed.
func PredictCreate(kind Kind, want Attributes) Prediction {
	p := PredictApply(want, Info{Kind: kind})
	p.unknown = append(p.unknown, keySize, keyMtime)
	if want.Mode == "" {
		p.unknown = append(p.unknown, keyMode)
	}
	if want.Owner == "" {
		p.unknown = append(p.unknown, keyOwner)
	}
	if want.Group == "" {
		p.unknown = append(p.unknown, keyGroup)
	}
	return p
}

// PredictWrite returns what Write of size bytes followed by Apply(want)
// would leave at a path found as before. Write replaces the file with a
// new one, so the size is the content's and the modification time is the
// device's to decide; the attributes are what Apply(want) sets, which
// for an existing file is PredictApply's answer and for a new one is
// PredictCreate's (so an owner or group want does not name is unknown,
// being the connecting account's). before must be absent or a regular
// file; the caller refuses anything else, as a real run does.
func PredictWrite(want Attributes, before Info, size int64) Prediction {
	var p Prediction
	if before.Exists() {
		p = PredictApply(want, before)
	} else {
		p = PredictCreate(KindFile, want)
	}
	p.info.Size = size
	known := p.unknown[:0]
	for _, key := range p.unknown {
		if key != keySize {
			known = append(known, key)
		}
	}
	p.unknown = append(known, keyMtime)
	return p
}

// PredictSymlink returns what Symlink(target, path) would leave: a
// symbolic link to target. Its own mode, owner, group, size and
// modification time are the device's to decide (a new link takes them
// from the account and the moment that makes it), so they are unknown.
func PredictSymlink(target string) Prediction {
	return Prediction{
		info:    Info{Kind: KindSymlink, Target: target},
		unknown: []string{keyMode, keyOwner, keyGroup, keySize, keyMtime},
	}
}

// PredictTouch returns what Touch followed by Apply(want) would leave at
// a path found as before: a new empty regular file when nothing is there
// (PredictCreate), and otherwise before with want applied (PredictApply)
// and a modification time only the device can decide, since touching an
// existing file moves it. before must be absent or a regular file; the
// caller refuses anything else, as a real run does.
func PredictTouch(want Attributes, before Info) Prediction {
	if !before.Exists() {
		return PredictCreate(KindFile, want)
	}
	p := PredictApply(want, before)
	p.unknown = append(p.unknown, keyMtime)
	return p
}
