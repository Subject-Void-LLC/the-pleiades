//go:build !windows

// This file stops gosec and every process it started on Linux and macOS.
//
// gosec is frozen first (SIGSTOP), so it starts no `go list` while its
// processes are listed. The list comes from ps, which both systems have, read
// for each process's parent and process group. Every process descended from
// gosec is signalled, and so is each process group one of them leads other
// than gosec-check's own, which reaches a compiler a `go list` started after
// the list was read. gosec itself is then signalled and let run again, so it
// acts on the signal.
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
)

// procTree is what stopTree signalled, so kill can send SIGKILL to the same.
type procTree struct {
	// leader is gosec's pid.
	leader int
	// pids are the processes descended from gosec.
	pids []int
	// groups are the process groups those lead or belong to, other than
	// gosec-check's own.
	groups []int
}

// stopTree freezes cmd's process, finds what it started, and sends SIGTERM to
// all of it, writing to warn when the processes could not be listed.
func stopTree(cmd *exec.Cmd, warn io.Writer) procTree {
	tree := procTree{leader: cmd.Process.Pid}
	_ = syscall.Kill(tree.leader, syscall.SIGSTOP)
	procs, err := listProcesses()
	if err != nil {
		fmt.Fprintf(warn, "gosec-check: could not list the processes gosec started (%v); a `go list` it started may still be running\n", err)
	}
	tree.pids, tree.groups = descendants(procs, tree.leader, syscall.Getpgrp())
	tree.signal(syscall.SIGTERM)
	_ = syscall.Kill(tree.leader, syscall.SIGCONT)
	return tree
}

// kill sends SIGKILL to everything stopTree signalled. A process or group
// that has already ended is not an error.
func (t procTree) kill() {
	t.signal(syscall.SIGKILL)
}

// signal sends sig to each group, each descendant, then gosec.
func (t procTree) signal(sig syscall.Signal) {
	for _, g := range t.groups {
		// A negative pid names a process group.
		_ = syscall.Kill(-g, sig)
	}
	for _, p := range t.pids {
		_ = syscall.Kill(p, sig)
	}
	_ = syscall.Kill(t.leader, sig)
}

// proc is one process ps listed.
type proc struct {
	pid, ppid, pgid int
}

// listProcesses lists every process with its parent and process group.
func listProcesses() ([]proc, error) {
	out, err := exec.Command("ps", "-A", "-o", "pid=", "-o", "ppid=", "-o", "pgid=").Output()
	if err != nil {
		return nil, fmt.Errorf("ps: %w", err)
	}
	return parseProcesses(out)
}

// parseProcesses reads ps's "pid ppid pgid" lines.
func parseProcesses(out []byte) ([]proc, error) {
	var procs []proc
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) != 3 {
			continue
		}
		var nums [3]int
		for i, f := range fields {
			n, err := strconv.Atoi(f)
			if err != nil {
				return nil, fmt.Errorf("reading ps line %q: %w", sc.Text(), err)
			}
			nums[i] = n
		}
		procs = append(procs, proc{pid: nums[0], ppid: nums[1], pgid: nums[2]})
	}
	return procs, sc.Err()
}

// descendants returns the pids descended from root in procs, and the process
// groups they belong to other than own and root's, each once.
func descendants(procs []proc, root, own int) (pids, groups []int) {
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
			pids = append(pids, c.pid)
			queue = append(queue, c.pid)
			if !seenGroup[c.pgid] && c.pgid > 1 {
				seenGroup[c.pgid] = true
				groups = append(groups, c.pgid)
			}
		}
	}
	return pids, groups
}
