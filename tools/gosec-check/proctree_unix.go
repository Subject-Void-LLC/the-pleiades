//go:build !windows

// This file stops gosec and what it started on Linux and macOS.
//
// gosec is frozen first (SIGSTOP), so it starts no `go list` while its
// processes are listed. The list comes from ps, which both systems have, read
// for each process's parent, process group and start time. gosec's own group
// is then killed at once, which ends gosec even while frozen; gosec has no
// signal handler, so SIGTERM would end it just as abruptly. Everything
// descended from gosec, and each group one of them belongs to, is sent
// SIGTERM; a group reaches a compiler a `go list` started after the list was
// read. What is still there after the grace is listed again and killed only
// when it is the same process: a pid whose start time is unchanged, or a
// group one such process is still in, since a group's id is not reused while
// anything is in it.
package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// inOwnGroup makes cmd the leader of a new process group when it starts, so
// a signal sent to gosec-check's group does not reach it.
func inOwnGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// procTree is what stopTree signalled, so settle and kill act on the same.
type procTree struct {
	// members are the processes descended from gosec when it was frozen.
	members []proc
	// groups are the process groups members belong to, other than
	// gosec-check's and gosec's own.
	groups []int
}

// stopTree freezes cmd's process, lists what it started, kills cmd's group,
// and sends SIGTERM to what it started, writing to warn when the processes
// could not be listed.
func stopTree(cmd *exec.Cmd, warn io.Writer) procTree {
	leader := cmd.Process.Pid
	_ = syscall.Kill(leader, syscall.SIGSTOP)
	procs, err := listProcesses()
	// Ended at once, with no SIGCONT to forget: SIGKILL acts on a frozen
	// process, and a group's id is gosec's own pid, which cannot be reused
	// before gosec is waited for.
	_ = syscall.Kill(-leader, syscall.SIGKILL)
	if err != nil {
		fmt.Fprintf(warn, "gosec-check: could not list the processes gosec started (%v); gosec is stopped, and a `go list` it started may still be running\n", err)
		return procTree{}
	}
	var t procTree
	t.members, t.groups = descendants(procs, leader, syscall.Getpgrp())
	for _, g := range t.groups {
		_ = syscall.Kill(-g, syscall.SIGTERM)
	}
	for _, m := range t.members {
		_ = syscall.Kill(m.pid, syscall.SIGTERM)
	}
	return t
}

// settle waits up to grace for every member to end, reading the process list
// again every tenth of a second.
func (t procTree) settle(grace time.Duration) {
	for end := time.Now().Add(grace); len(t.members) > 0 && time.Now().Before(end); time.Sleep(100 * time.Millisecond) {
		procs, err := listProcesses()
		if err != nil || len(t.still(procs)) == 0 {
			return
		}
	}
}

// kill sends SIGKILL to each member still running and to each recorded group
// one of them is still in, after listing the processes again; nothing that
// cannot be shown to be the same is signalled.
func (t procTree) kill() {
	if len(t.members) == 0 {
		return
	}
	procs, err := listProcesses()
	if err != nil {
		return
	}
	alive := t.still(procs)
	for _, g := range t.groups {
		for _, p := range alive {
			if p.pgid == g {
				_ = syscall.Kill(-g, syscall.SIGKILL)
				break
			}
		}
	}
	for _, p := range alive {
		_ = syscall.Kill(p.pid, syscall.SIGKILL)
	}
}

// still returns the members procs still holds: the same pid with the same
// start time.
func (t procTree) still(procs []proc) []proc {
	now := make(map[int]proc, len(procs))
	for _, p := range procs {
		now[p.pid] = p
	}
	var alive []proc
	for _, m := range t.members {
		if p, ok := now[m.pid]; ok && p.start == m.start {
			alive = append(alive, p)
		}
	}
	return alive
}

// proc is one process ps listed.
type proc struct {
	pid, ppid, pgid int
	// start is when it started, as ps prints it, which tells a process from
	// a later one given the same pid.
	start string
}

// listProcesses lists every process with its parent, process group and start
// time.
func listProcesses() ([]proc, error) {
	out, err := exec.Command("ps", "-A", "-o", "pid=", "-o", "ppid=", "-o", "pgid=", "-o", "lstart=").Output()
	if err != nil {
		return nil, fmt.Errorf("ps: %w", err)
	}
	return parseProcesses(out)
}

// parseProcesses reads ps's "pid ppid pgid start" lines, the start time
// being the rest of the line.
func parseProcesses(out []byte) ([]proc, error) {
	var procs []proc
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 4 {
			continue
		}
		var nums [3]int
		for i, f := range fields[:3] {
			n, err := strconv.Atoi(f)
			if err != nil {
				return nil, fmt.Errorf("reading ps line %q: %w", sc.Text(), err)
			}
			nums[i] = n
		}
		procs = append(procs, proc{pid: nums[0], ppid: nums[1], pgid: nums[2], start: strings.Join(fields[3:], " ")})
	}
	return procs, sc.Err()
}

// descendants returns the processes descended from root in procs, and the
// process groups they belong to other than own and root's, each once.
func descendants(procs []proc, root, own int) (members []proc, groups []int) {
	children := map[int][]proc{}
	for _, p := range procs {
		children[p.ppid] = append(children[p.ppid], p)
	}
	seenGroup := map[int]bool{own: true, root: true}
	queue := []int{root}
	for len(queue) > 0 {
		parent := queue[0]
		queue = queue[1:]
		for _, c := range children[parent] {
			members = append(members, c)
			queue = append(queue, c.pid)
			if !seenGroup[c.pgid] && c.pgid > 1 {
				seenGroup[c.pgid] = true
				groups = append(groups, c.pgid)
			}
		}
	}
	return members, groups
}
