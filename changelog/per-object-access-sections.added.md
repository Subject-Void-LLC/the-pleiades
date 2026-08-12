Added an Access section to an organization's and a team's own page, listing the role
bindings on that record, alongside the deployment-wide Access table rather than instead
of it.

The two answer different questions. The table is the auditor's one page: who reaches
what, across everything. The section answers "who reaches this", which is the question
somebody already has while looking at the record, and it means reviewing access no
longer requires leaving the page and reconstructing which target you were on. An
operator arriving from AWX finds it where AWX puts it.

Both read the same bindings port, so the two cannot disagree about what a grant says.
A section is narrowed by scope level and id together, never by id alone: `scope_id`
carries no foreign key and its values are per level, so organization 7 and device 7 are
unrelated records that share an integer.

Lists also stopped rendering foreign keys. Teams, Inventories, Users and the Access
table now show the referenced record's name as a link to it, rather than its primary
key. Nobody has to learn that organization 1 is Network.
