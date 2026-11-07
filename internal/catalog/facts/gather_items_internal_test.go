// Tests of the files, ports and users parsers (gather_files.go,
// gather_ports.go, gather_users.go, gather_bsd_more.go, gather_win_items.go)
// against output each real tool printed: the lines below were captured from
// Alpine 3.20, Debian 12, Rocky 9.6, FreeBSD 15.1 (bsd-check), macOS 27.0.1
// (the lab Mac) and Windows Server 2025 (win-check) as an ordinary login,
// with only the surrounding noise cut. The probes themselves run against real
// systems in gather_test.go and the lab release gates.
package facts

import (
	"encoding/base64"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// factsByKey is facts as a map, for comparing.
func factsByKey(facts []gatherFact) map[string]any {
	out := map[string]any{}
	for _, f := range facts {
		out[f.key] = f.value
	}
	return out
}

func TestParseSockstat_ReadsFreeBSDsListeners(t *testing.T) {
	// FreeBSD 15.1, sockstat -46lq as the login pleiades, with one of its own
	// listeners on ::1 and a bound UDP socket; then a hidden owner's line as
	// security.bsd.see_other_uids=0 prints it, a connected UDP socket, and a
	// command holding a space.
	out := "pleiades nc         1662  3 udp4  127.0.0.1:7778        *:*                  \n" +
		"pleiades nc         1660  3 tcp6  [::1]:7777            *:*                  \n" +
		"root     sshd       1521  6 tcp6  *:22                  *:*                  \n" +
		"root     sshd       1521  7 tcp4  *:22                  *:*                  \n" +
		"root     syslogd    1295  6 udp6  *:514                 *:*                  \n" +
		"?        ?          ?     ? tcp4  *:25                  *:*\n" +
		"root     ntpd       900   5 udp4  10.0.0.5:123          10.0.0.1:123\n" +
		"www      my daemon  77    4 tcp46 *:8080                *:*\n" +
		// One listener, three nginx workers holding it, the master's pid
		// lowest: one fact, named by the master, whatever the count.
		"www      nginx      903   6 tcp4  *:80                  *:*\n" +
		"www      nginx      901   6 tcp4  *:80                  *:*\n" +
		"www      nginx      902   6 tcp4  *:80                  *:*\n" +
		"USER COMMAND PID FD PROTO LOCAL ADDRESS FOREIGN ADDRESS\n"
	got := factsByKey(parseSockstat(out))
	want := map[string]any{
		"port.udp:127.0.0.1:7778": map[string]any{"protocol": "udp", "address": "127.0.0.1", "port": 7778, "process": "nc"},
		"port.tcp:[::1]:7777":     map[string]any{"protocol": "tcp", "address": "::1", "port": 7777, "process": "nc"},
		"port.tcp:[::]:22":        map[string]any{"protocol": "tcp", "address": "::", "port": 22, "process": "sshd"},
		"port.tcp:0.0.0.0:22":     map[string]any{"protocol": "tcp", "address": "0.0.0.0", "port": 22, "process": "sshd"},
		"port.udp:[::]:514":       map[string]any{"protocol": "udp", "address": "::", "port": 514, "process": "syslogd"},
		// A hidden owner is unknown, not a process named ?.
		"port.tcp:0.0.0.0:25": map[string]any{"protocol": "tcp", "address": "0.0.0.0", "port": 25},
		"port.tcp:[::]:8080":  map[string]any{"protocol": "tcp", "address": "::", "port": 8080, "process": "my daemon"},
		"port.tcp:0.0.0.0:80": map[string]any{"protocol": "tcp", "address": "0.0.0.0", "port": 80, "process": "nginx"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("sockstat:\n got %v\nwant %v", got, want)
	}
}

func TestParseDarwinNetstat_ReadsTheProcessBetweenFixedColumns(t *testing.T) {
	// macOS 27.0.1, netstat -anv -p tcp and -p udp as an ordinary login.
	out := "Active Internet connections (including servers)\n" +
		"Proto Recv-Q Send-Q  Local Address          Foreign Address        (state)          rxbytes      txbytes  rhiwat  shiwat          process:pid    state  options           gencnt    flags   flags1 usecnt rtncnt fltrs\n" +
		"tcp4       0      0  10.0.0.206.22          10.0.0.191.62092       ESTABLISHED         6904         4138  131072  131072     sshd-session:81177  00182 0000000c 00000000004222e8 00000080 01000800      2      0 000000\n" +
		"tcp6       0      0  ::1.7679               *.*                    LISTEN                 0            0  131072  131072     Google Drive:75810  00100 00000006 000000000039a019 00000001 00000800      1      0 000000\n" +
		"tcp4       0      0  127.0.0.1.8021         *.*                    LISTEN                 0            0  131072  131072          launchd:1      00180 00000006 0000000000252064 00000000 00000800      1      0 000000\n" +
		"tcp46      0      0  *.3283                 *.*                    LISTEN                 0            0  131072  131072         ARDAgent:1187   00000 00000202 0000000000002457 00000040 00000800      2      0 000000\n" +
		"tcp4       0      0  *.88                   *.*                    LISTEN                 0            0  131072  131072              kdc:1188   00180 00000006 0000000000002450 00000001 00000800      1      0 000000\n" +
		portSectionMark + "\n" +
		"udp6       0      0  2601:84:ca01:103.56230 2607:6bc0::10.443                         10128        17794 1048576   29040    Claude Helper:79701  00102 00000000 00000000004221d8 00000000 00200900      1      0 000002\n" +
		"udp46      0      0  *.547                  *.*                                           0            0  786896    9216           dhcp6d:75374  00100 00000000 000000000038f9fc 00000000 00000800      1      0 000000\n" +
		"udp4       0      0  *.*                    *.*                                           0            0  786896    9216        symptomsd:484    00000 00000000 0000000000120c2d 00000000 00002800      1      0 000000\n" +
		// A row whose process column is not name:pid names no process.
		"tcp4       0      0  *.9000                 *.*                    LISTEN                 0            0  131072  131072     odd-layout     00100 00000006 000000000039a019 00000001 00000800      1      0 000000\n"
	got := factsByKey(parseDarwinNetstat(out))
	want := map[string]any{
		"port.tcp:[::1]:7679":     map[string]any{"protocol": "tcp", "address": "::1", "port": 7679, "process": "Google Drive"},
		"port.tcp:127.0.0.1:8021": map[string]any{"protocol": "tcp", "address": "127.0.0.1", "port": 8021, "process": "launchd"},
		"port.tcp:[::]:3283":      map[string]any{"protocol": "tcp", "address": "::", "port": 3283, "process": "ARDAgent"},
		"port.tcp:0.0.0.0:88":     map[string]any{"protocol": "tcp", "address": "0.0.0.0", "port": 88, "process": "kdc"},
		"port.udp:[::]:547":       map[string]any{"protocol": "udp", "address": "::", "port": 547, "process": "dhcp6d"},
		"port.tcp:0.0.0.0:9000":   map[string]any{"protocol": "tcp", "address": "0.0.0.0", "port": 9000},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("netstat:\n got %v\nwant %v", got, want)
	}
}

func TestParseLinuxPorts_ReadsTheKernelsTablesInItsByteOrder(t *testing.T) {
	// Alpine 3.20's /proc/net/tcp6 (two listeners on ::, by root, seen by
	// uid 1000), with an IPv4 loopback listener, a bound and a connected UDP
	// socket, an established TCP socket, ls -l of the fd directories this
	// login could list, and the process names.
	tcp := "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n" +
		"   0: 0100007F:1538 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 29011 1 0000000000000000 100 0 0 10 0\n" +
		"   1: 0100007F:1538 0100007F:A000 01 00000000:00000000 00:00000000 00000000  1000        0 29012 1 0000000000000000 100 0 0 10 0\n"
	tcp6 := "  sl  local_address                         remote_address                        st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n" +
		"   0: 00000000000000000000000000000000:08AE 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 591956 1 0000000000000000 100 0 0 10 0\n" +
		"   1: 0000000000000000FFFF00000100007F:08AF 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 592969 1 0000000000000000 100 0 0 10 0\n"
	udp := "   sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode ref pointer drops\n" +
		"  5: 0100007F:14E9 00000000:0000 07 00000000:00000000 00:00000000 00000000  1000        0 7001 2 0000000000000000 0\n" +
		"  6: 0100007F:14EA 0100007F:0035 01 00000000:00000000 00:00000000 00000000  1000        0 7002 2 0000000000000000 0\n" +
		// A UDP socket not yet bound lists as 0.0.0.0:0 in state 07.
		"  7: 00000000:0000 00000000:0000 07 00000000:00000000 00:00000000 00000000  1000        0 7003 2 0000000000000000 0\n"
	fds := "/proc/1/fd:\nls: /proc/1/fd: Permission denied\n/proc/40/fd:\nlrwx------    1 u        u               64 Nov  4 09:00 3 -> socket:[29011]\n" +
		"lrwx------    1 u        u               64 Nov  4 09:00 4 -> socket:[7001]\n/proc/41/fd:\nlrwx------    1 u        u               64 Nov  4 09:00 3 -> socket:[29011]\n"
	comm := "1\tinit\n40\tpostgres\n41\tpostgres worker\n"
	out := "x86_64\n" + portSectionMark + " tcp\n" + tcp + portSectionMark + " tcp6\n" + tcp6 + portSectionMark + " udp\n" + udp +
		portSectionMark + " udp6\n" + portSectionMark + " fd\n" + fds + portSectionMark + " comm\n" + comm
	got := factsByKey(parseLinuxPorts(out))
	want := map[string]any{
		// The lowest pid holding the socket names it.
		"port.tcp:127.0.0.1:5432":          map[string]any{"protocol": "tcp", "address": "127.0.0.1", "port": 5432, "uid": 1000, "process": "postgres"},
		"port.tcp:[::]:2222":               map[string]any{"protocol": "tcp", "address": "::", "port": 2222, "uid": 0},
		"port.tcp:[::ffff:127.0.0.1]:2223": map[string]any{"protocol": "tcp", "address": "::ffff:127.0.0.1", "port": 2223, "uid": 0},
		"port.udp:127.0.0.1:5353":          map[string]any{"protocol": "udp", "address": "127.0.0.1", "port": 5353, "uid": 1000, "process": "postgres"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ports:\n got %v\nwant %v", got, want)
	}
	// A big-endian machine prints the same address in order: od says so, or
	// failing od, uname -m names one; od wins over a name that cannot tell
	// (MIPS is either).
	if machineBigEndian("0001\nmips\n") || !machineBigEndian("0100\nmips\n") || !machineBigEndian("\ns390x\n") || machineBigEndian("\nx86_64\n") {
		t.Error("the byte order was not read from od first and uname -m after")
	}
	be := strings.Replace(out, "x86_64", "s390x", 1)
	be = strings.Replace(be, "0100007F:1538 00000000:0000 0A", "7F000001:1538 00000000:0000 0A", 1)
	if _, ok := factsByKey(parseLinuxPorts(be))["port.tcp:127.0.0.1:5432"]; !ok {
		t.Errorf("a big-endian machine's 7F000001 was not read as 127.0.0.1: %v", factsByKey(parseLinuxPorts(be)))
	}
}

func TestParsePasswdAndGroup_ReadTheLocalFilesWithoutTheirPasswordField(t *testing.T) {
	// FreeBSD 15.1's cut -d: -f1,3- /etc/passwd and /etc/group (toor has no
	// shell), with a comment and a NIS marker FreeBSD's files may hold.
	passwd := "# $FreeBSD$\nroot:0:0:Charlie &:/root:/bin/sh\ntoor:0:0:Bourne-again Superuser:/root:\n" +
		"daemon:1:1:Owner of many system processes:/root:/usr/sbin/nologin\n+::::::\nbroken:x:1:c:/h:/s\n"
	group := "wheel:0:root\ndaemon:1:\nstaff:20:root,alice , bob\n"
	got := factsByKey(append(parsePasswd(passwd), parseGroup(group)...))
	want := map[string]any{
		"user.root":    map[string]any{"uid": 0, "gid": 0, "home": "/root", "shell": "/bin/sh", "source": "files"},
		"user.toor":    map[string]any{"uid": 0, "gid": 0, "home": "/root", "shell": "", "source": "files"},
		"user.daemon":  map[string]any{"uid": 1, "gid": 1, "home": "/root", "shell": "/usr/sbin/nologin", "source": "files"},
		"group.wheel":  map[string]any{"gid": 0, "members": []string{"root"}, "source": "files"},
		"group.daemon": map[string]any{"gid": 1, "members": []string{}, "source": "files"},
		"group.staff":  map[string]any{"gid": 20, "members": []string{"root", "alice", "bob"}, "source": "files"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("accounts:\n got %v\nwant %v", got, want)
	}
}

func TestParseDscl_ReadsTheMacsLocalDirectory(t *testing.T) {
	// macOS 27.0.1, dscl . -readall /Users and /Groups with the named
	// attributes; the last user's home holds a space, which dscl prints as
	// the attribute's name alone and its value on the next, indented line.
	users := "NFSHomeDirectory: /var/virusmails\nPrimaryGroupID: 83\nRecordName: _amavisd amavisd\nUniqueID: 83\nUserShell: /usr/bin/false\n-\n" +
		"NFSHomeDirectory:\n /Users/Jo Smith\nPrimaryGroupID: 20\nRecordName: jo\nUniqueID: 501\nUserShell: /bin/zsh\n-\n" +
		"RecordName: nouid\n"
	groups := "GroupMembership: _analyticsd _networkd _timed\nPrimaryGroupID: 250\nRecordName: _analyticsusers\n-\nPrimaryGroupID: 55\nRecordName: _appleevents\n"
	got := factsByKey(append(parseDsclUsers(users), parseDsclGroups(groups)...))
	want := map[string]any{
		"user._amavisd":         map[string]any{"uid": 83, "gid": 83, "home": "/var/virusmails", "shell": "/usr/bin/false", "source": "dslocal"},
		"user.jo":               map[string]any{"uid": 501, "gid": 20, "home": "/Users/Jo Smith", "shell": "/bin/zsh", "source": "dslocal"},
		"group._analyticsusers": map[string]any{"gid": 250, "members": []string{"_analyticsd", "_networkd", "_timed"}, "source": "dslocal"},
		"group._appleevents":    map[string]any{"gid": 55, "members": []string{}, "source": "dslocal"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("dscl:\n got %v\nwant %v", got, want)
	}
}

func TestParseFileSection_ReadsEachStatAndTellsUnreadableFromAbsent(t *testing.T) {
	sha := "cfe588b029ebbf885be7f167aa60c1d3121dabd8a72bd6a33ac0e5ff387007f1"
	cases := map[string]struct {
		section string
		want    map[string]any
	}{
		// GNU coreutils (Debian 12) and BusyBox (Alpine 3.20) stat -c.
		"gnu file": {"ok:regular file:870:0:0:644:root:root\nsha:" + sha + "  -\n",
			map[string]any{"state": "present", "type": "file", "size": int64(870), "uid": 0, "gid": 0, "mode": "0644", "owner": "root", "group": "root", "sha256": sha, "readable": true, "world_readable": true}},
		// Not world-readable: its digest is handed over raw, for the engine to
		// key or seal, and its size is dropped.
		"private": {"ok:regular file:497:0:42:640:root:shadow\nraw:" + sha + "  -\n",
			map[string]any{"state": "present", "type": "file", "uid": 0, "gid": 42, "mode": "0640", "owner": "root", "group": "shadow", "sha256_raw": sha, "readable": true, "world_readable": false}},
		"private unreadable": {"ok:regular file:497:0:42:640:root:shadow\nraw-unreadable\n",
			map[string]any{"state": "present", "type": "file", "uid": 0, "gid": 42, "mode": "0640", "owner": "root", "group": "shadow", "readable": false, "world_readable": false}},
		"gnu unreadable file": {"ok:regular file:497:0:42:640:root:shadow\nsha-unreadable\n",
			map[string]any{"state": "present", "type": "file", "size": int64(497), "uid": 0, "gid": 42, "mode": "0640", "owner": "root", "group": "shadow", "readable": false, "world_readable": true}},
		"gnu directory": {"ok:directory:4096:0:0:755:root:root\n",
			map[string]any{"state": "present", "type": "directory", "uid": 0, "gid": 0, "mode": "0755", "owner": "root", "group": "root"}},
		"empty file, setuid": {"ok:regular empty file:0:0:0:4755:root:root\nsha:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855\n",
			map[string]any{"state": "present", "type": "file", "size": int64(0), "uid": 0, "gid": 0, "mode": "4755", "owner": "root", "group": "root", "sha256": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", "readable": true, "world_readable": true}},
		// BSD stat -f (FreeBSD 15.1 and macOS 27): a link, its target hex.
		"bsd link": {"ok:Symbolic Link:23:0:0:0755:root:wheel\nlink:2f7573722f73686172652f6d6973632f7465726d636170\n",
			map[string]any{"state": "present", "type": "link", "size": int64(23), "uid": 0, "gid": 0, "mode": "0755", "owner": "root", "group": "wheel", "link_target": "/usr/share/misc/termcap"}},
		"bsd socket": {"ok:Socket:0:0:0:0666:root:wheel\n",
			map[string]any{"state": "present", "type": "socket", "size": int64(0), "uid": 0, "gid": 0, "mode": "0666", "owner": "root", "group": "wheel"}},
		"unknown owner": {"ok:regular file:1:4242:4242:600:UNKNOWN:UNKNOWN\nsha:\n",
			map[string]any{"state": "present", "type": "file", "size": int64(1), "uid": 4242, "gid": 4242, "mode": "0600", "owner": "UNKNOWN", "group": "UNKNOWN", "world_readable": true}},
		"absent":     {"absent\n", map[string]any{"state": "absent"}},
		"unreadable": {"unreadable\n", map[string]any{"state": "unreadable"}},
		// Output this does not recognize is never taken as absent.
		"garbage": {"ok:mystery:1\nabsentish\n", map[string]any{"state": "unreadable"}},
	}
	for name, tc := range cases {
		if got := parseFileSection(tc.section); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s:\n got %v\nwant %v", name, got, tc.want)
		}
	}
}

func TestFilesFacts_MatchAnswersToPathsByPosition(t *testing.T) {
	paths := []string{"/etc/hosts", "/a b/c\nd", "/gone"}
	out := fileSectionMark + "\nok:regular file:1:0:0:644:root:root\n" + fileSectionMark + "\nunreadable\n" + fileSectionMark + "\nabsent\n"
	got := factsByKey(filesFacts(paths, parseFiles(out)))
	if got["file.%2Fetc%2Fhosts"].(map[string]any)["state"] != "present" ||
		got["file.%2Fa%20b%2Fc%0Ad"].(map[string]any)["state"] != "unreadable" ||
		got["file.%2Fgone"].(map[string]any)["state"] != "absent" {
		t.Errorf("files by position: %v", got)
	}
	// One answer too many (a device printing a mark of its own) leaves every
	// path unreadable rather than shifting an answer onto the wrong path.
	extra := factsByKey(filesFacts(paths, parseFiles(out+fileSectionMark+"\nabsent\n")))
	for k, v := range extra {
		if v.(map[string]any)["state"] != "unreadable" {
			t.Errorf("%s = %v after a misaligned answer, want unreadable", k, v)
		}
	}
}

func TestGatherFilePaths_RefusesWhatCannotBeReadSafely(t *testing.T) {
	long := "/" + strings.Repeat("a/", 100)
	many := make([]any, gatherMaxFiles+1)
	for i := range many {
		many[i] = "/f" + strings.Repeat("x", i)
	}
	for name, files := range map[string]any{
		"relative": []any{"etc/hosts"}, "nul": []any{"/a\x00b"}, "unc": []any{`\\server\share\x`},
		"too long": []any{long}, "twice": []any{"/a", "/a"}, "too many": many, "bad utf8": []any{"/\xff"}, "not a list": 5,
		// Windows reads / as \, so each of these names another machine, a
		// pipe or a raw disk there.
		"slash unc": []any{"//attacker.example/share/x"}, "mixed unc": []any{`/\attacker\share\x`},
		"device path": []any{"//?/UNC/attacker/share/x"}, "pipe": []any{"//./pipe/spoolss"}, "raw disk": []any{"//./PhysicalDrive0"},
		"con": []any{`C:\x\CON`}, "nul ext": []any{`C:\nul.txt`}, "com1": []any{"c:/a/com1.log"}, "lpt9": []any{`C:\LPT9`},
	} {
		if _, err := gatherFilePaths(map[string]any{gatherParamFiles: files}); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	got, err := gatherFilePaths(map[string]any{gatherParamFiles: []any{"/etc/hosts", `C:\Windows\win.ini`, "c:/x"}})
	if err != nil || len(got) != 3 {
		t.Errorf("absolute POSIX and Windows paths: %v, %v", got, err)
	}
}

func TestWindowsParsers_ReadWhatTheScriptsPrinted(t *testing.T) {
	// Windows Server 2025 (win-check), each script run as Administrator
	// through WinRM, cut to a few items; members null is a group
	// Get-LocalGroupMember could not list.
	ports := `[{"protocol":"tcp","address":"::","port":5985,"process":"System"},{"protocol":"tcp","address":"0.0.0.0","port":135,"process":"svchost"},` +
		`{"protocol":"udp","address":"fe80::c3b6:179c:3dae:ff7%4","port":546,"process":"svchost"},{"protocol":"udp","address":"0.0.0.0","port":0,"process":null},` +
		`{"protocol":"tcp","address":"127.0.0.1","port":9,"process":null}]`
	users := `[{"name":"Administrator","sid":"S-1-5-21-1-2-3-500","enabled":true},{"name":"Guest","sid":"S-1-5-21-1-2-3-501","enabled":false}]`
	groups := `[{"name":"Administrators","sid":"S-1-5-32-544","members":["WIN-CHECK\\Administrator"]},{"name":"Backup Operators","sid":"S-1-5-32-551","members":[]},` +
		`{"name":"Orphaned","sid":"S-1-5-32-999","members":null}]`
	files := `[{"state":"present","type":"file","size":7,"attributes":"Archive","owner":"BUILTIN\\Administrators","sha256":"cd2eca3535741f27a8ae40c31b0c41d4057a7a7b912b33b9aed86485d1c84676","readable":true,"world_readable":true},` +
		`{"state":"present","type":"link","link_target":"C:\\pleiades-41c2\\locked","attributes":"Directory, ReparsePoint","owner":"BUILTIN\\Administrators"},` +
		`{"state":"present","type":"file","attributes":"Hidden, System, Archive","world_readable":false,"readable":true,"size":5,"sha256":"cd2eca3535741f27a8ae40c31b0c41d4057a7a7b912b33b9aed86485d1c84676","sha256_raw":"cd2eca3535741f27a8ae40c31b0c41d4057a7a7b912b33b9aed86485d1c84676"},{"state":"absent"},{"state":"unreadable"}]`
	got := factsByKey(append(append(parseWindowsPorts(ports), parseWindowsUsers(users)...), parseWindowsGroups(groups)...))
	got2 := factsByKey(filesFacts([]string{`C:\a.txt`, `C:\link`, `C:\pagefile.sys`, `C:\gone`, `C:\locked\x`}, parseWindowsFiles(files)))
	for k, v := range got2 {
		got[k] = v
	}
	want := map[string]any{
		"port.tcp:[::]:5985":                      map[string]any{"protocol": "tcp", "address": "::", "port": 5985, "process": "System"},
		"port.tcp:0.0.0.0:135":                    map[string]any{"protocol": "tcp", "address": "0.0.0.0", "port": 135, "process": "svchost"},
		"port.udp:[fe80::c3b6:179c:3dae:ff7]:546": map[string]any{"protocol": "udp", "address": "fe80::c3b6:179c:3dae:ff7%4", "port": 546, "process": "svchost"},
		"port.tcp:127.0.0.1:9":                    map[string]any{"protocol": "tcp", "address": "127.0.0.1", "port": 9},
		"user.Administrator":                      map[string]any{"sid": "S-1-5-21-1-2-3-500", "enabled": true, "source": "sam"},
		"user.Guest":                              map[string]any{"sid": "S-1-5-21-1-2-3-501", "enabled": false, "source": "sam"},
		"group.Administrators":                    map[string]any{"sid": "S-1-5-32-544", "members": []string{`WIN-CHECK\Administrator`}, "source": "sam"},
		"group.Backup%20Operators":                map[string]any{"sid": "S-1-5-32-551", "members": []string{}, "source": "sam"},
		"group.Orphaned":                          map[string]any{"sid": "S-1-5-32-999", "source": "sam"},
		`file.C:\a.txt`:                           map[string]any{"state": "present", "type": "file", "size": int64(7), "attributes": "Archive", "owner": `BUILTIN\Administrators`, "sha256": "cd2eca3535741f27a8ae40c31b0c41d4057a7a7b912b33b9aed86485d1c84676", "readable": true, "world_readable": true},
		`file.C:\link`:                            map[string]any{"state": "present", "type": "link", "attributes": "Directory, ReparsePoint", "owner": `BUILTIN\Administrators`, "link_target": `C:\pleiades-41c2\locked`},
		`file.C:\pagefile.sys`:                    map[string]any{"state": "present", "type": "file", "attributes": "Hidden, System, Archive", "world_readable": false, "readable": true, "sha256_raw": "cd2eca3535741f27a8ae40c31b0c41d4057a7a7b912b33b9aed86485d1c84676"},
		`file.C:\gone`:                            map[string]any{"state": "absent"},
		`file.C:\locked\x`:                        map[string]any{"state": "unreadable"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("windows:\n got %v\nwant %v", got, want)
	}
}

func TestItemParsers_SkipWhatTheyCannotRead(t *testing.T) {
	// Lines a parser must pass over rather than report: a bad port, an
	// address with no port, a short stat line, bad hex, nameless accounts.
	if got := parseSockstat("root sshd 1 2 tcp4 *:http *:*\nroot sshd 1 2 tcp4 nocolon *:*\n"); len(got) != 0 {
		t.Errorf("sockstat without a numeric port: %v", got)
	}
	if got := parseFileSection("ok:regular file:1\nok:regular file:x:0:0:644:a:b\n"); got["state"] != "unreadable" {
		t.Errorf("a short or unreadable stat line: %v", got)
	}
	bad := "x86_64\n" + portSectionMark + " tcp\n   0: ZZ00007F:1538 00000000:0000 0A 0 0 0 0 0 1\n   1: 0100007F:1538 00000000:0000 0A 0 0 0 x 0 2\n"
	if got := parseLinuxPorts(bad); len(got) != 0 {
		t.Errorf("unreadable /proc/net lines: %v", got)
	}
	if got := parseWindowsUsers(`[{"name":"","sid":"S-1"}]`); len(got) != 0 {
		t.Errorf("a nameless account: %v", got)
	}
	if got := parseWindowsGroups(`[{"name":"","sid":"S-1"}]`); len(got) != 0 {
		t.Errorf("a nameless group: %v", got)
	}
}

func TestGatherPlan_RefusesFilesItCannotRead(t *testing.T) {
	for _, params := range []map[string]any{
		{gatherParamFiles: 5},
		{gatherParamFiles: []any{"relative"}},
	} {
		if _, err := gatherPlan(params); err == nil {
			t.Errorf("%v: accepted", params)
		}
	}
}

func TestWindowsFileProbe_CarriesThePathsAsBase64JSON(t *testing.T) {
	paths := []string{`C:\it's $(x) "y"`, `C:\b`}
	probe := windowsFileProbe()
	script := probe.commandFor(paths)
	start := strings.Index(script, "FromBase64String('") + len("FromBase64String('")
	end := strings.Index(script[start:], "'")
	raw, err := base64.StdEncoding.DecodeString(script[start : start+end])
	var got []string
	if err != nil || json.Unmarshal(raw, &got) != nil || !reflect.DeepEqual(got, paths) {
		t.Fatalf("the script carries %q, want %q", raw, paths)
	}
	// No path appears in the script as text PowerShell would read.
	if strings.Contains(script, "it's") {
		t.Error("a path reached the script as text")
	}
	if probe.name() != "the files probe" {
		t.Errorf("the probe is named %q, never by its paths", probe.name())
	}
}

func TestGatherFilesFor_HoldsEachPathToTheDevicesOwnForm(t *testing.T) {
	if err := gatherFilesFor([]string{`C:\Windows\win.ini`, "c:/x"}, platformWindows); err != nil {
		t.Errorf("drive paths on Windows: %v", err)
	}
	if err := gatherFilesFor([]string{"/etc/hosts"}, platformLinux); err != nil {
		t.Errorf("a POSIX path on Linux: %v", err)
	}
	// Each written for the other kind of system would read something else.
	if gatherFilesFor([]string{"/etc/hosts"}, platformWindows) == nil || gatherFilesFor([]string{`C:\x`}, platformFreeBSD) == nil {
		t.Error("a path in the other platform's form was accepted")
	}
	if windowsDeviceName(`C:\console.txt`) || windowsDeviceName(`C:\COM10`) || windowsDeviceName(`C:\lpt`) {
		t.Error("an ordinary file name read as a device")
	}
}

func TestGatherPlan_RefusesAFilterThatDropsTheNamedPaths(t *testing.T) {
	_, err := gatherPlan(map[string]any{gatherParamFiles: []any{"/etc/hosts"}, gatherParamFilter: []any{"os.*"}})
	if err == nil || !strings.Contains(err.Error(), "matches no file.") {
		t.Errorf("named paths with a filter that drops them: %v", err)
	}
	if _, err := gatherPlan(map[string]any{gatherParamFiles: []any{"/etc/hosts"}, gatherParamFilter: []any{"file.*"}}); err != nil {
		t.Errorf("a filter keeping the paths: %v", err)
	}
}

func TestUserProbes_NeverCarryThePasswordField(t *testing.T) {
	// The probe cuts the second field on the device, so it never leaves it.
	for _, p := range posixUserProbes() {
		if !strings.Contains(p.command, "cut -d: -f1,3- ") {
			t.Errorf("%s does not cut the password field", p.command)
		}
	}
	// And a whole passwd or group line, hash and all, reached the parser
	// only by a probe that did not cut it: it is no account at all.
	whole := "root:$6$salt$hashhashhash:0:0:root:/root:/bin/sh\n"
	if got := parsePasswd(whole); len(got) != 0 {
		t.Errorf("an uncut passwd line was read: %v", got)
	}
	if got := parseGroup("wheel:$6$salt$hash:0:root\n"); len(got) != 0 {
		t.Errorf("an uncut group line was read: %v", got)
	}
}
