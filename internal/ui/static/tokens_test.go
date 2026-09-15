// Package static_test's token-architecture test: that every custom property a
// component reads resolves in every skin.
//
// The rule the four-skin stylesheet rests on, asserted rather than assumed. An
// unresolvable var() with no fallback invalidates its whole declaration
// silently.
package static_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/static"
)

// The stylesheet's whole architecture is that a skin is a block of values
// rather than a second stylesheet: one component layer reads tokens, and four
// token blocks give them values. A skin needing its own component rule is a
// fork, not a skin.
//
// That only holds while every token a component reads resolves in every skin.
// A token one skin declares and another does not is a rule that silently
// evaluates to nothing for three quarters of the deployment, and CSS has no
// word for that failure: an unresolvable var() with no fallback makes the whole
// declaration invalid at computed-value time and the property falls back to its
// initial value. A border vanishes, a background goes transparent, and nothing
// warns.
//
// There are two legitimate shapes and this asserts both. A token may be
// declared in the base :root block, which every skin inherits; or it may belong
// to one skin alone, in which case every component that reads it must supply a
// fallback -- the pattern --sidebar-bg established and the Las Ventanas bevel,
// divider and well tokens follow.

// rulePattern is one top-level rule: its selector and its declarations.
var rulePattern = regexp.MustCompile(`(?s)([^{}]*)\{([^{}]*)\}`)

// declPattern finds a token declaration.
var declPattern = regexp.MustCompile(`(--[a-z0-9-]+)\s*:`)

// readPattern finds a token read, capturing whether a fallback follows.
var readPattern = regexp.MustCompile(`var\(\s*(--[a-z0-9-]+)\s*(,?)`)

func TestEveryTokenResolvesInEverySkin(t *testing.T) {
	body, err := static.Read("app.css")
	if err != nil {
		t.Fatalf("reading app.css: %v", err)
	}
	css := stripComments(string(body))

	// Which skin declares which tokens. The bare :root block is Brutalist's
	// and is also the base every other skin inherits from, so a token in it
	// resolves everywhere.
	base := map[string]bool{}
	bySkin := map[string]map[string]bool{}
	// blockPattern and stripComments are contrast_test.go's, deliberately:
	// two opinions about how this stylesheet parses is how the two tests end
	// up disagreeing about what it says.
	for _, m := range blockPattern.FindAllStringSubmatch(css, -1) {
		skin := skinOf(m[1])
		for _, d := range declPattern.FindAllStringSubmatch(m[2], -1) {
			if skin == "" {
				base[d[1]] = true
				continue
			}
			if bySkin[skin] == nil {
				bySkin[skin] = map[string]bool{}
			}
			bySkin[skin][d[1]] = true
		}
	}
	// Brutalist's block is written as `:root, :root[data-skin="brutalist"]`,
	// so it is both the default skin and the base every other skin inherits
	// from. Its tokens therefore resolve everywhere and belong in base --
	// which is the whole reason the other three skins declare only what they
	// differ on.
	for token := range bySkin["brutalist"] {
		base[token] = true
	}
	if len(base) == 0 || len(bySkin) < 3 {
		t.Fatalf("parsed %d base tokens and %d skins, which is not this stylesheet", len(base), len(bySkin))
	}

	// Every read, in every rule, with the selector that read it.
	for _, rule := range rulePattern.FindAllStringSubmatch(css, -1) {
		selector, decls := strings.TrimSpace(rule[1]), rule[2]
		// A token block declares rather than reads; a token reading another
		// token inside a skin's own block resolves within that block.
		if strings.HasPrefix(selector, ":root") {
			continue
		}

		for _, read := range readPattern.FindAllStringSubmatch(decls, -1) {
			token, hasFallback := read[1], read[2] == ","
			if hasFallback || base[token] {
				continue
			}
			// The remaining legitimate case: a rule scoped to the one skin
			// that declares the token.
			if owner := scopedSkin(selector); owner != "" && bySkin[owner][token] {
				continue
			}

			var declaredIn []string
			for skin, tokens := range bySkin {
				if tokens[token] {
					declaredIn = append(declaredIn, skin)
				}
			}
			t.Errorf("rule %q reads %s with no fallback.\n"+
				"It is not in the base :root block, so it resolves only in %v and silently "+
				"invalidates this declaration in every other skin.\n"+
				"Either declare it in the base block or read it as var(%s, <fallback>).",
				selector, token, declaredIn, token)
		}
	}
}

// skinOf reads the skin name out of a token block's attribute selector, empty
// for the bare :root block.
func skinOf(attr string) string {
	// A block whose selector list includes a bare :root is the base, however
	// many skin aliases it also carries.
	for _, part := range strings.Split(attr, ",") {
		if strings.TrimSpace(part) == ":root" {
			return ""
		}
	}
	const marker = `data-skin="`
	i := strings.Index(attr, marker)
	if i < 0 {
		return ""
	}
	rest := attr[i+len(marker):]
	j := strings.Index(rest, `"`)
	if j < 0 {
		return ""
	}
	return rest[:j]
}

// scopedSkin reads the skin a component rule is scoped to, empty when it
// applies to every skin.
func scopedSkin(selector string) string {
	const marker = `data-skin="`
	i := strings.Index(selector, marker)
	if i < 0 {
		return ""
	}
	rest := selector[i+len(marker):]
	j := strings.Index(rest, `"`)
	if j < 0 {
		return ""
	}
	return rest[:j]
}
