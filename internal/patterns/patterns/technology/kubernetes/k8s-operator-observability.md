# Review Pattern: Kubernetes Operator Observability

**Review-Area**: quality
**Detection-Hint**: Operators without metrics endpoints, missing health/readiness probes, alerts without runbooks, high-cardinality metric labels, missing status conditions on CRs
**Severity**: WARNING
**Category**: technology
**Applies-When**: kubernetes
**Sources**: Kubebuilder Good Practices (https://book.kubebuilder.io/reference/good-practices), CNCF Operator White Paper (https://tag-app-delivery.cncf.io/whitepapers/operator/), Operator SDK Observability Best Practices (https://sdk.operatorframework.io/docs/best-practices/observability-best-practices/)

## What to check

### Metrics
1. Operators must expose health and key performance metrics (throughput, latency, availability, errors, capacity)
2. Metric naming must follow the convention: `<operator_name>_<entity>_<metric_name>` for discoverability in Prometheus/Grafana
3. Use Prometheus base units for suffixes, `_total` suffix only for monotonically increasing counters
4. Be cautious with metric labels — high-cardinality labels (user IDs, unbounded sets) explode storage
5. Include `namespace` label for resource-type metrics (pod, container) for unique identification

### Alerts
6. Alert names must be CamelCase with a component prefix (e.g. `AlertmanagerFailedReload`)
7. Every alert needs: `severity` label (critical/warning/info), `summary` and `description` annotations
8. Critical alerts: impending data/service loss, ~5 minute response — reserve for cluster-wide threats only
9. Warning alerts: needs timely action, ~60 minute response — most alerts should be this severity
10. Info alerts: awareness only — consider Kubernetes events as alternative
11. Every alert should have a `runbook_url` linking to investigation/resolution procedures

### Events and Status
12. Custom Resources should emit events documenting significant operations (audit trail)
13. Keep monitoring code in a dedicated directory, separate from core operator logic
14. Expose telemetry about remediation actions taken — what was detected, what was corrected, and duration

### Work Queue Metrics
15. Export work queue depth, reconciliation duration (especially p99 latency), and error counts as standard controller metrics
16. Include reconciliation context (namespace, name, generation, phase) in structured log entries for correlation

### Testing
17. Validate that alerts include all mandatory fields and that runbook URLs are accessible
18. E2E tests should verify alerts don't fire incorrectly (no noise) and do fire under defined conditions
19. Simulate pod, configuration, storage, and network failures as part of operator testing

## Why it matters

An operator without observability is a black box in production. When it misbehaves, there are no metrics to diagnose the issue, no alerts to notify oncall, and no runbook to guide recovery. High-cardinality labels silently explode Prometheus storage costs.
