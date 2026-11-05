// Package facts: the users subset's probes (Phase 41c). One fact per local
// account, user.<name> = {uid, gid, home, shell, source}, and one per local
// group, group.<name> = {gid, members, source}.
//
// Local means the system's own files: /etc/passwd and /etc/group on Linux and
// FreeBSD, read through cut so the password field (an x or a *, or on an old
// system without shadow files the hash itself) never leaves the device, and
// never reaches this process, a log or a stored fact. getent is not used: it
// lists every account the name service reaches, a directory's thousands
// included, which are not this device's accounts.
package facts

import (
	"fmt"
	"strconv"
	"strings"
)

// sourceFiles is where a POSIX account fact was read: the system's own files.
const sourceFiles = "files"

// posixUserProbes reads the local account and group files, each without its
// password field (the second): passwd's name, uid, gid, comment, home and
// shell, and group's name, gid and members.
func posixUserProbes() []gatherProbe {
	return []gatherProbe{
		{
			subset:  gatherSubsetUsers,
			family:  gatherFamilyUser,
			present: `[ -r /etc/passwd ]`,
			command: orFailed(`cut -d: -f1,3- /etc/passwd`),
			parse:   parsePasswd,
			check:   listsSomething("/etc/passwd"),
		},
		{
			subset:  gatherSubsetUsers,
			family:  gatherFamilyGroup,
			present: `[ -r /etc/group ]`,
			command: orFailed(`cut -d: -f1,3- /etc/group`),
			parse:   parseGroup,
			check:   listsSomething("/etc/group"),
		},
	}
}

// parsePasswd reads cut's passwd lines (name, uid, gid, comment, home,
// shell). A comment line (FreeBSD's files start with them), a NIS marker
// (+ or -) and a line whose ids are not numbers are not accounts. The
// comment field is left out: it is free text, often a person's name, and
// says nothing about what the account can do.
func parsePasswd(out string) []gatherFact {
	var facts []gatherFact
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Split(strings.TrimRight(line, "\r"), ":")
		if len(fields) != 6 || !posixAccountName(fields[0]) {
			continue
		}
		uid, err1 := strconv.ParseUint(fields[1], 10, 32)
		gid, err2 := strconv.ParseUint(fields[2], 10, 32)
		if err1 != nil || err2 != nil {
			continue
		}
		facts = append(facts, gatherFact{key: itemKey(gatherFamilyUser, fields[0]), value: map[string]any{
			"uid": int(uid), "gid": int(gid), "home": fields[4], "shell": fields[5], "source": sourceFiles,
		}})
	}
	return facts
}

// parseGroup reads cut's group lines (name, gid, members). Members are as
// the file lists them, in its order; an account whose primary group this is
// is not repeated here, as the file does not repeat it.
func parseGroup(out string) []gatherFact {
	var facts []gatherFact
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Split(strings.TrimRight(line, "\r"), ":")
		if len(fields) != 3 || !posixAccountName(fields[0]) {
			continue
		}
		gid, err := strconv.ParseUint(fields[1], 10, 32)
		if err != nil {
			continue
		}
		members := []string{}
		for _, m := range strings.Split(fields[2], ",") {
			if m = strings.TrimSpace(m); m != "" {
				members = append(members, m)
			}
		}
		facts = append(facts, gatherFact{key: itemKey(gatherFamilyGroup, fields[0]), value: map[string]any{
			"gid": int(gid), "members": members, "source": sourceFiles,
		}})
	}
	return facts
}

// posixAccountName reports whether name can be an account in a local file:
// not empty, not a comment, and not a NIS inclusion marker (+name, -name, +).
func posixAccountName(name string) bool {
	return name != "" && !strings.HasPrefix(name, "#") && !strings.HasPrefix(name, "+") && !strings.HasPrefix(name, "-")
}

// listsSomething is a check that an answer is not empty: what source lists
// always holds an entry (an account database without root, a group file
// without wheel or root, does not happen), so an empty one was not read.
func listsSomething(source string) func(string) error {
	return func(out string) error {
		if strings.TrimSpace(out) == "" {
			return fmt.Errorf("%s listed nothing, and it always lists something", source)
		}
		return nil
	}
}
