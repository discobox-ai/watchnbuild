package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Failure reports are plain files in the working directory, so that whoever
// is editing the code — a person, or an agent that never sees wnb's terminal
// — can find out that the last build or run failed, and why, by looking for
// them. Each is removed once the step it describes works again.
const (
	buildReportFile = "wnb-build-failed.txt"
	runReportFile   = "wnb-run-failed.txt"
)

// runHealthyAfter is how long a started run process must stay up before a
// previous run failure report is considered resolved and removed.
const runHealthyAfter = time.Second

// isReportFile reports whether rel (relative to the working directory) is a
// failure report or its temp file. They are written into the watched tree,
// so they must never trigger a build — whatever the watch config says.
func isReportFile(rel string) bool {
	for _, name := range []string{buildReportFile, runReportFile} {
		if rel == name || rel == reportTemp(name) {
			return true
		}
	}
	return false
}

func reportTemp(name string) string { return "." + name + ".tmp" }

// failureReport describes one failed build or run.
type failureReport struct {
	what    string // "build" or "run"
	command string
	trigger string // what started the build; empty for runs
	result  string // exit status, or why it could not start
	proc    *Proc  // nil if the command never started
	cleared string // when the report will be removed
}

func (r failureReport) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "watchnbuild: %s failed\n\n", r.what)
	fmt.Fprintf(&b, "Command:  %s\n", r.command)
	if r.trigger != "" {
		fmt.Fprintf(&b, "Trigger:  %s\n", r.trigger)
	}
	now := time.Now()
	if r.proc != nil {
		fmt.Fprintf(&b, "Started:  %s\n", r.proc.Started().Format(time.RFC3339))
		fmt.Fprintf(&b, "Ended:    %s (after %s)\n", now.Format(time.RFC3339), now.Sub(r.proc.Started()).Round(time.Millisecond))
	} else {
		fmt.Fprintf(&b, "Time:     %s\n", now.Format(time.RFC3339))
	}
	fmt.Fprintf(&b, "Result:   %s\n\n", r.result)
	fmt.Fprintf(&b, "watchnbuild retries on the next watched file change, and on its own after a backoff delay if retries are enabled. %s\n", r.cleared)

	if r.proc == nil {
		return b.String()
	}
	lines, dropped := r.proc.Output()
	switch {
	case len(lines) == 0:
		b.WriteString("\n--- output: (none) ---\n")
	case dropped > 0:
		fmt.Fprintf(&b, "\n--- output (truncated: %d earlier lines omitted, last %d shown) ---\n", dropped, len(lines))
	default:
		b.WriteString("\n--- output ---\n")
	}
	for _, l := range lines {
		b.WriteString(l)
		b.WriteByte('\n')
	}
	return b.String()
}

// writeReport writes the report to dir/name via a temp file and rename, so a
// reader never sees a half-written one.
func writeReport(dir, name string, r failureReport) {
	path := filepath.Join(dir, name)
	tmp := filepath.Join(dir, reportTemp(name))
	if err := os.WriteFile(tmp, []byte(r.String()), 0o644); err != nil {
		log.Printf("[wnb] warning: cannot write %s: %v", path, err)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		log.Printf("[wnb] warning: cannot write %s: %v", path, err)
		return
	}
	log.Printf("[wnb] wrote %s", name)
}

// clearReport removes dir/name if it exists.
func clearReport(dir, name string) {
	err := os.Remove(filepath.Join(dir, name))
	switch {
	case err == nil:
		log.Printf("[wnb] removed %s", name)
	case !os.IsNotExist(err):
		log.Printf("[wnb] warning: cannot remove %s: %v", name, err)
	}
}
