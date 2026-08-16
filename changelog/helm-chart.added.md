`helm/the-pleiades` is a real Helm chart. It deploys the controller and a fleet of
runners as separate workloads, with PostgreSQL and NATS as in-chart StatefulSets you
can turn off (`postgresql.enabled`, `nats.enabled`) to point at a managed service
instead, plus an ingress, an autoscaler, disruption budgets and a `values.schema.json`
that turns a mistyped key into an error rather than a silently ignored setting. It has
no subchart dependencies, so `helm package` produces one artifact that installs with no
network. It replaces the `helm create` scaffold, which deployed nginx.
