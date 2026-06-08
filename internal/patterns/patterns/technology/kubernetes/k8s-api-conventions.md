# Review Pattern: Kubernetes API Conventions

**Review-Area**: architecture
**Detection-Hint**: Custom Resource Definitions, API types, controller code with non-standard status patterns
**Severity**: WARNING
**Category**: technology
**Applies-When**: kubernetes
**Sources**: Kubernetes API Conventions (https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md), API Changes Guidelines (https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api_changes.md), Custom Resources (https://kubernetes.io/docs/concepts/extend-kubernetes/api-extension/custom-resources/), Recommended Labels (https://kubernetes.io/docs/concepts/overview/working-with-objects/common-labels/), Using Finalizers to Control Deletion (https://kubernetes.io/blog/2021/05/14/using-finalizers-to-control-deletion/), Enforce CRD Immutability with CEL (https://kubernetes.io/blog/2022/09/29/enforce-immutability-using-cel/), Kubernetes Patterns 2nd Ed. (https://www.oreilly.com/library/view/kubernetes-patterns-2nd/9781098131678/)

## What to check

1. CRD names follow the convention: `<plural>.<group>` (e.g., `crontabs.stable.example.com`)
2. Status subresource is used for status updates (not the main resource endpoint)
3. Conditions follow standard patterns: `type`, `status` (True/False/Unknown), `reason`, `message`, `lastTransitionTime`
4. Labels follow the recommended format: `app.kubernetes.io/name`, `app.kubernetes.io/version`, etc.
5. Finalizers are used correctly: added before the resource needs protection, removed after cleanup
6. Finalizer names must use domain-qualified format (e.g. `myapp.example.com/cleanup`) — orphaned finalizers with no managing controller block deletion indefinitely
7. Every finalizer must have an active controller that removes it after cleanup — "dead" finalizers are a common source of stuck resources
8. API versions follow stability levels: `v1alpha1` → `v1beta1` → `v1`
9. Immutable fields in CRDs should use CEL transition rules (`x-kubernetes-validations` with `oldSelf`) to prevent mutation after creation

## Why it matters

Following API conventions ensures interoperability with the Kubernetes ecosystem (kubectl, dashboards, operators) and reduces surprise for users familiar with native resources.
