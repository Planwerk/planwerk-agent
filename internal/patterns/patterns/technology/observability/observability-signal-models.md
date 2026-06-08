# Review Pattern: Service-Level Signals (Golden Signals / RED / USE)

**Review-Area**: quality
**Detection-Hint**: Dashboards or alerts focused on infrastructure (CPU%, memory) without user-visible latency/error metrics, services with no defined SLI, alerts that don't map to user impact, RED metrics missing for HTTP/RPC services, USE metrics missing for resource-bound systems, latency reported as average instead of percentiles, error rate measured separately from success rate
**Severity**: WARNING
**Category**: technology
**Sources**: Google SRE Book — Monitoring Distributed Systems (https://sre.google/sre-book/monitoring-distributed-systems/), RED Method (Tom Wilkie) (https://www.weave.works/blog/the-red-method-key-metrics-for-microservices-architecture/), USE Method (Brendan Gregg) (https://www.brendangregg.com/usemethod.html), Prometheus Documentation (https://prometheus.io/docs/)

## What to check

### Pick the right model for what you are measuring
1. Use the **Four Golden Signals** for any user-facing service: **Latency**, **Traffic**, **Errors**, **Saturation**. These are the four numbers that answer "is the service healthy from the outside" and they map directly to SLIs/SLOs
2. Use **RED** (Rate, Errors, Duration) for request-driven services — HTTP/gRPC servers, queue consumers, RPC handlers. RED is Golden Signals minus saturation, focused on per-service request shape
3. Use **USE** (Utilization, Saturation, Errors) for resources — CPU, memory, disk, network, pools, queues. USE answers "is the resource the bottleneck" and is the dual of RED. A complete dashboard usually has RED for the service plus USE for its expensive resources

### Latency / Duration (what users feel)
4. Latency is reported as a histogram, not an average — averages hide tail latency and cannot be aggregated meaningfully across instances. Default to plotting p50, p90, p99 (or higher) computed from the histogram in PromQL/equivalent
5. Latency is measured at the boundary the user cares about — for an HTTP service, that is the time from request received to response sent, not internal processing time. Include serialization, validation, and middleware
6. Successful requests and errors are measured separately when computing latency — error responses are often much faster (auth failures, validation rejects) and dragging them into the same series hides real success-path regressions

### Traffic / Rate
7. Traffic is requests-per-second (or messages, jobs, transactions — pick the unit that matches the service contract). Drop in traffic is itself a signal: services with no requests during business hours are failing silently
8. Break traffic down by the dimensions that matter for capacity (endpoint, customer/tenant tier, method) but bound the cardinality (see metrics-naming pattern). Traffic per-customer-id is almost always wrong

### Errors
9. Error rate is an explicit metric, derived from a counter with an outcome label (`status="error"` / `status="success"`), not a separate `errors_total` series that must be kept in sync
10. Distinguish client errors (4xx, validation rejects) from server errors (5xx, panics, timeouts) — they have different action implications. Alerts fire on server-side error rate; client errors are an SLI but rarely a page
11. Define what counts as an error per service explicitly. Is a 404 an error? Is a deliberate cancellation? Document the answer where the SLO is documented

### Saturation / Utilization
12. Saturation is the degree to which the resource has more work queued than it can serve — queue depth, request concurrency vs. limit, connection pool wait time. It is a leading indicator: saturation rises before latency degrades
13. Utilization is the fraction of capacity in active use (CPU%, memory%, pool used / pool total). High utilization plus low saturation is healthy; rising saturation is what predicts collapse
14. For each resource, identify and surface the natural saturation metric: queue length for queues, accept-queue depth for sockets, pool wait time for pools, GC pause time for runtimes, lock-wait for mutexes

### Alerting and SLOs
15. Alerts page on user-visible symptoms, not causes — high error rate or latency, not "CPU > 80%". CPU is a USE-side debugging metric, not a customer signal
16. SLOs are expressed against the same SLIs that the dashboards plot. Burn-rate alerts (multi-window, multi-burn-rate) come from the SLI; ad-hoc threshold alerts on raw metrics drift from what was promised
17. Every alert has a runbook URL pointing to investigation steps for the failing signal — see also `Kubernetes Operator Observability` for the alert-format details

## Why it matters

Most production observability problems are not about missing telemetry — they are about telemetry that doesn't answer the question being asked at 3 AM. The Golden Signals, RED, and USE are not three competing models; they are three lenses for three questions. "Is my service healthy from the outside" → Golden Signals. "Are my requests being handled correctly" → RED. "Is my resource the bottleneck" → USE. The Google SRE book formalized the user-facing signals; Tom Wilkie distilled the request-shape view at Weaveworks; Brendan Gregg built the resource-shape view from systems performance work at Sun and Netflix. They overlap by design. A service that lacks any of the three has predictable blind spots: a CPU-heavy dashboard can be all green while users see timeouts, a request-only dashboard misses the noisy neighbor saturating disk. Reviewing instrumentation against these models catches the gaps before they become unanswerable questions during an incident.
