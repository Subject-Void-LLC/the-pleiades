Added the Contacts view: who is accountable for an organization or a team, and how to
reach them. The list is the deployment-wide page an access review reads, and each
organization and team now carries a Contacts section listing its own.

The entity, its store and its five API endpoints already shipped. What was missing was
any page that showed them, so the question the record exists to answer, which tenants
have nobody named against them, could be written over HTTP and read nowhere.

A contact answers for exactly one record, never both and never neither. The form offers
one control listing organizations and teams together rather than two selects, so neither
invalid state is something a submission can express: a select carries one value, the
field is required, and a value that is not one of the offered options is refused before
any handler sees it.

A contact's owner is also set once. It is chosen when the contact is created, absent
from the edit form entirely, and a submission carrying it to an update is refused rather
than ignored. Moving a contact between owners is indistinguishable from deleting one and
creating another, and an accountability record that quietly changed what it was
accountable for would defeat the attestation beside it.
