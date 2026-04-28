# Best Practice Sources

Central catalog of all best-practice guides referenced by review patterns.

## Tier Legend

- **S** — Primary/normative source: official language/project documentation, specifications, RFCs, standards bodies, foundation whitepapers
- **A** — Authoritative secondary: books by recognized authors, enterprise-scale style guides, original papers, dedicated project companion sites
- **B** — Trusted vendor engineering content and community-maintained checklists
- **C** — Individual-author blogs and community posts; use only when no higher-tier source exists and prefer replacing them over time

## Technology

### Go — Official References
| Tier | Source | URL | Description |
|------|--------|-----|-------------|
| S | Effective Go | https://go.dev/doc/effective_go | Official Go style and idiom guide |
| S | Go Language Specification | https://go.dev/ref/spec | Normative language reference |
| S | The Go Memory Model | https://go.dev/ref/mem | Formal happens-before/channel/mutex/atomic semantics |
| S | Go Wiki | https://go.dev/wiki/ | Canonical wiki location (migrated from github.com/golang/go/wiki) |
| S | Go Code Review Comments | https://go.dev/wiki/CodeReviewComments | 31 review topics from the Go team (migrated from github.com/golang/go/wiki) |
| S | Table-Driven Tests (Go Wiki) | https://go.dev/wiki/TableDrivenTests | Canonical table-driven-test pattern with `t.Run`, `t.Parallel()` |
| S | errors package | https://pkg.go.dev/errors | `New`, `Unwrap`, `Is`, `As`, `Join` reference |
| S | Go Proverbs | https://go-proverbs.github.io/ | Rob Pike's design principles for Go |
| S | Go Talks Index | https://go.dev/talks/ | Official index of Go project talks |
| S | Rob Pike — Go Concurrency Patterns (2012) | https://go.dev/talks/2012/concurrency.slide | Foundational Google I/O 2012 talk on goroutines, channels, select |

### Go — Official Blog
| Tier | Source | URL | Description |
|------|--------|-----|-------------|
| S | Go Blog: Concurrency Patterns | https://go.dev/blog/pipelines | Patterns for goroutines and channels |
| S | Go Blog: Contexts and structs | https://go.dev/blog/context-and-structs | Proper `context.Context` usage |
| S | Working with Errors in Go 1.13 | https://go.dev/blog/go1.13-errors | `Unwrap()`, `errors.Is`, `errors.As`, `%w` |
| S | When To Use Generics (Ian Lance Taylor) | https://go.dev/blog/when-generics | Official guidance: generics vs. interfaces |
| S | Using Subtests and Sub-benchmarks | https://go.dev/blog/subtests | `t.Run`/`b.Run`, parallelism control |
| S | Go Fuzzing | https://go.dev/doc/security/fuzz/ | Fuzz testing since Go 1.18 |

### Go — Community Style Guides & Workshops
| Tier | Source | URL | Description |
|------|--------|-----|-------------|
| S | Google Go Style Guide — Landing | https://google.github.io/styleguide/go/ | Google's normative Go style guidance (Guide + Decisions + Best Practices) |
| S | Google Go Style Guide — Guide | https://google.github.io/styleguide/go/guide | Core principles: Clarity, Simplicity, Concision, Maintainability, Consistency |
| S | Google Go Style Guide — Decisions | https://google.github.io/styleguide/go/decisions | Detailed style decisions: naming, doc-comments, errors, nil-slices, panics, goroutine lifetimes, interfaces, generics, receivers, context |
| S | Google Go Style Guide — Best Practices | https://google.github.io/styleguide/go/best-practices | Practical patterns: package organization, structured errors, `%w` vs `%v`, zero values, functional options, test helpers |
| A | Uber Go Style Guide | https://github.com/uber-go/guide/blob/master/style.md | Enterprise-scale style guide: interface rules, mutex zero values, slice/map boundaries, error wrapping, functional options |
| A | Dave Cheney — Practical Go (QCon China 2019) | https://dave.cheney.net/practical-go/presentations/qcon-china.html | Workshop on maintainable Go: identifiers, package design, API design, errors, concurrency |
| A | Dave Cheney — Practical Go (GopherCon Singapore 2019) | https://dave.cheney.net/practical-go/presentations/gophercon-singapore-2019.html | Updated practical-Go workshop including testing |

