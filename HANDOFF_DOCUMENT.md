# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**This session split Phase 72 in `.SPECIFICATION/IMPLEMENTATION.md`, the question the previous session's
own handoff note ended on.** Branch is still `feature/The-Transport-Layer`. **Nothing is committed.** No
Go code changed; the work is specification only, and `.SPECIFICATION/` is gitignored, so this file and
`LESSONS_LEARNED.md` (unchanged this session) are the only trace of it in `git status`.

**Why:** the previous session's own note flagged it: "Phase 72 is now large enough to question. At 54
items it spans the breaker consolidation, the `Target` hop chain, three WinRM execution modes, SFTP,
`internal/psdiag`, the CI matrix, and `windows.Server`'s missing accessors." A single Release Gate
covering four independently-shippable concerns cannot close until all four do, which meant SFTP and
`internal/psdiag`'s fixture work sat blocked behind a WinRM library defect neither one has anything to do
with.

**What changed.** Phase 72 kept its number and narrowed to the shared foundations: the circuit breaker
extraction, `retry.Do`, the `transport.Target` hop chain, and the CI matrix. Three new phases took the
next free numbers after Phase 74, following the exact rule Part IX's own preamble already states for
Phase 70 and Phase 71, and the same rule the previous session already applied once to place Phase 72
through Phase 74 themselves: nothing already numbered is renumbered, new work takes the next free number
and is placed thematically.

1. **Phase 75, WinRM, the Three Execution Modes.** The three typed execution modes, the WinRM adapter,
   `windows.Server`'s missing accessors, and `WindowsShellCapable`.
2. **Phase 76, `internal/psdiag`, the Blocked-Script Diagnosis.** The classifier itself and its checked-in
   fixture transcripts. Its package has no code dependency on Phase 75 (it must never import
   `internal/transport/winrm`), but its Release Gate does, since proving the classifier needs a real WinRM
   PowerShell session to test a blocked script against. That is why it is numbered after Phase 75 rather
   than built in parallel with it.
3. **Phase 77, SFTP/SCP.** SFTP behind its own narrow interface and `linux.Server`'s `FileTransferRoot`
   accessor. Depends only on Phase 72; nothing stops it being built alongside Phase 75 or Phase 76.

Every one of the original 54 checklist items kept its exact wording and moved to exactly one of the four
phases. The five phase-closing items that spanned all four concerns in one paragraph each (Fuzz/Stress
Test, Adversarial Pattern Justification, Schema/Injection Hardening, Documentation Gate, Release Gate and
Coverage Assurance) were decomposed clause by clause into each phase's own version, using the phase's own
wording throughout and dropping only the connective text needed to make each stand alone; nothing in them
was invented. Physically, Phase 75 through Phase 77 sit between the narrowed Phase 72 and Phase 73 in the
document, not after Phase 74, matching the Part's own thematic grouping ("extends what already works")
over strict numeric order, the same latitude Part IX already takes with Phase 70 and Phase 71.

**Cross-references fixed.** Every "Phase 72" mention inside Phase 73 and Phase 74 that pointed at WinRM or
SFTP now points at Phase 75 or Phase 77; mentions of the breaker, `retry.Do`, the hop chain, or the CI
matrix still correctly point at Phase 72. The Phase 34 correction note (`IMPLEMENTATION.md:4477`) was
updated the same way. Part XV's own preamble gained a paragraph explaining the second split and its
dependency ordering, in the same style as its existing paragraph explaining why Phase 72 through Phase 74
were not inserted at Phase 35.

**Not done, and not needed:** no numbers were renumbered, no other Part's cross-references were touched
(a grep of every `.SPECIFICATION/*.md` file for "Phase 72" outside `IMPLEMENTATION.md` found none before
this session started), and no checklist item's substance changed, only its location and, for the five
composite items, its grouping.

**Next step.** Nothing in Part XV is built. Phase 72 (foundations) is still the entry point and its first
item is still the Pattern Entry Gate. The two things worth settling before writing code are unchanged from
before: where `retry.Do` lands (Phase 72 already resolves this in favor of `pkg/retry`), and whether
`transport.Target`'s non-network-endpoint field is declared in Phase 72 as explicitly unproven or left
entirely to Phase 73.

**Files changed this session:** `.SPECIFICATION/IMPLEMENTATION.md` (gitignored; Phase 72 narrowed, Phase
75 through Phase 77 added, cross-references in Phase 73, Phase 74, and the Phase 34 correction updated),
this file.


---

Full session-by-session history (every `## Previous session: ...` and `## Files changed in the ... session` entry) lives in [`HANDOFF_ARCHIVE.md`](HANDOFF_ARCHIVE.md), kept out of this file so it stays cheap to read every session. Read the archive only when you need a specific past session's detail.

When Current Status above is superseded, move the outgoing text into `HANDOFF_ARCHIVE.md` as a new `## Previous session: ...` entry at the top of that file (before its current first entry), then overwrite Current Status here. Never delete a past entry.
