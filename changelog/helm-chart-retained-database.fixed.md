The Helm chart refuses to install onto a PostgreSQL data volume its credentials cannot
open, instead of installing something that crash-loops. `helm uninstall` leaves the
database claim behind on purpose, and PostgreSQL only applies a username, database name
and password when it initializes an empty directory, so a reinstall with a different
password used to bring up a healthy database the controller could never authenticate to,
with nothing naming the cause. The refusal names the retained claim and both choices:
reinstall with the values that volume was created with, which destroys nothing, or delete
the claim, which destroys the database.

Two more rendering mistakes are refused as well. An `ingress.hosts[].paths[]` entry
without a `pathType` no longer renders, since `networking.k8s.io/v1` requires one and the
API server rejects the object. An enabled `podDisruptionBudget` that sets both
`minAvailable` and `maxUnavailable`, or neither, is refused rather than rendering a budget
Kubernetes rejects or, worse, no budget at all for a release whose values say disruption
protection is on. A budget of `0` now counts as a real answer on either field.
