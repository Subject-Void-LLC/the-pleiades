// Package engine: handlers and notify, Ansible's way of running a task only
// when something before it changed.
//
// A runbook's handlers: are tasks that run only when notified. A task's
// notify: names handlers (by name, or by a topic a handler listens: to), and
// when that task ran on a device and changed something there, each handler it
// names is notified on that device. At the end of each section (pretasks,
// tasks, posttasks) every notified handler runs on the devices that notified
// it, once however many tasks did, in the order the handlers are written, not
// the order they were notified.
//
// The builder splices one copy of every handler after each section that
// notifies any (chain.go): real nodes of the DAG, so validation, the journal
// and a rollback treat a handler as the task it is. Whether a copy runs on a
// device is decided when it is reached (runPerDevice): a device it was not
// notified on records a not_notified skip. A failure that ends the run runs no
// handler, as Ansible does without force_handlers, which stays refused.
//
// Not built: meta: flush_handlers, a handler notifying another handler, and a
// handler that is a block or a parallel group. Each is refused by name.
package engine

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// SkipReasonNotNotified is why a handler did not run on a device.
const SkipReasonNotNotified = "not notified: no task that notifies this handler changed anything on this device in this section"

// handlerRef marks a node as one copy of a handler: which handler, by its
// place in the runbook's handlers list.
type handlerRef struct {
	index int
}

// validateHandlers refuses handlers and notify keys that cannot mean what they
// say, naming where, and returns which handlers each notify name reaches: a
// handler's own name, or a topic one or more handlers listen to.
func validateHandlers(def *WorkflowDef) (map[string][]int, error) {
	targets := map[string][]int{}
	names := map[string]bool{}
	for i := range def.Handlers {
		h := &def.Handlers[i]
		label := fmt.Sprintf("handlers[%d]", i)
		name := strings.TrimSpace(h.Name)
		switch {
		case name == "":
			return nil, fmt.Errorf("%s: a handler needs a name, which is what notify: names it by", label)
		case names[name]:
			return nil, fmt.Errorf("%s: two handlers are named %q; notify: names one handler by its name, so names must be unique (listen: gives several handlers one topic)", label, name)
		case h.Kind() != TaskKindLeaf:
			return nil, fmt.Errorf("handler %q: a handler is one task calling one method; a block, a parallel group or import_tasks is not supported as a handler", name)
		case len(h.Notify) > 0:
			return nil, fmt.Errorf("handler %q: a handler notifying another handler is not supported; notify both handlers from the task instead", name)
		}
		names[name] = true
		targets[name] = append(targets[name], i)
		for _, topic := range h.Listen {
			topic = strings.TrimSpace(topic)
			if topic == "" {
				return nil, fmt.Errorf("handler %q: listen: has an empty topic", name)
			}
			if !containsInt(targets[topic], i) {
				targets[topic] = append(targets[topic], i)
			}
		}
	}
	for _, list := range []struct {
		tasks []Task
		where string
	}{{def.PreTasks, "pretasks"}, {def.Tasks, "tasks"}, {def.PostTasks, "posttasks"}} {
		if err := checkNotifyIn(list.tasks, list.where, targets); err != nil {
			return nil, err
		}
	}
	for _, idx := range targets {
		sort.Ints(idx)
	}
	return targets, nil
}

// checkNotifyIn holds every task under tasks to the notify and listen rules:
// listen: only on a handler, notify: only on a task that runs a method, and
// every name notify: gives reaching a handler.
func checkNotifyIn(tasks []Task, where string, targets map[string][]int) error {
	for i := range tasks {
		t := &tasks[i]
		label := fmt.Sprintf("%s[%d]", where, i)
		if t.Name != "" {
			label = fmt.Sprintf("task %q", t.Name)
		}
		if len(t.Listen) > 0 {
			return fmt.Errorf("%s: listen: belongs on a handler (handlers:), not on a task", label)
		}
		if len(t.Notify) > 0 {
			if t.Kind() != TaskKindLeaf {
				return fmt.Errorf("%s: notify: goes on each task that changes something, not on a block or a parallel group", label)
			}
			for _, name := range t.Notify {
				if len(targets[strings.TrimSpace(name)]) == 0 {
					return fmt.Errorf("%s: notify: names %q, which no handler is named or listens to", label, name)
				}
			}
		}
		for _, sub := range []struct {
			tasks []Task
			part  string
		}{{t.Block, "block"}, {t.Rescue, "rescue"}, {t.Always, "always"}, {t.Parallel, "parallel"}} {
			if err := checkNotifyIn(sub.tasks, label+"."+sub.part, targets); err != nil {
				return err
			}
		}
	}
	return nil
}

// notifies reports whether any task under tasks has a notify:.
func notifies(tasks []Task) bool {
	for i := range tasks {
		t := &tasks[i]
		if len(t.Notify) > 0 || notifies(t.Block) || notifies(t.Rescue) || notifies(t.Always) || notifies(t.Parallel) {
			return true
		}
	}
	return false
}

// containsInt reports whether list holds n.
func containsInt(list []int, n int) bool {
	for _, v := range list {
		if v == n {
			return true
		}
	}
	return false
}

// notifiedHandlers is, per device, which handlers wait to run there. It is
// written at the level barrier (Run's result scan) and read and cleared by a
// handler copy deciding whether to run on a device, from that level's
// goroutines, so a mutex guards it.
type notifiedHandlers struct {
	mu      sync.Mutex
	pending map[string]map[int]bool
}

// newNotifiedHandlers returns an empty set.
func newNotifiedHandlers() *notifiedHandlers {
	return &notifiedHandlers{pending: map[string]map[int]bool{}}
}

// notify marks each handler in indexes as waiting on device.
func (n *notifiedHandlers) notify(device string, indexes []int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.pending[device] == nil {
		n.pending[device] = map[int]bool{}
	}
	for _, i := range indexes {
		n.pending[device][i] = true
	}
}

// take reports whether handler index waits on device, and clears it, so a
// handler notified once runs once and runs again in a later section only if
// it is notified again there.
func (n *notifiedHandlers) take(device string, index int) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	if !n.pending[device][index] {
		return false
	}
	delete(n.pending[device], index)
	return true
}

// recordNotifications notifies, for each result of a task with notify: that
// ran and changed something on its device, the handlers it names. A result
// that failed, was skipped, or changed nothing notifies nothing. A loop's
// items notify through their task (BaseNodeID).
func (r *run) recordNotifications(results []NodeResult) {
	for _, nr := range results {
		if nr.Err != nil || nr.Skipped || !nr.Changed {
			continue
		}
		task := r.dag.Nodes[BaseNodeID(nr.NodeID)]
		if task == nil || len(task.Notify) == 0 {
			continue
		}
		var indexes []int
		for _, name := range task.Notify {
			indexes = append(indexes, r.dag.notifyTargets[strings.TrimSpace(name)]...)
		}
		r.notified.notify(nr.Device, indexes)
	}
}