### Go — Books
| Tier | Source | URL | Description |
|------|--------|-----|-------------|
| A | 100 Go Mistakes and How to Avoid Them | https://www.manning.com/books/100-go-mistakes-and-how-to-avoid-them | Teiva Harsanyi, Manning 2022, ISBN 9781617299599 — anti-pattern catalog |
| A | Learning Go, 2nd Edition | https://www.oreilly.com/library/view/learning-go-2nd/9781098139285/ | Jon Bodner, O'Reilly 2024, ISBN 9781098139292 — idiomatic Go incl. generics |
| A | The Go Programming Language | https://www.gopl.io/ | Donovan/Kernighan, Addison-Wesley 2015, ISBN 978-0134190440 — language reference work |

### Kubernetes — Official Concepts & Reference
| Tier | Source | URL | Description |
|------|--------|-----|-------------|
| S | API Concepts | https://kubernetes.io/docs/reference/using-api/api-concepts/ | Verbs, resource URIs, watches, dry-run, ResourceVersion |
| S | Kubernetes API Conventions | https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md | CRD and API design guide (SIG Architecture) |
| S | kubernetes/enhancements (KEPs) | https://github.com/kubernetes/enhancements | Tracking repo for all Kubernetes Enhancement Proposals |
| S | KEP-753 Sidecar Containers | https://github.com/kubernetes/enhancements/blob/master/keps/sig-node/753-sidecar-containers/README.md | Native sidecars via `restartPolicy=Always` on init containers |
| S | KEP-3488 CEL Admission Control | https://github.com/kubernetes/enhancements/blob/master/keps/sig-api-machinery/3488-cel-admission-control/README.md | ValidatingAdmissionPolicy with CEL, GA in v1.30 |
| S | Configuration Best Practices | https://kubernetes.io/docs/concepts/configuration/overview/ | YAML conventions, naked pods, annotations |
| S | Recommended Labels | https://kubernetes.io/docs/concepts/overview/working-with-objects/common-labels/ | `app.kubernetes.io/*` standard labels |
| S | Labels/Annotations/Taints Reference | https://kubernetes.io/docs/reference/labels-annotations-taints/ | Well-known labels and annotations |
| S | Pods | https://kubernetes.io/docs/concepts/workloads/pods/ | Pod lifecycle, multi-container, shared network/storage |
| S | Deployments | https://kubernetes.io/docs/concepts/workloads/controllers/deployment/ | Rolling updates, rollbacks, update strategies |
| S | Pod Disruptions | https://kubernetes.io/docs/concepts/workloads/pods/disruptions/ | Voluntary/involuntary disruptions, PodDisruptionBudgets |
| S | Resource Management for Containers | https://kubernetes.io/docs/concepts/configuration/manage-resources-containers/ | Requests/limits, CPU throttling, OOM kills |
| S | Resource Quotas | https://kubernetes.io/docs/concepts/policy/resource-quotas/ | Namespace-scoped quotas for multi-tenancy |
| S | Configure Probes | https://kubernetes.io/docs/tasks/configure-pod-container/configure-liveness-readiness-startup-probes/ | Liveness, readiness, startup probes (exec, HTTP, TCP, gRPC) |
| S | Network Policies | https://kubernetes.io/docs/concepts/services-networking/network-policies/ | L3/L4 ingress/egress rules |
| S | Operator Pattern | https://kubernetes.io/docs/concepts/extend-kubernetes/operator/ | Operator definition and control-loop principles |
| S | Custom Resources | https://kubernetes.io/docs/concepts/extend-kubernetes/api-extension/custom-resources/ | CRDs vs. API aggregation, declarative APIs |

