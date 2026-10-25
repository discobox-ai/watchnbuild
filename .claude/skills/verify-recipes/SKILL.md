---
name: verify-recipes
description: Build and exercise watchnbuild through its CLI with isolated watched files and captured lifecycle logs.
---

Build the CLI with `go build -o /tmp/wnb-verify .`. Launch it from a fresh
`mktemp -d` directory with an explicit `.wnb.yaml`, capturing stdout/stderr
outside the watched tree. There is no Taskfile or `task dev` in this repo.

For trigger diagnostics, configure `watch.extensions: [go]`, a short debounce,
`build.command: "sleep 0.1"`, and a long-running run command such as `sleep 30`.
Launch with `-verbose -config .wnb.yaml`. Wait for startup's `starting process`
before creating, writing, renaming, and removing `.go` files. Write more than
five files inside one debounce window; confirm the complete verbose entries,
the bounded summary, and the matching reason at process stop/start. Try a
file named `startup` with `watch.extensions: []`, and metadata-only chmod.

For retry diagnostics, use `build.command: "exit 1"`, `retry.initial: 200ms`,
and `retry.max: 200ms`; observe `startup` followed by `retry after failure`.
Run without `-verbose` to check concise lifecycle logs and full build failure
reports. Terminate each CLI with SIGTERM and wait for exit; remove scratch
files. Actual kernel overflow and non-Linux backends need separate coverage;
do not claim to observe them from synthetic unit inputs.
