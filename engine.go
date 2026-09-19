package main

import (
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// Engine is the single owner of all build/run process state. Every other
// goroutine (watcher, debouncer, signal handler) only sends it messages;
// nothing else starts or stops processes. This is the design lesson from
// watchexec's supervisor — and the fix for air's races: air checked a stop
// flag between stages but had no owner able to cancel work in flight.
type Engine struct {
	cfg        *Config
	batches    <-chan []string
	signals    <-chan os.Signal
	stopSignal os.Signal

	// aborted is the termination signal that arrived while the engine was
	// waiting for a process to stop; forced records that a second one
	// arrived and the groups were killed outright. Both are written only
	// by stopProcs, on the engine goroutine.
	aborted os.Signal
	forced  bool

	// reportDir is where failure reports are written (the working
	// directory); buildTrigger is what started the current build, for
	// its report.
	reportDir    string
	buildTrigger string
}

// stopTarget is one process to stop, with the grace period configured for
// its role. Builds and run processes have very different ones — a compiler
// has nothing to drain, a server may have plenty.
type stopTarget struct {
	proc  *Proc
	grace time.Duration
	what  string
}

func NewEngine(cfg *Config, batches <-chan []string, signals <-chan os.Signal) *Engine {
	sig, err := ParseSignal(cfg.Run.StopSignal)
	if err != nil {
		// validate() already rejected bad names; this is unreachable.
		panic(err)
	}
	return &Engine{cfg: cfg, batches: batches, signals: signals, stopSignal: sig, reportDir: workDir}
}

// Backoff kicks in after this many consecutive builds are cancelled by new
// changes — the signature of a feedback loop (build output inside the
// watched tree retriggering the watcher).
const backoffThreshold = 3

// Run drives the loop until a termination signal arrives. Returns the
// process exit code.
func (e *Engine) Run() int {
	var (
		build, run *Proc
		// Feedback-loop damping: consecutive cancelled builds, which
		// files caused them, and — once past the threshold — a timer
		// deferring the next build plus the changes queued behind it.
		cancels int
		hot     map[string]int
		backoff <-chan time.Time
		pending []string
		// Failure retry: consecutive failed builds/starts and the timer
		// that re-runs the pipeline without waiting for a file change.
		failures int
		retry    <-chan time.Time
		// Fires once the run process has stayed up long enough to
		// count as healthy, clearing any previous run failure report.
		runHealthy <-chan time.Time
	)

	build = e.startBuild([]string{"startup"})
	if build == nil {
		failures++
		retry = e.scheduleRetry(failures)
	}

	for {
		select {
		case batch, ok := <-e.batches:
			if !ok {
				return e.shutdown(build, run)
			}
			// A real change supersedes any pending retry; the failure
			// count stays, so repeated failures keep backing off.
			retry = nil
			if backoff != nil {
				pending = mergeBatch(pending, batch)
				countHot(hot, batch)
				continue
			}
			if build != nil {
				cancels++
				if hot == nil {
					hot = map[string]int{}
				}
				countHot(hot, batch)
				log.Printf("[wnb] change during build, cancelling stale build")
				e.stopBuild(build)
				build = nil
				if e.aborted != nil {
					return e.shutdown(nil, run)
				}
				if cancels >= backoffThreshold {
					delay := backoffDelay(cancels)
					log.Printf("[wnb] warning: %d consecutive builds cancelled by new changes (%s) — possible feedback loop; if a build output is being watched, add it to exclude/exclude_dirs. Backing off %s", cancels, hotSummary(hot), delay)
					pending = batch
					backoff = time.After(delay)
					continue
				}
			}
			build = e.startBuild(batch)
			if build == nil {
				failures++
				retry = e.scheduleRetry(failures)
			}

		case <-backoff:
			backoff = nil
			build = e.startBuild(pending)
			pending = nil
			if build == nil {
				failures++
				retry = e.scheduleRetry(failures)
			}

		case <-retry:
			retry = nil
			build = e.startBuild([]string{retryMarker})
			if build == nil {
				failures++
				retry = e.scheduleRetry(failures)
			}

		case <-done(build):
			finished := build
			build = nil
			cancels, hot = 0, nil
			if !finished.Success() {
				failures++
				log.Printf("[wnb] build failed (%v)%s", finished.ExitError(), keepNote(run))
				e.reportBuild(finished.ExitError().Error(), finished)
				retry = e.scheduleRetry(failures)
				continue
			}
			log.Printf("[wnb] build succeeded")
			clearReport(e.reportDir, buildReportFile)
			var ok bool
			run, ok = e.restartRun(run)
			runHealthy = nil
			if e.aborted != nil {
				return e.shutdown(nil, run)
			}
			if !ok {
				failures++
				retry = e.scheduleRetry(failures)
				continue
			}
			if run != nil {
				runHealthy = time.After(runHealthyAfter)
			}
			failures = 0

		case <-runHealthy:
			runHealthy = nil
			clearReport(e.reportDir, runReportFile)

		case <-done(run):
			runHealthy = nil
			err := run.ExitError()
			switch {
			case err == nil && e.cfg.Run.AllowExit:
				log.Printf("[wnb] process exited cleanly (waiting for next change)")
				clearReport(e.reportDir, runReportFile)
			case err == nil:
				// A server that exited has stopped serving, however
				// politely it did so — same transient case a crash is,
				// and the same fix. run.allow_exit opts out.
				failures++
				log.Printf("[wnb] process exited cleanly but is not running (set run.allow_exit to treat this as done)")
				e.reportRun("exited with status 0, but the run command is expected to keep running (set run.allow_exit to treat a clean exit as done)", run)
				retry = e.scheduleRetry(failures)
			default:
				// A crash is the transient case retries exist for.
				failures++
				log.Printf("[wnb] process exited: %v", err)
				e.reportRun(err.Error(), run)
				retry = e.scheduleRetry(failures)
			}
			run = nil

		case sig := <-e.signals:
			log.Printf("[wnb] received %v, shutting down (press again to kill immediately)", sig)
			e.aborted = sig
			return e.shutdown(build, run)
		}
	}
}

func (e *Engine) startBuild(reasons []string) *Proc {
	display := reasons
	if len(display) > 5 {
		display = append(append([]string{}, display[:5]...), "...")
	}
	for i, r := range display {
		if r == overflowMarker {
			display[i] = "(event overflow)"
		}
	}
	e.buildTrigger = strings.Join(display, ", ")
	log.Printf("[wnb] building (%s)", e.buildTrigger)

	p, err := StartProc(e.cfg.Build.Command)
	if err != nil {
		log.Printf("[wnb] failed to start build: %v", err)
		e.reportBuild("could not start: "+err.Error(), nil)
		return nil
	}
	return p
}

// reportBuild writes the build failure report; build is nil if the command
// never started.
func (e *Engine) reportBuild(result string, build *Proc) {
	writeReport(e.reportDir, buildReportFile, failureReport{
		what:    "build",
		command: e.cfg.Build.Command,
		trigger: e.buildTrigger,
		result:  result,
		proc:    build,
		cleared: "This file is removed when a build succeeds.",
	})
}

// reportRun writes the run failure report; run is nil if the command never
// started.
func (e *Engine) reportRun(result string, run *Proc) {
	writeReport(e.reportDir, runReportFile, failureReport{
		what:    "run",
		command: e.cfg.Run.Command,
		result:  result,
		proc:    run,
		cleared: fmt.Sprintf("This file is removed once a restarted process stays up for %s.", runHealthyAfter),
	})
}

// stopProcs stops every target concurrently and waits for all of them, while
// staying responsive to termination signals — a stop can legitimately take
// the whole grace period, and Ctrl-C has to work throughout it.
//
// The first signal (or armed=true, meaning one was already consumed by the
// caller) records that wnb is shutting down; the second stops waiting and
// kills the process groups. That double-Ctrl-C escape is the whole point:
// the grace period is a promise to the process, not to the user.
func (e *Engine) stopProcs(armed bool, targets ...stopTarget) {
	var live []stopTarget
	for _, t := range targets {
		if t.proc != nil {
			live = append(live, t)
		}
	}
	if len(live) == 0 {
		return
	}

	done := make(chan struct{})
	go func() {
		var wg sync.WaitGroup
		for _, t := range live {
			wg.Add(1)
			go func(t stopTarget) {
				defer wg.Done()
				if !t.proc.Stop(e.stopSignal, t.grace) {
					log.Printf("[wnb] warning: some %s processes survived kill (escaped the process group)", t.what)
				}
			}(t)
		}
		wg.Wait()
		close(done)
	}()

	for {
		select {
		case <-done:
			return
		case sig := <-e.signals:
			if !armed {
				armed = true
				e.aborted = sig
				log.Printf("[wnb] received %v, shutting down (press again to kill immediately)", sig)
				continue
			}
			log.Printf("[wnb] received %v again, killing now", sig)
			e.forced = true
			for _, t := range live {
				t.proc.Force()
			}
			// Force short-circuits every grace period, so the only
			// remaining wait is for SIGKILL to land.
			<-done
			return
		}
	}
}

func (e *Engine) stopBuild(build *Proc) {
	e.stopProcs(false, stopTarget{build, e.cfg.Build.Grace.D(), "build"})
}

// scheduleRetry returns a timer for the next automatic rebuild after
// `failures` consecutive failures, or nil if retries are disabled.
func (e *Engine) scheduleRetry(failures int) <-chan time.Time {
	if !e.cfg.Retry.On() {
		log.Printf("[wnb] waiting for next change (retry disabled)")
		return nil
	}
	d := retryDelay(e.cfg.Retry.Initial.D(), e.cfg.Retry.Max.D(), failures)
	log.Printf("[wnb] retrying in %s (failure %d)", d, failures)
	return time.After(d)
}

// restartRun (re)starts the run command. The bool is false only when a start
// was attempted and failed; an empty run command is a success with no proc.
func (e *Engine) restartRun(run *Proc) (*Proc, bool) {
	if e.cfg.Run.Command == "" {
		// Nothing to run, so nothing can be failing: drop a report a
		// previous session with a run command left behind.
		clearReport(e.reportDir, runReportFile)
		return nil, true
	}
	if run != nil {
		log.Printf("[wnb] stopping process (signal %s, grace %s)", e.cfg.Run.StopSignal, e.cfg.Run.Grace.D())
		e.stopProcs(false, stopTarget{run, e.cfg.Run.Grace.D(), ""})
		if e.aborted != nil {
			// Ctrl-C during the stop: don't start a replacement we
			// are about to shut down again.
			return nil, true
		}
	}
	log.Printf("[wnb] starting process")
	p, err := StartProc(e.cfg.Run.Command)
	if err != nil {
		log.Printf("[wnb] failed to start process: %v", err)
		e.reportRun("could not start: "+err.Error(), nil)
		return nil, false
	}
	return p, true
}

// shutdown stops everything and returns the process exit code: 130 (128 +
// SIGINT) if the user had to insist, 0 if it drained on its own.
func (e *Engine) shutdown(build, run *Proc) int {
	// armed: a termination signal has already been seen by the caller, so
	// the next one kills rather than repeating "shutting down".
	e.stopProcs(e.aborted != nil,
		stopTarget{build, e.cfg.Build.Grace.D(), "build"},
		stopTarget{run, e.cfg.Run.Grace.D(), ""})
	log.Printf("[wnb] done")
	if e.forced {
		return 130
	}
	return 0
}

// done returns a nil channel for a nil proc, so it never fires in select.
func done(p *Proc) <-chan struct{} {
	if p == nil {
		return nil
	}
	return p.Done()
}

// retryMarker is the build "reason" logged for an automatic retry.
const retryMarker = "retry after failure"

// retryDelay doubles from initial per consecutive failure, capped at max.
func retryDelay(initial, max time.Duration, failures int) time.Duration {
	d := initial
	for i := 1; i < failures; i++ {
		d *= 2
		if d >= max || d <= 0 { // >= max, or overflowed past it
			return max
		}
	}
	if d > max {
		return max
	}
	return d
}

// backoffDelay grows exponentially from 1s at the threshold, capped at 8s.
func backoffDelay(cancels int) time.Duration {
	shift := cancels - backoffThreshold
	if shift > 3 {
		shift = 3
	}
	return time.Second << shift
}

func mergeBatch(pending, batch []string) []string {
	seen := make(map[string]bool, len(pending))
	for _, p := range pending {
		seen[p] = true
	}
	for _, p := range batch {
		if !seen[p] {
			seen[p] = true
			pending = append(pending, p)
		}
	}
	return pending
}

func countHot(hot map[string]int, batch []string) {
	for _, p := range batch {
		hot[p]++
	}
}

// hotSummary lists the most frequently changing files, most frequent first.
func hotSummary(hot map[string]int) string {
	files := make([]string, 0, len(hot))
	for f := range hot {
		files = append(files, f)
	}
	sort.Slice(files, func(i, j int) bool {
		if hot[files[i]] != hot[files[j]] {
			return hot[files[i]] > hot[files[j]]
		}
		return files[i] < files[j]
	})
	if len(files) > 5 {
		files = files[:5]
	}
	parts := make([]string, len(files))
	for i, f := range files {
		parts[i] = fmt.Sprintf("%s x%d", f, hot[f])
	}
	return strings.Join(parts, ", ")
}

func keepNote(run *Proc) string {
	if run != nil {
		return ", keeping previous process running"
	}
	return ""
}
