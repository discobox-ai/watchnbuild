//go:build unix

package main

import (
	"syscall"
	"testing"
	"time"
)

func TestStopKillsWholeGroup(t *testing.T) {
	// The child spawns a grandchild that ignores nothing; killing the
	// group must take both down.
	p, err := StartProc("sleep 30 & wait")
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)

	start := time.Now()
	if !p.Stop(syscall.SIGTERM, 5*time.Second) {
		t.Fatal("process group did not fully exit")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("graceful stop took %v, expected fast TERM exit", elapsed)
	}
	if err := syscall.Kill(-p.pgid, syscall.Signal(0)); err != syscall.ESRCH {
		t.Fatalf("process group still has members: kill(0) = %v", err)
	}
}

func TestStopEscalatesToKillAfterGrace(t *testing.T) {
	// Child traps and ignores TERM; only SIGKILL after the grace period
	// can end it.
	p, err := StartProc(`trap "" TERM; sleep 30`)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)

	start := time.Now()
	if !p.Stop(syscall.SIGTERM, 500*time.Millisecond) {
		t.Fatal("process group did not fully exit")
	}
	elapsed := time.Since(start)
	if elapsed < 400*time.Millisecond {
		t.Fatalf("stop returned in %v, before the grace period elapsed", elapsed)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("escalation to SIGKILL took %v", elapsed)
	}
}

func TestZeroGraceForceKillsImmediately(t *testing.T) {
	p, err := StartProc(`trap "" TERM; sleep 30`)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)

	start := time.Now()
	if !p.Stop(syscall.SIGTERM, 0) {
		t.Fatal("process group did not fully exit")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("force kill took %v", elapsed)
	}
}

func TestStopAfterNaturalExit(t *testing.T) {
	p, err := StartProc("true")
	if err != nil {
		t.Fatal(err)
	}
	<-p.Done()
	if !p.Success() {
		t.Fatalf("expected success, got %v", p.ExitError())
	}
	if !p.Stop(syscall.SIGTERM, time.Second) {
		t.Fatal("Stop on exited process should report group gone")
	}
}

func TestFailedCommandReportsError(t *testing.T) {
	p, err := StartProc("exit 3")
	if err != nil {
		t.Fatal(err)
	}
	<-p.Done()
	if p.Success() {
		t.Fatal("expected failure")
	}
}
