// Package iosparse parses the output of Cisco IOS and IOS XE show commands
// into the nested structure Cisco's genieparser returns for them, so
// genieparser's schema documentation describes the result and two reads of
// the same command can be compared field by field.
//
// It is a port of a chosen set of parsers from genieparser
// (https://github.com/CiscoTestAutomation/genieparser) at commit
// e62f9467a383997e190e04679d769c5c009e7460, Copyright (c) 2021 Cisco
// Systems, Inc. and/or its affiliates, licensed under the Apache License,
// Version 2.0 (LICENSE.genieparser beside this file). It is modified: each
// parser, and each genieparser helper it calls, was translated by hand from
// Python to Go. NOTICE.genieparser records this, and each ported file says
// which genieparser file it comes from.
//
// The ported classes are ShowVersion, ShowInventory, ShowIpInterfaceBrief,
// ShowInterfaces, ShowIpRoute, ShowIpOspfNeighbor, ShowIpBgpSummary,
// ShowBgpSummary, ShowVlan, ShowCdpNeighbors, ShowLldpNeighbors and
// ShowArchiveConfigDifferences, all from genieparser's iosxe folder.
// Commands lists the command forms they cover. Each is held, in this
// package's tests, to every one of genieparser's own recorded outputs and
// expected results for it (testdata/, copied by tools/geniefixtures), the
// iosxe ones and, where genieparser's ios class is the iosxe parser
// unchanged, the ios ones; testdata/skips.json names each fixture the port
// does not pass and why.
//
// Two pieces genieparser relies on live in Cisco's separate genie library,
// whose source is not published with genieparser: the table reader
// parsergen.oper_fill_tabular and the schema check MetaParser.parse runs.
// Both are reimplemented here from their documented behavior and the
// fixtures; nothing of them was copied. See tabular.go and schema.go.
//
// Where the port differs from genieparser, and why:
//
//   - Go's regexp (RE2) has no lookaround. The one ported expression that
//     used it, ShowVlan's VLAN name, is rewritten equivalently and the
//     ShowVlan fixtures hold the rewrite. Every other expression is
//     genieparser's text unchanged.
//   - Python's \d, \w and \s also match non-ASCII digits, letters and
//     spaces; Go's match ASCII only. IOS output is ASCII.
//   - A dict key genieparser makes an int (a route's next-hop index, a CDP
//     entry's index) is its decimal string, the key JSON gives it, and an
//     int is an int64; a number past int64 is a ParserError where Python
//     would carry on.
//   - Where genieparser raises a Python exception on malformed output, the
//     port returns a *ParserError naming it, rather than a result Genie
//     never returns. That includes genieparser's own defects, which are
//     kept: ShowInterfaces fails on an "Encapsulation ..., medium is ..."
//     line, as genieparser does.
//   - A remote SPAN range wider than every VLAN number is refused, where
//     genieparser would enumerate it, so untrusted output cannot make a
//     parse run unbounded.
//   - "show ip route vrf X" passes X to ShowIpRoute, as genieparser's
//     fixtures do; genieparser's own command dispatcher does not, which
//     differs only for output with no "Routing Table:" line.
//   - BGP summary forms that make genieparser run a second command on the
//     device ("rd" forms, "all summary" forms) are refused.
package iosparse
