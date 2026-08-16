The Helm chart no longer collapses its four workloads into one name. A release name of
49 to 53 characters, all legal to Helm, used to truncate the controller, the runner, the
PostgreSQL StatefulSet and the NATS StatefulSet to the same string, so the objects
overwrote each other on install. Object names are now built by cutting the release-scoped
prefix first and appending the component second, and `make helm-lint` renders the chart at
release-name lengths 1, 20, 48, 49, 52 and 53 and requires every workload name to stay
distinct.
