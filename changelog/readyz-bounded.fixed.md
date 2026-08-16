`/readyz` no longer runs a database query per request. The endpoint is
unauthenticated and sits outside the rate limiter, so any caller who could reach
the port could drive unbounded concurrent database work and push a healthy
controller out of its load balancer; production packaging sharpened that by
pointing the container healthcheck at the same endpoint. Concurrent probes now
collapse onto one real check, and a completed answer is reused for a minimum
interval, so the cost is bounded by time instead of by caller rate. Measured
through the real handler: 4.6 million requests cost 15 dependency checks, against
one check per request before. The controller also bounds its connection pool,
which was previously unlimited.