### Kubernetes — Security & Hardening
| Tier | Source | URL | Description |
|------|--------|-----|-------------|
| S | Pod Security Standards | https://kubernetes.io/docs/concepts/security/pod-security-standards/ | Privileged/Baseline/Restricted policies |
| S | Security Overview | https://kubernetes.io/docs/concepts/security/overview/ | Landing page for Kubernetes security topics |
| S | Security Checklist | https://kubernetes.io/docs/concepts/security/security-checklist/ | Baseline checklist (auth, network, pod, audit, secrets, images) |
| S | Securing a Cluster | https://kubernetes.io/docs/tasks/administer-cluster/securing-a-cluster/ | API access, kubelet access, runtime capabilities |
| S | RBAC Good Practices | https://kubernetes.io/docs/concepts/security/rbac-good-practices/ | Least-privilege, privilege-escalation risks |
| S | Multi-Tenancy | https://kubernetes.io/docs/concepts/security/multi-tenancy/ | Multi-team/multi-customer isolation |
| S | Secrets | https://kubernetes.io/docs/concepts/configuration/secret/ | Secret API, encryption at rest, external stores |
| S | Manage TLS in a Cluster | https://kubernetes.io/docs/tasks/tls/managing-tls-in-a-cluster/ | certificates.k8s.io API, CSR workflow |
| S | Validating Admission Policy | https://kubernetes.io/docs/reference/access-authn-authz/validating-admission-policy/ | CEL-based admission (GA v1.30) |
| S | CIS Kubernetes Benchmark | https://www.cisecurity.org/benchmark/kubernetes | Compliance standard for Kubernetes hardening |
| S | NIST SP 800-190 | https://csrc.nist.gov/publications/detail/sp/800-190/final | NIST Application Container Security Guide |
| S | NSA/CISA Kubernetes Hardening Guide v1.2 | https://www.cisa.gov/news-events/alerts/2022/03/15/updated-kubernetes-hardening-guide | U.S. government Kubernetes hardening guide |
| S | NSA/CISA Hardening Guide PDF | https://media.defense.gov/2022/Aug/29/2003066362/-1/-1/0/CTR_KUBERNETES_HARDENING_GUIDANCE_1.2_20220829.PDF | Direct PDF of v1.2 |
| S | BSI IT-Grundschutz APP.4.4 Kubernetes (Edition 2023) | https://www.bsi.bund.de/SharedDocs/Downloads/DE/BSI/Grundschutz/IT-GS-Kompendium_Einzel_PDFs_2023/06_APP_Anwendungen/APP_4_4_Kubernetes_Edition_2023.pdf?__blob=publicationFile&v=4 | DE-/EU-regulatorische Kubernetes-Anforderungen: Trennung von Anwendungen, Pod-Härtung, Netz-Separierung, RBAC, Secrets, Image-Quellen |
| S | BSI IT-Grundschutz SYS.1.6 Containerisierung (Edition 2023) | https://www.bsi.bund.de/SharedDocs/Downloads/DE/BSI/Grundschutz/IT-GS-Kompendium_Einzel_PDFs_2023/07_SYS_IT_Systeme/SYS_1_6_Containerisierung_Edition_2023.pdf?__blob=publicationFile&v=4 | Container-Härtung als Voraussetzung für APP.4.4 (Image-Bezug, Laufzeit-Isolation, Persistenz) |
| A | OWASP Kubernetes Top 10 (2025) | https://owasp.org/www-project-kubernetes-top-ten/ | K01 insecure workload config through K10 inadequate logging |

### Kubernetes — Operators
| Tier | Source | URL | Description |
|------|--------|-----|-------------|
| S | Kubebuilder Book | https://book.kubebuilder.io/ | Official Kubebuilder guide: API design, versioning, self-healing, GC |
| S | Kubebuilder Good Practices | https://book.kubebuilder.io/reference/good-practices | Reconciliation principles, API conventions, observability |
| S | controller-runtime | https://github.com/kubernetes-sigs/controller-runtime | Go libraries backing Kubebuilder and Operator SDK |
| S | Operator SDK Best Practices | https://sdk.operatorframework.io/docs/best-practices/best-practices/ | Operator design, CRD ownership, namespace config |
| S | Operator SDK Common Recommendations | https://sdk.operatorframework.io/docs/best-practices/common-recommendation/ | Idempotent reconciliation, single-Kind controllers |
| S | Operator SDK Designing Lean Operators | https://sdk.operatorframework.io/docs/best-practices/designing-lean-operators/ | Filtered caches, memory optimization |
| S | Operator SDK Managing Resources | https://sdk.operatorframework.io/docs/best-practices/managing-resources/ | Resource requests/limits for operators |
| S | Operator SDK Observability | https://sdk.operatorframework.io/docs/best-practices/observability-best-practices/ | Metrics, alerts, events |
| S | Operator SDK Multi-Tenancy | https://sdk.operatorframework.io/docs/best-practices/multi-tenancy/ | NetworkPolicy, ingress, namespace isolation |
| A | CNCF Operator Whitepaper | https://tag-app-delivery.cncf.io/whitepapers/operator/ | Capabilities, maturity model, security, lifecycle, design patterns |
| A | CNCF TAG App Delivery — Operator WG | https://tag-app-delivery.cncf.io/wgs/operator/ | Working group charter and artifacts |

