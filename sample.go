package main

import (
	"fmt"
	"os"
)

// sampleConfig is written when wnb starts with no config, so the first run
// leaves behind a file listing every option to edit. Only the
// section headers are live (an empty section is a valid one); "#field:"
// lines are options, set to their default or to an example where there is
// no default, and "## " lines explain them. Uncommenting every option line
// yields a valid config.
const sampleConfig = `## watchnbuild config. Every option is commented out and shows its default,
## or an example where there is none. Nothing is required: as is, this builds
## and runs the Go app in this directory.
## Durations are strings like 300ms, 10s, 1m30s.

watch:
  ## Roots watched recursively.
  #paths: ["."]

  ## File extensions (no dot) that trigger a build. Omit for the built-in list
  ## of ~150 source/config types; [] triggers on every file.
  #extensions: [go, mod, sum]

  ## Extra trigger files that no extension matches: exact names or globs.
  #include: [Makefile, Dockerfile]

  ## Files that never trigger a build: globs vs base name or relative path.
  #exclude: ["*_test.go", "*.log"]

  ## Directories not watched at all. Hidden directories are always skipped.
  ## Anything your build writes belongs here or in exclude, or it loops.
  #exclude_dirs: [bin, node_modules]

## Coalesce window, opened by the first change (fixed, not sliding).
#debounce: 300ms

build:
  ## Run through $SHELL after every change.
  #command: go build -o bin/app.exe .

  ## When cancelling a stale build: stop signal, then SIGKILL after this.
  #grace: 2s

run:
  ## Restarted after every successful build. Defaults to ./bin/app.exe only
  ## when build.command is unset too; otherwise omit it to only watch and build.
  #command: ./bin/app.exe

  ## Sent to the process group to stop it: TERM, INT, HUP, USR1, USR2, QUIT, KILL.
  #stop_signal: TERM

  ## How long after stop_signal before SIGKILL (0 kills immediately).
  #grace: 10s

  ## true: a clean exit (status 0) means "done" rather than "stopped serving",
  ## and is not retried. For run commands meant to finish.
  #allow_exit: false

retry:
  ## Rebuild after a failure without waiting for a file change.
  #enabled: true

  ## Delay before the first retry; it doubles per consecutive failure.
  #initial: 15s

  ## Cap on the retry delay.
  #max: 10m
`

// writeSampleConfig writes sampleConfig to name, refusing to overwrite.
func writeSampleConfig(name string) error {
	f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(sampleConfig); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("writing %s: %w", name, err)
	}
	return nil
}
