package view

import "fmt"

// NavGroup is the sidebar heading a view is listed under.
//
// A named type with a closed vocabulary, for the same reason auth.Scope and
// auth.LinkRel are: the value becomes an element id and a rendered heading,
// and free text would let two views spell one group two ways and render two
// headings that look like a bug in the sidebar.
//
// The grouping itself is AWX's, and the labels are nouns rather than
// Spacelift's verb phrases ("Ship", "Enforce"). This sidebar already renders
// flat capitalised nouns, so verbs would be a second voice inside one
// control, and the audience most likely to read this navigation is the one
// arriving from AWX.
type NavGroup string

// The groups, in the order they render.
const (
	// NavGroupNone is the empty group: views that render above every
	// heading, with no heading of their own.
	NavGroupNone NavGroup = ""

	// NavGroupViews is what is happening: the dashboard and the job record.
	NavGroupViews NavGroup = "VIEWS"

	// NavGroupResources is what the platform acts on and with.
	NavGroupResources NavGroup = "RESOURCES"

	// NavGroupAccess is who may do any of it. Its own group rather than a
	// corner of administration, because "who can reach what" is the question
	// an auditor arrives with and it should not require knowing which
	// settings page it was filed under.
	NavGroupAccess NavGroup = "ACCESS"

	// NavGroupAdministration is what an administrator maintains about the
	// platform rather than about the fleet.
	NavGroupAdministration NavGroup = "ADMINISTRATION"
)

// navGroupOrder fixes the order groups render in.
//
// Held here rather than on the Descriptor deliberately. If order came from
// whichever descriptor declared a group first, two views could disagree
// about where a group sits and the sidebar would depend on registration
// order, which is a map iteration and therefore not an order at all.
var navGroupOrder = []NavGroup{
	NavGroupNone,
	NavGroupViews,
	NavGroupResources,
	NavGroupAccess,
	NavGroupAdministration,
}

// validNavGroups is the membership test Register applies.
var validNavGroups = func() map[NavGroup]bool {
	out := make(map[NavGroup]bool, len(navGroupOrder))
	for _, g := range navGroupOrder {
		out[g] = true
	}
	return out
}()

// ID is the group's DOM identifier, used by the heading a list is labelled
// by. Empty for the ungrouped bucket, which renders no heading to label.
func (g NavGroup) ID() string {
	if g == NavGroupNone {
		return ""
	}
	slug := make([]rune, 0, len(g))
	for _, r := range g {
		switch {
		case r >= 'A' && r <= 'Z':
			slug = append(slug, r+('a'-'A'))
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			slug = append(slug, r)
		default:
			slug = append(slug, '-')
		}
	}
	return "nav-group-" + string(slug)
}

// validateNavGroup refuses a group outside the declared vocabulary.
func validateNavGroup(name string, group NavGroup) error {
	if !validNavGroups[group] {
		return fmt.Errorf("view %q declares nav group %q, which is not one of the declared groups", name, group)
	}
	return nil
}

// NavSection is one rendered group: its heading, and the items a caller may
// actually reach.
type NavSection struct {
	// Label is the heading text, empty for the ungrouped bucket.
	Label string

	// ID labels the list for assistive technology, empty when there is no
	// heading to point at.
	ID string

	Items []NavItem
}

// Heading reports whether this section renders a heading at all.
func (s NavSection) Heading() bool { return s.Label != "" }

// BuildNavSections groups the sidebar, dropping any group whose every item
// the caller may not reach.
//
// A group whose items are all filtered out renders nothing, heading
// included. An empty labelled list is worse than no list: it tells a reader
// there is something under that heading and then shows them nothing, which
// reads as a broken page rather than as a permission boundary.
func BuildNavSections(prefix, currentName string, descriptors []Descriptor, permitted func(Descriptor) bool) []NavSection {
	byGroup := make(map[NavGroup][]NavItem, len(navGroupOrder))
	for _, d := range descriptors {
		if permitted != nil && !permitted(d) {
			continue
		}
		byGroup[d.NavGroup] = append(byGroup[d.NavGroup], NavItem{
			Label:   d.NavLabel,
			Href:    navHref(prefix, d.Name),
			Current: d.Name == currentName,
		})
	}

	sections := make([]NavSection, 0, len(navGroupOrder))
	for _, g := range navGroupOrder {
		items := byGroup[g]
		if len(items) == 0 {
			continue
		}
		sections = append(sections, NavSection{Label: string(g), ID: g.ID(), Items: items})
	}
	return sections
}

// FirstHref is where the index redirects: the first entry a caller can
// actually reach, in rendered order. Empty when they can reach none.
func FirstHref(sections []NavSection) string {
	for _, s := range sections {
		if len(s.Items) > 0 {
			return s.Items[0].Href
		}
	}
	return ""
}
