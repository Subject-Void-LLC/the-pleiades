// The disks VirtualBox has registered, as list --long hdds describes them,
// and the linked clones that descend from a machine's own disks.
package vboxmanage

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

// Medium is one registered disk image.
type Medium struct {
	// UUID is the disk's own UUID.
	UUID string
	// Parent is the UUID of the disk it is a differencing image of, or ""
	// for a base disk, which VirtualBox writes as "base".
	Parent string
	// Location is the disk's file on the host.
	Location string
	// Users are the machines the disk is attached to, in their current
	// state or a snapshot. A disk a linked clone descends from is not
	// attached to the clone, so it does not list the clone.
	Users []MediumUser
}

// MediumUser is a machine a disk is attached to.
type MediumUser struct {
	Name string
	UUID string
}

// userPattern is one machine in an "In use by VMs" value: its name and its
// UUID, as name (UUID: 9883f6c3-...). A snapshot the disk is attached in
// follows it in square brackets, which ParseMedia drops first.
var userPattern = regexp.MustCompile(`([^,\[\]]*?)\s*\(UUID: ([0-9a-fA-F-]{36})\)`)

// bracketed is a snapshot an "In use by VMs" value names in brackets.
var bracketed = regexp.MustCompile(`\[[^\]]*\]`)

// ParseMedia reads list --long hdds: one block of "Key: value" lines a
// disk, the blocks separated by blank lines. A value continued on the next
// line (a second machine using the disk, a second child) starts that line
// with spaces. Keys it does not use are skipped, and a block with no UUID
// is dropped.
func ParseMedia(stdout string) []Medium {
	var media []Medium
	var cur Medium
	key := ""
	flush := func() {
		if cur.UUID != "" {
			media = append(media, cur)
		}
		cur, key = Medium{}, ""
	}
	for _, raw := range strings.Split(stdout, "\n") {
		line := strings.TrimRight(raw, "\r")
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		value := line
		// A line starting with a space continues the key before it.
		if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			k, v, ok := strings.Cut(line, ":")
			if !ok {
				key = ""
				continue
			}
			key, value = k, v
		}
		value = strings.TrimSpace(value)
		switch key {
		case "UUID":
			cur.UUID = value
		case "Parent UUID":
			if value != "base" {
				cur.Parent = value
			}
		case "Location":
			cur.Location = value
		case "In use by VMs":
			for _, m := range userPattern.FindAllStringSubmatch(bracketed.ReplaceAllString(value, ""), -1) {
				cur.Users = append(cur.Users, MediumUser{Name: strings.TrimSpace(m[1]), UUID: m[2]})
			}
		}
	}
	flush()
	return media
}

// Media returns every disk VirtualBox has registered, with its parent and
// the machines using it.
func (h Host) Media(ctx context.Context) ([]Medium, error) {
	out, err := h.run(ctx, "list", "--long", "hdds")
	if err != nil {
		return nil, err
	}
	return ParseMedia(out.Stdout), nil
}

// LinkedClones returns the machines other than uuid using a disk that
// descends from one of uuid's own disks: the linked clones of uuid. When
// no disk names uuid as a user, as for a machine VirtualBox cannot read,
// whose own registry of disks was never loaded, a disk nothing uses that
// another machine's disk descends from is counted as one of uuid's, since
// VirtualBox cannot say whose it is; a base whose clones are known only
// through their own registries looks exactly so.
func LinkedClones(media []Medium, uuid string) []MediumUser {
	byUUID := make(map[string]Medium, len(media))
	own := map[string]bool{}
	for _, m := range media {
		byUUID[strings.ToLower(m.UUID)] = m
		for _, u := range m.Users {
			if strings.EqualFold(u.UUID, uuid) {
				own[strings.ToLower(m.UUID)] = true
			}
		}
	}
	unread := len(own) == 0
	var clones []MediumUser
	seen := map[string]bool{}
	for _, m := range media {
		for _, u := range m.Users {
			if strings.EqualFold(u.UUID, uuid) || seen[strings.ToLower(u.UUID)] {
				continue
			}
			if descends(byUUID, m, own, unread) {
				seen[strings.ToLower(u.UUID)] = true
				clones = append(clones, u)
			}
		}
	}
	return clones
}

// descends reports whether an ancestor of m is one of own's disks, or,
// when unread, a disk no machine uses. The walk stops at a disk it has
// seen, so a parent loop VirtualBox would never write cannot hang it.
func descends(byUUID map[string]Medium, m Medium, own map[string]bool, unread bool) bool {
	visited := map[string]bool{}
	for parent := strings.ToLower(m.Parent); parent != "" && !visited[parent]; {
		visited[parent] = true
		if own[parent] {
			return true
		}
		p, known := byUUID[parent]
		if !known {
			return false
		}
		if unread && len(p.Users) == 0 {
			return true
		}
		parent = strings.ToLower(p.Parent)
	}
	return false
}

// refuseLinked is Forget's refusal of a machine with linked clones.
func refuseLinked(uuid string, clones []MediumUser) error {
	names := make([]string, len(clones))
	for i, c := range clones {
		names[i] = fmt.Sprintf("%s (%s)", c.Name, c.UUID)
	}
	return fmt.Errorf("vboxmanage: %s has linked clones whose disks descend from its own: %s; forgetting it would leave them on disks no registered machine owns, so delete them first", uuid, strings.Join(names, ", "))
}
