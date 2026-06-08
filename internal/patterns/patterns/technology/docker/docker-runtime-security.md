# Review Pattern: Docker Runtime Security

**Review-Area**: security
**Detection-Hint**: `docker run --privileged`, containers running as UID 0, missing `--cap-drop=ALL`, default seccomp profile disabled, host namespaces shared (`--network=host`, `--pid=host`, `--ipc=host`), `/var/run/docker.sock` mounted into containers, no AppArmor/SELinux confinement, daemon API exposed without TLS, rootful daemon where rootless would suffice
**Severity**: CRITICAL
**Category**: technology
**Applies-When**: docker
**Sources**: Docker Engine Security (https://docs.docker.com/engine/security/), CIS Docker Benchmark (https://www.cisecurity.org/benchmark/docker), docker/docker-bench-security (https://github.com/docker/docker-bench-security), OCI Runtime Specification (https://github.com/opencontainers/runtime-spec)

## What to check

### Container privilege
1. `--privileged` (or `privileged: true` in compose) should never appear in production manifests — it disables all isolation. Document any exception with the specific capability that drove it and migrate to targeted `--cap-add`
2. Drop all Linux capabilities by default and add only what the workload needs: `--cap-drop=ALL --cap-add=NET_BIND_SERVICE` (or none). The default capability set is far broader than most workloads require
3. `--security-opt no-new-privileges:true` should be set so child processes cannot gain capabilities through setuid binaries
4. Containers should not run as UID 0; set `USER` in the Dockerfile and prefer arbitrary high UIDs (compatible with Kubernetes `runAsNonRoot`)

### Host isolation
5. `--network=host`, `--pid=host`, `--ipc=host`, `--userns=host` flags must each be justified — they collapse the namespace boundary that makes containers containers
6. `-v /var/run/docker.sock:/var/run/docker.sock` grants the container full control of the daemon and therefore the host. Document every occurrence and prefer rootless socket forwarding or scoped APIs (Docker Authorization Plugin, sysbox)
7. Bind-mounts of host paths (`-v /:/host`, `-v /etc:/etc`) should be read-only by default and scoped to the smallest path needed

### Mandatory access control
8. Keep the default seccomp profile enabled (`--security-opt seccomp=default.json`) — passing `--security-opt seccomp=unconfined` re-exposes hundreds of syscalls
9. Use AppArmor (Debian/Ubuntu) or SELinux (RHEL/Fedora) profiles; the default `docker-default` AppArmor profile is the floor, not the ceiling, for production
10. Set `--read-only` on the root filesystem and mount writable scratch space as `tmpfs` where the workload needs it — read-only roots block most persistence techniques

### Daemon and host
11. The Docker daemon listens on `/var/run/docker.sock` by default; if a TCP socket is enabled it must require client TLS (`--tlsverify`) and mTLS-pinned clients — an exposed daemon is a remote-root vulnerability
12. Prefer rootless mode for development and high-isolation workloads — it removes the daemon-as-root attack surface entirely
13. Run the CIS Docker Benchmark (`docker-bench-security`) against the host and remediate failures; treat the script as a regression suite rather than a one-time scan
14. Limit container resources (`--memory`, `--cpus`, `--pids-limit`, `--ulimit nofile=...`) so a runaway container cannot starve the host

## Why it matters

A container is a process tree confined by namespaces, cgroups, capabilities, and a mandatory-access-control profile. Every flag that disables one of these layers shifts the workload one step closer to a host process — not metaphorically, but literally: `--privileged` plus a host bind-mount is equivalent to running the binary as root on the node. The CIS Docker Benchmark, Docker's own security documentation, and the OCI runtime spec all converge on the same defense-in-depth model because individual layers fail. Reviewing run-time configuration against this baseline catches the high-impact misconfigurations — unprivileged containers absorbing breakouts, dropped capabilities turning kernel exploits into noise — before they become incidents.
