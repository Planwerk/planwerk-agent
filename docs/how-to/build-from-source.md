# Build from source

The repository brings its own build environment. A machine that builds
planwerk-agent from a checkout needs three things on `$PATH`:

| Tool | Why it cannot live in the image |
|---|---|
| Docker | It runs the image. On Linux the invoking user must be allowed to talk to the daemon (member of the `docker` group). |
| `make` | It is the entry point that builds and enters the image. |
| `git` | It produced the checkout, and the Makefile reads the version stamp from it. |

Everything else comes from the toolbox image described by
`tools/toolbox/Dockerfile`: Go at the version `go.mod` pins, `golangci-lint` at
the version `.github/workflows/lint.yml` runs, and the `claude` CLI.

```bash
git clone https://github.com/planwerk/planwerk-agent.git && cd planwerk-agent
make build      # the first run builds the image, then compiles inside it
./planwerk-agent --version
```

## How a target runs

`make <target>` on the host does not run the target's recipe. It checks that
the toolbox image for this checkout exists, builds it when it does not, and
then runs `make <target>` again inside a container started by
`tools/toolbox/run.sh`. Several goals on one command line share one container
and keep their order, and variables given on the command line
(`make eval EVAL_ARGS=-json`) travel with them.

Inside the container the recipes are the native ones. The image sets
`PLANWERK_TOOLBOX=1`, and the Makefile reads that as "already inside, do not
delegate again".

`make build` compiles for the host that asked, not for the Linux container it
runs in: on a Mac it leaves a macOS binary in the checkout. To build for
another platform, name it:

```bash
make build BUILD_GOOS=linux BUILD_GOARCH=amd64
```

Three targets always run on the host:

| Target | What it does |
|---|---|
| `make toolbox-image` | Builds the image when this checkout's tag is missing. Every delegated target depends on it, so calling it by hand only pre-warms a machine. |
| `make toolbox-shell` | Opens an interactive `bash` in the container, for ad-hoc `go test ./internal/...` work. |
| `make toolbox-clean` | Removes every `planwerk-agent-toolbox` image and both cache volumes. |

## What the container sees

- **The checkout, at its host path.** The repository is bind-mounted at the
  same absolute path it has on the host, so a linked git worktree finds its
  object store (which is mounted too), and file paths in compiler and test
  output are the ones on your disk.
- **Two named volumes.** `planwerk-agent-toolbox-go` is mounted over `/go` and
  holds the module cache; `planwerk-agent-toolbox-cache` holds the Go build
  cache and the golangci-lint cache. The first `make test` on a machine
  downloads the modules; every later run finds them in the volume.
- **Selected environment variables.** Host variables matching `PLANWERK_*` and
  `ANTHROPIC_*`, plus `CLAUDE_CODE_OAUTH_TOKEN`, `NO_COLOR`, and `TERM`, are
  forwarded. Name further ones per invocation with
  `make <target> TOOLBOX_ENV="FOO BAR"`.

The container runs as the invoking user, so build output in the checkout
keeps its owner. `TOOLBOX_RUN_ARGS` appends raw `docker run` arguments.

## Run the eval in the toolbox

`make eval` drives the `claude` CLI inside the container, which cannot see the
login of the `claude` on your host. Export a credential before running it:

```bash
export CLAUDE_CODE_OAUTH_TOKEN=...   # from `claude setup-token`, or
export ANTHROPIC_API_KEY=...
make eval
```

Or run it natively with `TOOLBOX=0`, against the `claude` you are logged in
with.

## Switching the toolbox off

`TOOLBOX=0` runs the recipes against the tools installed on the host:

```bash
make lint TOOLBOX=0     # one invocation
export TOOLBOX=0        # the whole shell session
```

The Makefile resolves the mode in this order:

1. Inside the toolbox container the mode is always native.
2. An explicit `TOOLBOX=0` or `TOOLBOX=1`, on the command line or in the
   environment, wins.
3. With `CI` set the default is native. The workflows install their pinned
   tools through setup actions and keep exercising the native recipes.
4. Otherwise the default is `TOOLBOX=1`.

## The image tag

The image is tagged `planwerk-agent-toolbox:<digest>`, where the digest covers
the Dockerfile and every build argument. Editing the Dockerfile, moving the Go
version in `go.mod`, bumping golangci-lint in the lint workflow, or changing
`TOOLBOX_CLAUDE_CODE_VERSION` in the Makefile yields a new tag, and the next
`make` rebuilds. An unchanged checkout finds its image by name and starts
immediately. Old tags are not pruned automatically; `make toolbox-clean`
removes them.
