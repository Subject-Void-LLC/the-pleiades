package view

import (
	"fmt"
	"strings"
)

// BannerLevel names one environment or classification marking, and with it
// the colour pair that marking is rendered in.
//
// It is a closed set for the same reason auth.Scope is: these are not
// decorative labels an operator invents, they are markings with published
// meanings, and a typo that silently rendered an unrecognised value would
// be a banner claiming something nobody chose. Six of the nine below are
// the IC/DoD banner-marking colours; their values are prescribed, not
// design preferences, and must not be adjusted to taste.
type BannerLevel string

const (
	// Classification markings. The colours are the published standard.
	BannerUnclassified BannerLevel = "unclassified"
	BannerCUI          BannerLevel = "cui"
	BannerConfidential BannerLevel = "confidential"
	BannerSecret       BannerLevel = "secret"
	BannerTopSecret    BannerLevel = "topsecret"
	BannerTopSecretSCI BannerLevel = "topsecret-sci"

	// Environment markings, for the far more common failure of acting on
	// production while believing you are in staging.
	BannerDevelopment BannerLevel = "development"
	BannerStaging     BannerLevel = "staging"
	BannerProduction  BannerLevel = "production"
)

// bannerLevels is every level and the CSS class that paints it. The class
// names are a closed set so nothing caller-controlled ever reaches a class
// attribute.
var bannerLevels = map[BannerLevel]string{
	BannerUnclassified: "banner-unclassified",
	BannerCUI:          "banner-cui",
	BannerConfidential: "banner-confidential",
	BannerSecret:       "banner-secret",
	BannerTopSecret:    "banner-topsecret",
	BannerTopSecretSCI: "banner-topsecret-sci",
	BannerDevelopment:  "banner-development",
	BannerStaging:      "banner-staging",
	BannerProduction:   "banner-production",
}

// BannerLevelNames returns every valid level, sorted, for help text and
// for the error a bad configuration produces.
func BannerLevelNames() []string {
	names := make([]string, 0, len(bannerLevels))
	for level := range bannerLevels {
		names = append(names, string(level))
	}
	// Sorted so a configuration error lists them in a stable order.
	for i := range names {
		for j := i + 1; j < len(names); j++ {
			if names[j] < names[i] {
				names[i], names[j] = names[j], names[i]
			}
		}
	}
	return names
}

// maxBannerTextLength bounds the label. A banner is one line of chrome on
// every page; an unbounded string would push the entire application below
// the fold and, on a classification banner, would be a way to hide the
// marking by burying it in noise.
const maxBannerTextLength = 120

// Banner is the environment or classification marking shown at the top and
// bottom of every page.
//
// Both edges, not just the top, because that is what the classification
// standard requires: a marking that scrolls away is a marking that is not
// on the part of the screen someone is reading or photographing.
//
// It is deliberately configuration, never a setting a signed-in user can
// change. A classification marking a user can edit is a security failure
// outright, and an environment banner a user can dismiss is not a control
// -- the whole value of "PRODUCTION" across the top is that the person
// about to do something irreversible did not get to turn it off. There is
// no UI for this, and that absence is the feature.
type Banner struct {
	// Level decides the colours and is validated against the closed set.
	Level BannerLevel

	// Text is the marking itself, e.g. "UNCLASSIFIED//FOUO" or
	// "PRODUCTION -- CHANGES ARE LIVE".
	Text string
}

// Configured reports whether a banner should render at all. A deployment
// that sets nothing gets no banner rather than a default one: guessing an
// environment would eventually guess wrong, and a banner that says the
// wrong thing is worse than no banner.
func (b Banner) Configured() bool { return b.Text != "" }

// Class is the CSS class painting this banner.
func (b Banner) Class() string {
	if class, ok := bannerLevels[b.Level]; ok {
		return class
	}
	// Unreachable through ParseBanner, which refuses an unknown level.
	// Falling back to the most alarming styling rather than the least is
	// deliberate: if this is ever reached, the safe failure is a banner
	// that draws attention, not one that blends in.
	return "banner-production"
}

// Label is the marking, uppercased. Classification markings are written in
// upper case by convention, and doing it here rather than in CSS means the
// text a screen reader announces matches the text on screen.
func (b Banner) Label() string { return strings.ToUpper(b.Text) }

// ParseBanner validates an operator's configuration, refusing anything it
// does not fully understand.
//
// It fails closed and loudly. A misconfigured classification banner that
// silently rendered nothing would leave an operator believing a marking
// was displayed when it was not, which is precisely the failure mode a
// marking exists to prevent -- so an unknown level is a startup error, not
// a fallback.
func ParseBanner(level, text string) (Banner, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		if strings.TrimSpace(level) != "" {
			return Banner{}, fmt.Errorf("banner level %q is set but the banner text is empty", level)
		}
		return Banner{}, nil
	}

	if len(text) > maxBannerTextLength {
		return Banner{}, fmt.Errorf("banner text is %d characters, want at most %d",
			len(text), maxBannerTextLength)
	}
	// A control character would let a configured string break out of the
	// line it is meant to occupy.
	if strings.ContainsAny(text, "\x00\r\n\t") {
		return Banner{}, fmt.Errorf("banner text contains a control character")
	}

	parsed := BannerLevel(strings.ToLower(strings.TrimSpace(level)))
	if _, ok := bannerLevels[parsed]; !ok {
		return Banner{}, fmt.Errorf("unknown banner level %q, want one of: %s",
			level, strings.Join(BannerLevelNames(), ", "))
	}

	return Banner{Level: parsed, Text: text}, nil
}