### Kubernetes — Foundations & Specialist Tools
| Tier | Source | URL | Description |
|------|--------|-----|-------------|
| A | CNCF TAG Security | https://tag-security.cncf.io/ | Security TAG hub: working groups, publications, supply chain |
| A | Cloud Native Security Whitepaper v2 | https://github.com/cncf/tag-security/tree/main/community/resources/security-whitepaper/v2 | CNSWP v2 (May 2022) |
| A | cert-manager Docs | https://cert-manager.io/docs/ | De-facto Kubernetes TLS-cert automation (Issuer, Certificate, renewal) |
| A | External Secrets Operator | https://external-secrets.io/latest/ | Sync secrets from AWS/Vault/Azure KV/GCP SM (40+ providers) |
| B | Production Best Practices (learnkube) | https://learnkube.com/production-best-practices | Community-maintained production-readiness checklist (formerly learnk8s.io) |

### Kubernetes — Book & Companion
| Tier | Source | URL | Description |
|------|--------|-----|-------------|
| A | Kubernetes Patterns, 2nd Ed. | https://www.oreilly.com/library/view/kubernetes-patterns-2nd/9781098131678/ | Ibryam/Huss, O'Reilly 2023, ISBN 9781098131678 — foundational/deployment/lifecycle/security/config patterns |
| A | k8spatterns.com (Companion) | https://k8spatterns.com/ | Official companion site to Kubernetes Patterns book |
| A | k8spatterns Examples (GitHub) | https://github.com/k8spatterns/examples | Official example code for the book |

### Kubernetes Blog
| Tier | Source | URL | Description |
|------|--------|-----|-------------|
| S | Configuration Good Practices | https://kubernetes.io/blog/2025/11/25/configuration-good-practices/ | YAML conventions, minimal manifests, annotations, grouping |
| S | 7 Common Kubernetes Pitfalls | https://kubernetes.io/blog/2025/10/20/seven-kubernetes-pitfalls-and-how-to-avoid/ | Resource limits, probes, logging, common mistakes |
| S | The Case for Resource Limits | https://kubernetes.io/blog/2023/11/16/the-case-for-kubernetes-resource-limits/ | Predictability vs. efficiency trade-off |
| S | Start Sidecar First | https://kubernetes.io/blog/2025/06/03/start-sidecar-first/ | Startup probes to sequence sidecar init |
| S | Multi-Container Pods Overview | https://kubernetes.io/blog/2025/04/22/multi-container-pods-overview/ | Sidecar, ambassador, adapter patterns |
| S | Protect Pods with PriorityClass | https://kubernetes.io/blog/2023/01/12/protect-mission-critical-pods-priorityclass/ | PriorityClass for critical workload eviction protection |
| S | 11 Ways Not to Get Hacked | https://kubernetes.io/blog/2018/07/18/11-ways-not-to-get-hacked/ | RBAC, TLS, network policies, non-root, image scanning |
| S | Securing Admission Controllers | https://kubernetes.io/blog/2022/01/19/secure-your-admission-controllers-and-webhooks/ | Webhook security hardening |
| S | NSA/CISA Hardening Guidance Analysis | https://kubernetes.io/blog/2021/10/05/nsa-cisa-kubernetes-hardening-guidance/ | Commentary on NSA guide recommendations |
| S | Security Best Practices for Deployment | https://kubernetes.io/blog/2016/08/security-best-practices-kubernetes-deployment/ | Image scanning, authorized images, access control |
| S | Cloud Native Security | https://kubernetes.io/blog/2020/11/18/cloud-native-security-for-your-clusters/ | 4C security model |
| S | Using Finalizers to Control Deletion | https://kubernetes.io/blog/2021/05/14/using-finalizers-to-control-deletion/ | Finalizer lifecycle, cleanup |
| S | Server Side Apply | https://kubernetes.io/blog/2022/10/20/advanced-server-side-apply/ | Field ownership, conflict handling |
| S | Annotating Services for Humans | https://kubernetes.io/blog/2021/04/20/annotating-k8s-for-humans/ | a8r.io annotation standard |
| S | Non-root Containers and Devices | https://kubernetes.io/blog/2021/11/09/non-root-containers-and-devices/ | Non-root with device access |
| S | Container Design Patterns | https://kubernetes.io/blog/2016/06/container-design-patterns/ | Foundational multi-container patterns |
| S | Principles of Container App Design | https://kubernetes.io/blog/2018/03/principles-of-container-app-design/ | Container-native application design |
| S | Enforce CRD Immutability with CEL | https://kubernetes.io/blog/2022/09/29/enforce-immutability-using-cel/ | CEL transition rules for immutable CRD fields |
| S | Validating Admission Policy (GA) | https://kubernetes.io/blog/2024/04/24/validating-admission-policy-ga/ | Native policy enforcement without webhooks |

