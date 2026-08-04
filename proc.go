package main

import (
	"os"
	"os/exec"
	"sync"
	"time"
)

// Proc is a shell command running as the leader of its own process group, so
// that stop signals reach every descendant (compilers, node servers, etc.)
// and nothing is orphaned. Modeled on watchexec's process-wrap ProcessGroup
// and supervisor job: graceful stop is signal → grace timer raced against
// exit → SIGKILL, and after the direct child exits we wait for the *whole
// group* to be gone before declaring the stop complete.
type Proc struct {
	cmd  *exec.Cmd
	pgid int
	done chan struct{}
	err  error // result of Wait, valid after done is closed

	// force is closed by Force to cut a grace period short. A stop in
	// progress races it against the grace timer, so a second Ctrl-C stops
	// waiting on a process that is clearly not going to leave politely.
	force     chan struct{}
	forceOnce sync.Once
}

// StartProc runs command through the shell in a new process group, with
// stdout/stderr inherited.
func StartProc(command string) (*Proc, error) {
	cmd := shellCommand(command)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = nil
	setNewProcessGroup(cmd)

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	p := &Proc{
		cmd:   cmd,
		pgid:  cmd.Process.Pid,
		done:  make(chan struct{}),
		force: make(chan struct{}),
	}
	go func() {
		p.err = cmd.Wait()
		close(p.done)
	}()
	return p, nil
}

// Done is closed once the direct child has been reaped.
func (p *Proc) Done() <-chan struct{} { return p.done }

// Success reports whether the command exited with status 0. Only meaningful
// after Done is closed.
func (p *Proc) Success() bool { return p.err == nil }

// ExitError returns the Wait error (nil on success). Only meaningful after
// Done is closed.
func (p *Proc) ExitError() error { return p.err }

// Stop terminates the process group: send sig, wait up to grace for the
// direct child to exit, then SIGKILL the group. grace <= 0 force-kills
// immediately, and Force cuts the wait short at any point. After the child
// is reaped it waits (briefly) for the rest of the group to disappear and
// reports whether the group is fully gone.
//
// Stop can block for the whole grace period, so callers that must stay
// responsive run it on its own goroutine and use Force to cut it short.
func (p *Proc) Stop(sig os.Signal, grace time.Duration) (groupGone bool) {
	select {
	case <-p.done:
		// Already exited; still check for surviving descendants.
		return p.awaitGroupGone()
	default:
	}

	if grace > 0 {
		p.signalGroup(sig)
		select {
		case <-p.done:
			return p.awaitGroupGone()
		case <-p.force:
		case <-time.After(grace):
		}
	}

	p.killGroup()
	<-p.done
	return p.awaitGroupGone()
}

// Force abandons the graceful part of a stop: a Stop waiting out its grace
// period kills the group immediately instead, and a later Stop skips the
// grace entirely. Safe to call repeatedly and from any goroutine.
func (p *Proc) Force() { p.forceOnce.Do(func() { close(p.force) }) }

// groupSettleWait bounds the wait for a process group to empty after the
// direct child has been reaped. It is deliberately not the caller's grace:
// grace answers "how long may you take to shut down gracefully", which can
// legitimately be minutes, while this answers "how long do I wait to confirm
// nothing escaped" — a question about reaping, not about draining.
const groupSettleWait = 3 * time.Second

// awaitGroupGone polls until no member of the process group remains.
// Descendants that double-forked out of the group can't be seen here; the
// group check catches everything else.
func (p *Proc) awaitGroupGone() bool {
	deadline := time.Now().Add(groupSettleWait)
	for {
		if p.groupGone() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
}
