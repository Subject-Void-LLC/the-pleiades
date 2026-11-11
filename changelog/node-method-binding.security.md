The Controller now stores a Runner's facts and journal rows under the method it dispatched for each node, read from
the runbook graph it records on the job at its first fan-out or from a rollback job's plan, rather than the method the
batch names. A Runner could otherwise name a built-in method for a node that ran an external Collection's method and
so write built-in facts, or a journal row's undo, as that method's. A batch naming another method is stored under the
node's with a warning, a journal row stored that way keeps no Collection identity, program or undo, and a node the
Controller cannot resolve (a job from before this release, a playbook job, a node the record lacks) is stored with its
method unknown and without the facts a built-in holds. Both consumers clear, with a warning, a Collection identity that
cannot be its method's by one rule, and say what they changed only once a batch is stored. A job keeps the graph it
started with on both sides, compared by a frozen digest (scheme `graph/1`) of what each node runs, `rollback:` lists
included: devices a reclaimed fan-out or a forks window reaches after its graph changed are failed, naming the change,
and a Runner whose copy of the runbook digests otherwise runs nothing on that device and fails it. Such a dispatch is
payload version 2, which a Runner of an earlier release refuses rather than run unheld, so upgrade Runners with the
Controller; rollbacks and playbooks stay at version 1.