### Kubernetes — Community & Anti-Patterns (legacy references)
Lower-tier sources retained where no S/A-tier replacement covers the specific angle. Flagged for gradual replacement.

| Tier | Source | URL | Description |
|------|--------|-----|-------------|
| C | Nutanix: K8s Anti-Patterns | https://www.nutanix.dev/2026/04/01/avoid-these-kubernetes-anti-patterns-a-guide-for-virtualization-professionals/ | Node snapshots, manual SSH, static IPs, cluster-admin everywhere |
| C | DevOps.dev: K8s Anti-Patterns Field Guide | https://blog.devops.dev/kubernetes-anti-patterns-a-field-guide-to-cluster-sabotage-a0c8e8969824 | `latest` tag, root containers, missing limits, secrets in ConfigMaps |
| C | OneUptime: GitOps Anti-Patterns | https://oneuptime.com/blog/post/2026-02-26-gitops-anti-patterns/view | Secrets in Git, manual kubectl alongside GitOps |
| C | Axelspire: Certificate Anti-Patterns | https://axelspire.com/blog/localhost-is-not-an-identity-certificate-anti-patterns-in-multi-cluster-kubernetes/ | Localhost SANs, throwaway self-signed CAs |
| C | 7 K8s Anti-Patterns That Hurt in Production | https://medium.com/devops-ai-decoded/7-kubernetes-anti-patterns-that-hurt-in-production-91682dbccc5b | Liveness probe misuse, cascading restart failures |
| C | Kubernetes Operators Deep Dive: Internals | https://dev.to/piyushjajoo/kubernetes-operators-a-deep-dive-into-the-internals-221m | Reconciliation, CRD design, caching, webhooks |

### Python
| Tier | Source | URL | Description |
|------|--------|-----|-------------|
| S | PEP 8 — Style Guide for Python Code | https://peps.python.org/pep-0008/ | Canonical Python code style |
| S | PEP 257 — Docstring Conventions | https://peps.python.org/pep-0257/ | Triple-quote, one-line summary, blank line, body |
| S | PEP 287 — reStructuredText Docstring Format | https://peps.python.org/pep-0287/ | reST as the default docstring markup |
| S | PEP 484 — Type Hints | https://peps.python.org/pep-0484/ | Specification for type annotations |
| S | PEP 3134 — Exception Chaining | https://peps.python.org/pep-3134/ | Exception chain preservation with `from` |
| S | mypy Documentation | https://mypy.readthedocs.io/ | Static type checker reference |
| A | Google Python Style Guide — Docstrings | https://google.github.io/styleguide/pyguide.html#38-comments-and-docstrings | Google-style docstring sections (Args, Returns, Raises) |
| A | NumPy Docstring Standard | https://numpydoc.readthedocs.io/en/latest/format.html | NumPy-style docstring sections, widely used in the scientific stack |

