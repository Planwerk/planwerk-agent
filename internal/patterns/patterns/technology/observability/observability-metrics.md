# Review Pattern: Prometheus Metric Naming and Cardinality

**Review-Area**: quality
**Detection-Hint**: Metric names without unit suffix or with non-base units (`_ms`, `_kb`), counters without `_total` suffix, mixedCase or kebab-case names, label values that include user/request/trace IDs or full URLs, gauges where counters belong (or vice versa), metrics derived in client code that should be derived in PromQL, instrumentation in hot paths without sampling
**Severity**: WARNING
**Category**: technology
**Sources**: Prometheus Documentation (https://prometheus.io/docs/), Prometheus Naming Best Practices (https://prometheus.io/docs/practices/naming/), Prometheus Instrumentation Best Practices (https://prometheus.io/docs/practices/instrumentation/), OpenTelemetry Semantic Conventions (https://opentelemetry.io/docs/specs/semconv/)

## What to check

### Naming
1. Metric names use lowercase snake_case with a domain prefix: `<namespace>_<subsystem>_<name>_<unit>` (e.g. `http_requests_total`, `process_cpu_seconds_total`, `myapp_orders_processed_total`). The prefix scopes the metric to its owner so cross-team conflicts are impossible
2. Counters end in `_total` (`http_requests_total`, not `http_requests` or `http_requests_count`) — this is what `rate()` and `increase()` require to recognize the type
3. Units are SI base units appended as suffix: `_seconds` (not `_ms` or `_minutes`), `_bytes` (not `_kb`), `_ratio` (0–1), `_celsius`, `_meters`. Convert at instrumentation time, not at query time
4. Histogram metrics use the base name plus `_bucket`/`_sum`/`_count` (Prometheus client libraries emit these automatically); name the metric for what it measures (`http_request_duration_seconds`)
5. Custom metric names follow the same rules as the OTel/Prometheus standard — but where a standard convention exists (HTTP, DB, messaging), use it. Inventing `myapp_http_calls` when `http_client_request_duration_seconds` exists fragments dashboards

### Instrument selection
6. Counter for monotonically increasing values that reset only on restart (request count, errors, bytes processed). A counter that decreases is a bug — use a gauge or split into separate counters
7. Gauge for values that go up and down (queue depth, in-flight requests, current memory). Don't store a counter in a gauge variable — `rate()` won't work
8. Histogram for distributions you query with quantiles or buckets (latency, request size). Define buckets that span the realistic range; the default Prometheus buckets are tuned for HTTP latency and may not fit your data
9. Summary only when client-side quantiles are required and aggregation across instances isn't needed — quantiles from a Summary cannot be re-aggregated in PromQL. Histograms are usually the right answer

### Labels and cardinality
10. Labels are keys with bounded, low-cardinality value sets — `method`, `status_code`, `endpoint` (the route template, not the URL), `outcome` (success/error). Total time-series count = product of label value counts
11. Never use unbounded values as labels: user ID, request ID, full URL, IP address, raw query string, error message text. These produce a new time series per value and exhaust the TSDB
12. The `namespace`/`pod`/`instance` labels are added by Prometheus on scrape — don't duplicate them in application metrics
13. Standardize the same label name across metrics in a service (`endpoint` everywhere, not `endpoint` here and `path` there) so dashboards compose
14. Histograms multiply cardinality by the bucket count — be deliberate about per-label bucket counts

### Instrumentation patterns
15. Errors are observed as a counter with an outcome/status label, not as a gauge or as a separate metric per error class. `http_requests_total{status_code="500"}` lets PromQL compute error rates without a parallel `http_errors_total` to keep in sync
16. Resource utilization metrics (`*_seconds`, `*_bytes`, `*_total`) follow the USE method shape so they slot into existing dashboards and alerts without translation
17. Histograms for latency are at the request boundary, not inside the work — measure user-visible latency, not subsystem timing (which is a span attribute, not a metric)
18. Instrumentation in hot paths must be cheap: prefer counters and atomic increments over locks; avoid per-request object allocations in metrics code

### Exposition
19. Metrics are exposed via the standard `/metrics` endpoint in Prometheus exposition format (or OTLP via collector) — never via a custom HTTP shape
20. The exposition endpoint is unauthenticated by default — restrict it via network policy or a dedicated auth scheme; never publish it on the public-facing port

## Why it matters

A metric is cheap to add and expensive to fix once it's in dashboards, alerts, and cross-team queries. Naming drift (`_ms` vs `_seconds`, missing `_total`, inconsistent label keys) means PromQL queries silently disagree with each other and operators waste time reconciling. High-cardinality labels are the failure mode that ends a Prometheus deployment: a single metric with `user_id` as a label can grow to millions of time series, exhaust ingestion budget, and take down the TSDB without warning — and the cost is paid by every team sharing that backend, not just the one that introduced the label. The Prometheus naming and instrumentation guides exist because every organization runs into the same set of problems independently. Reviewing metric definitions against the conventions catches them at PR time, when the fix is a rename in one file rather than a coordinated migration across every dashboard and alert that consumes the metric.
