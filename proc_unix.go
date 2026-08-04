//go:build unix

package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

func shellCommand(command string) *exec.Cmd {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	return exec.Command(shell, "-c", command)
}

func setNewProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// signalGroup signals the whole process group, and the direct child as well
// in case it moved itself to another group (watchexec does the same).
func (p *Proc) signalGroup(sig os.Signal) {
	s, ok := sig.(syscall.Signal)
	if !ok {
		s = syscall.SIGTERM
	}
	_ = syscall.Kill(-p.pgid, s)
	_ = syscall.Kill(p.pgid, s)
}

func (p *Proc) killGroup() {
	_ = syscall.Kill(-p.pgid, syscall.SIGKILL)
	_ = syscall.Kill(p.pgid, syscall.SIGKILL)
}

// groupGone reports whether no process in the group remains. Signal 0 probes
// for existence; ESRCH means the group has no members left.
func (p *Proc) groupGone() bool {
	err := syscall.Kill(-p.pgid, syscall.Signal(0))
	return err == syscall.ESRCH
}

var signalNames = map[string]syscall.Signal{
	"HUP":  syscall.SIGHUP,
	"INT":  syscall.SIGINT,
	"QUIT": syscall.SIGQUIT,
	"KILL": syscall.SIGKILL,
	"USR1": syscall.SIGUSR1,
	"USR2": syscall.SIGUSR2,
	"TERM": syscall.SIGTERM,
}

func ParseSignal(name string) (os.Signal, error) {
	key := strings.ToUpper(strings.TrimPrefix(strings.ToUpper(name), "SIG"))
	if s, ok := signalNames[key]; ok {
		return s, nil
	}
	return nil, fmt.Errorf("unknown signal %q", name)
}