### Helm — Official Documentation
| Tier | Source | URL | Description |
|------|--------|-----|-------------|
| S | Helm Docs Home | https://helm.sh/docs/ | Entry point for all Helm documentation |
| S | Helm Chart Best Practices (Index) | https://helm.sh/docs/chart_best_practices/ | Official chart authoring guide |
| S | Chart Conventions | https://helm.sh/docs/chart_best_practices/conventions/ | Naming (lowercase/dashes), SemVer, YAML formatting |
| S | Values Best Practices | https://helm.sh/docs/chart_best_practices/values/ | camelCase naming, flat vs. nested, type clarity, `--set` design |
| S | Templates Best Practices | https://helm.sh/docs/chart_best_practices/templates/ | Directory structure, template namespacing, whitespace, comments |
| S | Labels and Annotations | https://helm.sh/docs/chart_best_practices/labels/ | Label vs. annotation, 7 standard labels, hook annotations |
| S | Dependencies Best Practices | https://helm.sh/docs/chart_best_practices/dependencies/ | Version ranges, HTTPS URLs, conditions/tags |
| S | CRD Best Practices | https://helm.sh/docs/chart_best_practices/custom_resource_definitions/ | `crds/` directory vs. separate charts, upgrade/delete limits |
| S | RBAC Best Practices | https://helm.sh/docs/chart_best_practices/rbac/ | ServiceAccount/Role/ClusterRole/Bindings, `rbac.create` |
| S | Pods and PodTemplates | https://helm.sh/docs/chart_best_practices/pods/ | Fixed image tags/SHA, pull policy, selectors |
| S | Charts (Topics) | https://helm.sh/docs/topics/charts/ | Chart structure, Chart.yaml fields, schema files, repositories |
| S | Chart Template Guide | https://helm.sh/docs/chart_template_guide/ | Built-in objects, pipelines, flow control, named templates, files, subcharts, debugging |
| S | Chart Template Function List | https://helm.sh/docs/chart_template_guide/function_list/ | 18 function categories: Logic, String, Type, Regex, Crypto, Date, Dict, Encoding, Lists, Math, SemVer, URL, UUID |
| S | Values Files (Template Guide) | https://helm.sh/docs/chart_template_guide/values_files/ | Hierarchy (values.yaml < parent < user-supplied < `--set`), flat-tree recommendation, delete via `null` |
| S | Named Templates | https://helm.sh/docs/chart_template_guide/named_templates/ | `_helpers.tpl`, `define`/`include`/`template`, scope passing |
| S | Subcharts and Globals | https://helm.sh/docs/chart_template_guide/subcharts_and_globals/ | Subchart isolation, parent override, `Values.global` |
| S | Chart Hooks | https://helm.sh/docs/topics/charts_hooks/ | 9 hook types (install/delete/upgrade/rollback/test), weight, delete-policy |
| S | Chart Tests | https://helm.sh/docs/topics/chart_tests/ | `helm.sh/hook: test`, `helm test <RELEASE>`, tests under `templates/tests/` |
| S | Library Charts | https://helm.sh/docs/topics/library_charts/ | Chart primitives, reuse, distinction from application charts |
| S | helm lint Command | https://helm.sh/docs/helm/helm_lint/ | `--strict`, `--with-subcharts`, `--kube-version` |
| S | Version Skew / Compatibility | https://helm.sh/docs/topics/version_skew/ | Helm-Kubernetes compatibility (n-3) |
| S | Provenance and Integrity | https://helm.sh/docs/topics/provenance/ | PGP signing, `.prov` files, SHA256, Keybase, Sigstore |
| S | Advanced Helm Techniques | https://helm.sh/docs/topics/advanced/ | Post-renderer warnings, storage-backend sensitivity, SQL backend |
| S | Helm 4 Overview | https://helm.sh/docs/overview/ | Breaking changes, Wasm plugins, kstatus, OCI digests, multi-doc values, SSA |
| S | Helm 4 Changelog | https://helm.sh/docs/changelog/ | HIP-0023 (SSA), HIP-0026 (plugins) |
| S | Helm 4 Released (Blog) | https://helm.sh/blog/helm-4-released/ | v4.0.0 announcement, v3 support timeline |
| S | Helm Security Process | https://helm.sh/community/security/ | Vulnerability reporting (cncf-helm-security@lists.cncf.io), CVE process |

