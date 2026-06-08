# Review Pattern: OpenTelemetry Instrumentation

**Review-Area**: quality
**Detection-Hint**: Bespoke tracing libraries instead of OpenTelemetry SDKs, missing context propagation across service boundaries, hand-rolled span/trace IDs, attribute names invented per service instead of following semantic conventions, telemetry export hardcoded to a single backend, exporters without sampling/batching configuration, logs not correlated to traces
**Severity**: WARNING
**Category**: technology
**Sources**: OpenTelemetry Documentation (https://opentelemetry.io/docs/), OpenTelemetry Specification (https://github.com/open-telemetry/opentelemetry-specification), OpenTelemetry Semantic Conventions (https://opentelemetry.io/docs/specs/semconv/), OTLP — OpenTelemetry Protocol (https://github.com/open-telemetry/opentelemetry-proto)

## What to check

### SDK and API choice
1. Use the official OpenTelemetry SDK for the language (Go, Python, Java, Node, etc.) and the OTel API for instrumentation. Vendor-locked tracing libraries (Datadog APM, NewRelic agents) should sit behind the OTel API or export OTLP — application code does not bind to a vendor SDK
2. Auto-instrumentation libraries (OTel contrib for HTTP frameworks, gRPC, DB drivers) cover the boilerplate; reach for manual spans only for business operations the auto-instrumentation cannot see
3. Initialize the SDK once at process startup with explicit `Resource` attributes (`service.name`, `service.version`, `deployment.environment`, `service.instance.id`) — every signal is keyed on these

### Context propagation
4. Trace context propagates across every service boundary using W3C Trace Context (`traceparent`, `tracestate`) — HTTP servers extract the incoming context, clients inject it on outbound calls. A trace that ends at the service boundary is not a distributed trace
5. Propagate context through async boundaries (queues, scheduled jobs, goroutines) — preserve the trace by passing `context.Context` (Go) or the language equivalent through the call chain. Spawning a goroutine that drops the parent context loses the link
6. Baggage (W3C Baggage header) carries cross-cutting metadata (tenant ID, request priority); use it sparingly — every value is propagated to every downstream and emitted on every span

### Spans
7. Span names describe the operation, not the instance: `GET /users/{id}` not `GET /users/42`. The dynamic part goes into attributes (`http.route`, `db.operation`)
8. Span kinds are set correctly: `SERVER` (incoming request), `CLIENT` (outbound call), `INTERNAL` (in-process), `PRODUCER`/`CONSUMER` (messaging). Wrong kinds break service-map visualizations
9. Errors are recorded with `span.SetStatus(Error, "...")` and `span.RecordException(err)`; non-error outcomes set `Ok` status. Don't paper over errors by leaving status `Unset`
10. Span attributes follow OTel semantic conventions — `http.request.method`, `http.response.status_code`, `db.system`, `db.statement`, `messaging.system`, `rpc.service`, `genai.system`. Inventing your own names defeats correlation across services

### Metrics
11. Application metrics use the OTel Metrics API with explicit instrument types: `Counter` (monotonic), `UpDownCounter` (signed), `Histogram` (latency/size), `Gauge` (instantaneous). The right instrument enables the right backend aggregation
12. Metric names and attributes follow semantic conventions where they exist (`http.server.request.duration`, `system.cpu.utilization`); custom metrics use lowercase dot-separated names (`orders.processed.total`)
13. Cardinality is bounded — never use unbounded values (user IDs, request IDs, full URLs) as attribute keys. Backends fail open on cardinality and bills explode silently

### Logs
14. Logs are structured (JSON or logfmt) with the active trace ID and span ID embedded as fields (`trace_id`, `span_id`) — the OTel SDK has helpers in every language. Without correlation, traces and logs are two silos
15. Log severity uses the standard set; map application levels to OTel `SeverityNumber` consistently
16. Avoid logging the same fact twice (once as a span event, once as a log line, once as an event in a metric exemplar) — pick one signal per fact

### Sampling and export
17. Sampling is configured at the SDK (`ParentBased(TraceIdRatio)`, tail-based at the collector) — never sample by dropping spans in application code. Head sampling decisions must be made before the root span starts
18. Export uses OTLP (gRPC or HTTP) to an OTel Collector — applications do not push to vendor backends directly. The collector handles batching, retries, transformation, and backend fan-out
19. Exporters use batching (`BatchSpanProcessor`, `BatchLogProcessor`) with configured limits — synchronous exports are a denial-of-service vector for the application
20. The collector pipeline is itself code — versioned, tested, and reviewed. The processor chain (resourcedetection, batch, attributes, tail_sampling) is part of the observability contract

## Why it matters

OpenTelemetry is the CNCF-backed convergence standard for traces, metrics, and logs — the single project where vendors, cloud providers, and language teams are aligned. Adopting OTel decouples the application from any specific observability vendor: the same instrumentation can fan out to Prometheus, Grafana, Datadog, Honeycomb, or whatever the SRE team migrates to next year. The semantic conventions are the part that makes telemetry actually useful at scale: when every service emits `http.request.method` rather than `httpMethod` / `method` / `verb`, dashboards and alerts compose across services without per-service translation. Code that reaches for a vendor SDK directly, invents its own attribute names, or drops trace context across boundaries undoes every benefit OTel provides — distributed tracing collapses back to per-service guesswork. Reviewing telemetry code against the spec and conventions catches these issues while they are local; afterwards, retrofitting consistency across an instrumented fleet is enormously expensive.
