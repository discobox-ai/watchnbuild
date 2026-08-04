# watchnbuild

Watch files, run a build command, run the result. A tiny, race-free
alternative to [air](https://github.com/air-verse/air) that is not specific
to Go.

`wnb` guarantees three things air doesn't:

1. **No parallel stale builds.** A file change during a build cancels the
   in-flight build — the entire process group, so the compiler and everything
   it spawned actually die — before the new build starts.
2. **No orphans on exit.** Ctrl-C (or SIGTERM) stops the build and the
   running process by process group, with graceful-signal → grace period →
   SIGKILL escalation, and waits until the groups are actually empty. A
   second Ctrl-C during that wait kills immediately — the grace period is a
   promise to the process, not to you.
3. **Failed builds don't kill your process.** The previous binary keeps
   serving until a new build succeeds; only then is it gracefully swapped.
4. **Failures don't wedge the loop.** A failed build or a crashed process is
   retried on its own schedule (15s, backing off to 10m), so recovery never
   depends on catching the right file change.

## Usage

```
watchnbuild [-config path]
```

With no flag, the first of `.wnb.yaml`, `.wnb.yml`, `.wnb.json` in the
working directory is used. YAML is a superset of JSON, so both formats share
one schema.

### As a Go tool dependency

```
go get -tool github.com/ibuildthecloud/watchnbuild
go tool watchnbuild
```

## Configuration

```yaml
watch:
  paths: ["."]              # roots watched recursively (default: ["."])
  extensions: [go, yaml]    # file extensions that trigger a build; omit for
                            # the built-in source list, [] for every file
  include: [go.mod, go.sum] # extra trigger files: exact names or globs
  exclude: ["*_test.go"]    # never trigger: globs vs base name or rel path
  exclude_dirs: [bin, node_modules]  # not watched at all; hidden dirs
                                     # are always skipped

debounce: 300ms             # coalesce window; opens at the first event and
                            # fires once it elapses (fixed, not sliding)

build:
  command: go build -o bin/app .   # run through $SHELL; required
  grace: 2s                 # TERM → grace → KILL when cancelling a stale build

run:                        # optional: omit for a watch-and-build-only loop
  command: ./bin/app serve  # restarted after every successful build
  stop_signal: TERM         # TERM, INT, HUP, USR1, USR2, QUIT, KILL
  grace: 10s                # after stop_signal, before SIGKILL (0 = kill now)

retry:
  enabled: true             # retry after a failure without waiting for a
                            # file change (default: true)
  initial: 15s              # delay before the first retry
  max: 10m                  # cap; the delay doubles per consecutive failure
```

### Stopping

`grace` is how long a process gets after `stop_signal` before its group is
SIGKILLed, and it applies to every stop: shutdown, and the swap of the old
process for a new one after a successful build.

You never have to wait it out. `wnb` stays in its event loop while a stop is
in progress, so the first Ctrl-C begins the shutdown and a **second Ctrl-C
kills the process groups immediately** and exits 130. That makes a long
`grace` safe to configure: a server that legitimately needs minutes to drain
can have them, without a wedged one holding your terminal hostage.

Confirming that a process group has emptied is bounded separately (a few
seconds) rather than by `grace`, since "how long may you take to shut down"
and "how long do I wait to see that you're gone" are different questions.

### Retrying failures

File changes are not the only way a rebuild happens. Whenever the pipeline
ends up in a failed state — the build command exits non-zero, a command
can't be spawned at all, or the run process crashes — `wnb` schedules
another build on its own, 15s later, doubling per consecutive failure up to
10 minutes. The delay resets once a build succeeds and the process starts.

Triggers alone aren't enough: the fix may land in a file that doesn't match
your watch patterns, and a start failure is often transient (the old port is
still held, a database isn't up yet). A file change always supersedes a
pending retry and builds immediately, but does not reset the delay — only
success does. A run process that exits *cleanly* is treated as done, not
failed, and is not retried. Set `retry.enabled: false` to go back to
change-triggered builds only.

### What triggers a build

With no `watch.extensions`, `wnb` triggers on a built-in list of about 150
source, template, schema, and config file types — Go, C/C++, Rust, the JVM
and .NET languages, the usual scripting languages, JS/TS and its component
frameworks, HTML/CSS, template and IDL formats, and `json`/`yaml`/`toml`.
Startup logs that the default list is in use.

The point of a default allowlist is that files a build *writes* — logs,
databases, coverage profiles, extensionless binaries — don't retrigger it.
The cost is that an unlisted type is silently ignored, so:

- `watch.extensions: [go, sql]` narrows it to exactly what you list;
- `watch.extensions: []` (an explicit empty list) goes back to triggering on
  every non-excluded file;
- `watch.include: [Makefile, Dockerfile]` covers extensionless names, which
  no extension list can match.

Make sure anything your build writes (output binaries, logs) is excluded via
`exclude` / `exclude_dirs`, or you'll build in a loop. If a loop happens
anyway, `wnb` notices — three consecutive builds cancelled by new changes
triggers a warning naming the files changing most often, and rebuilds back
off exponentially (1s doubling to an 8s cap) until a build manages to
finish. Changes arriving during backoff are merged into the next build.

Durations are strings like `300ms`, `10s`, `1m30s`. Unknown config fields
are errors, so typos fail fast.

## Design

The architecture is a deliberate inversion of air's: a single goroutine (the
engine) owns all process state; the watcher, debouncer, and signal handler
only send it messages. There is no shared mutable state to race on.

The process-handling semantics are learned from
[watchexec](https://github.com/watchexec/watchexec) (Apache-2.0), whose
source was studied for this implementation (no code was ported):

- every command runs as the leader of its own process group, and signals are
  sent to the whole group (`kill(-pgid)`), so compilers, shells, and servers
  can't leave children behind;
- graceful stop is stop-signal → grace timer *raced against process exit* →
  SIGKILL, never a blind sleep;
- after the direct child is reaped, `wnb` waits until the process group is
  actually empty and warns if anything escaped;
- events are debounced in a fixed window opened by the first event, so a
  steady stream of changes can't starve the rebuild;
- signals bypass the event queue entirely;
- metadata-only (chmod/utime) events are ignored, and editor atomic saves
  are handled by watching directories rather than files.

On Windows there are no signals: stops are forceful process-tree kills
(`taskkill /T /F`), same as watchexec's behavior there.

## License

Apache-2.0