### Helm — Projects & Vendor Docs
| Tier | Source | URL | Description |
|------|--------|-----|-------------|
| S | helm/helm (GitHub) | https://github.com/helm/helm | Source-of-truth repository |
| S | Helm Releases (GitHub) | https://github.com/helm/helm/releases | Release timeline and notes |
| S | helm/chart-testing (ct) | https://github.com/helm/chart-testing | CLI for chart linting and PR testing |
| A | helm-unittest | https://github.com/helm-unittest/helm-unittest | BDD-style unit test framework as Helm plugin (snapshot testing) |
| A | Artifact Hub Helm Charts Docs | https://artifacthub.io/docs/topics/repositories/helm-charts/ | Repository setup, annotations, OCI registry support |
| B | Bitnami Production-Ready Charts | https://techdocs.broadcom.com/us/en/vmware-tanzu/bitnami-secure-images/bitnami-secure-images/services/bsi-doc/apps-tutorials-production-ready-charts-index.html | Non-root containers, ConfigMap config, logging, exporters |
| B | Bitnami Hardening Helm Charts | https://techdocs.broadcom.com/us/en/vmware-tanzu/bitnami-secure-images/bitnami-secure-images/services/bsi-doc/apps-tutorials-best-practices-hardening-charts-index.html | Container hardening, CVE scanning |

### Helm — Book
| Tier | Source | URL | Description |
|------|--------|-----|-------------|
| A | Learning Helm | https://www.oreilly.com/library/view/learning-helm/9781492083641/ | Butcher/Farina/Dolitsky (Helm maintainers), O'Reilly 2021, ISBN 9781492083658 |

## Documentation

| Tier | Source | URL | Description |
|------|--------|-----|-------------|
| S | Diátaxis Framework | https://diataxis.fr | Four-mode documentation framework (Tutorial, How-To, Reference, Explanation) |
| S | Google Developer Documentation Style Guide | https://developers.google.com/style | Google's normative developer-doc style guide |
| S | Microsoft Writing Style Guide | https://learn.microsoft.com/style-guide/welcome/ | Microsoft's developer-and-product writing style guide |
| S | Write the Docs — Documentation Guide | https://www.writethedocs.org/guide/ | Community guide on documenting software |
| S | Go Doc Comments | https://go.dev/doc/comment | Official Go doc-comment reference (replaces older godoc rules) |
| S | PEP 257 — Docstring Conventions | https://peps.python.org/pep-0257/ | Canonical Python docstring conventions |
| S | PEP 287 — reStructuredText Docstring Format | https://peps.python.org/pep-0287/ | reST as the default Python docstring markup |
| A | ISO/IEC/IEEE 26515:2018 | https://www.iso.org/standard/72088.html | Developing user documentation in agile environments |
| A | Docs for Developers | https://www.apress.com/gp/book/9781484272169 | Bhatti et al., Apress 2021 — practical doc engineering |



| Tier | Source | URL | Description |
|------|--------|-----|-------------|
| A | Clean Code | - | Robert C. Martin: SOLID principles, clean architecture |
| A | Clean Architecture | - | Robert C. Martin: dependency rules, boundaries |
| A | Extreme Programming Explained | - | Kent Beck: TDD, YAGNI, simple design |
| A | The Pragmatic Programmer | https://pragprog.com/titles/tpp20/the-pragmatic-programmer-20th-anniversary-edition/ | Hunt/Thomas: DRY principle, pragmatic approaches |
| A | Test Driven Development: By Example | - | Kent Beck: red-green-refactor cycle |
| A | Growing Object-Oriented Software, Guided by Tests | - | Freeman/Pryce: outside-in TDD |
| A | BDD in Action | - | John Ferguson Smart: behavior specifications |
| A | Design Patterns | - | Gamma et al. (Gang of Four): classic OOP patterns |
| A | Agile Software Development: Principles, Patterns, and Practices | - | Robert C. Martin: SOLID deep dive |
| A | A Behavioral Notion of Subtyping | - | Barbara Liskov, Jeannette Wing: original LSP paper |
