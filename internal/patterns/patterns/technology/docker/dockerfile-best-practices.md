# Review Pattern: Dockerfile Best Practices

**Review-Area**: quality
**Detection-Hint**: Dockerfile or Containerfile present; single-stage builds with build toolchains in the final image, instructions ordered against cache locality, missing `.dockerignore`, `RUN apt-get` without lockfile cleanup, `ADD` used where `COPY` would suffice, `latest`/empty `FROM` tags, `USER` not set or set to root, secrets baked into layers
**Severity**: WARNING
**Category**: technology
**Applies-When**: docker
**Sources**: Dockerfile Reference (https://docs.docker.com/reference/dockerfile/), Docker Build — Best Practices (https://docs.docker.com/build/building/best-practices/), Docker Build Concepts (https://docs.docker.com/build/concepts/), OCI Image Format Specification (https://github.com/opencontainers/image-spec)

## What to check

### Build structure
1. Multi-stage builds should separate build-time tooling from the runtime image — the final stage must not contain compilers, package managers, build caches, or `.git`/source trees unless explicitly required at runtime
2. Order instructions from least-frequently-changed to most-frequently-changed so layer cache hits survive routine code changes — dependency installs come before `COPY . .`
3. Combine related shell steps in a single `RUN` and clean up in the same layer (`apt-get update && apt-get install -y --no-install-recommends ... && rm -rf /var/lib/apt/lists/*`) — anything cleaned up in a later layer is still present in earlier layers and ships in the image
4. A `.dockerignore` file at the build context root must exclude `.git`, build artifacts, secrets, and editor files; without it, BuildKit ships the entire context to the daemon and busts cache on unrelated changes

### Instructions
5. Prefer `COPY` over `ADD` for local files — `ADD`'s implicit URL fetching and tar extraction are foot-guns; reserve `ADD` for the cases that actually need them
6. Pin every `FROM` to an immutable reference: a SemVer-aligned tag plus a digest (`FROM image:1.2.3@sha256:...`) — floating tags (`latest`, `bookworm`) make the same Dockerfile build different artifacts over time
7. Set `WORKDIR` explicitly instead of chains of `RUN cd ...`; absolute paths only
8. Declare `EXPOSE`, `ENV`, and `ARG` purposefully and document defaults; avoid leaking build-time `ARG` values into runtime via `ENV`
9. Use `ENTRYPOINT` (exec form: `["bin", "arg"]`) for the program identity and `CMD` for the default arguments — shell-form (`ENTRYPOINT cmd`) bypasses signal handling and PID 1 semantics

### Runtime hygiene
10. Add a non-root `USER` near the end of the Dockerfile and ensure all paths the process writes to are owned by that user — root in a container is one capability or namespace bug away from host impact
11. Provide a `HEALTHCHECK` (or rely on orchestrator-level probes and document that explicitly) so misbehaving containers are detected
12. Set sensible `LABEL`s following OCI image annotations (`org.opencontainers.image.source`, `.revision`, `.version`, `.licenses`) so registries and scanners can correlate images with source

### Secrets and context
13. Never `COPY` secrets, `.env` files, or private keys into a layer — use BuildKit secret mounts (`RUN --mount=type=secret,...`) or build-arg-then-discard patterns; secrets in any historical layer are recoverable from the image
14. Build context should be the smallest meaningful directory; sending the repo root when only `./service` is needed inflates image-build time and risks leaking unrelated files

## Why it matters

A Dockerfile is a declarative recipe whose every line becomes an immutable layer. Mistakes are cheap to write and expensive to undo: secrets in early layers can never be removed without a rebuild from scratch, build toolchains in the final image expand the attack surface and the image size by an order of magnitude, and cache-unfriendly ordering turns every CI build into a full reinstall. The Docker build best-practices guide and BuildKit concepts exist because these failure modes recur across organizations regardless of language. Reviewing Dockerfiles against the canonical reference catches the issues that will otherwise become production incidents — slow rollouts, surprise CVEs in unused tools, or an image that drifts because `FROM ubuntu:latest` re-tagged.
