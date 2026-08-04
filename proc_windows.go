//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

func shellCommand(command string) *exec.Cmd {
	shell := os.Getenv("COMSPEC")
	if shell == "" {
		shell = "cmd.exe"
	}
	return exec.Command(shell, "/c", command)
}

func setNewProcessGroup(cmd *exec.Cmd) {}

// Windows has no signals; like watchexec, every stop is a forceful
// termination. taskkill /T takes the whole process tree down.
func (p *Proc) signalGroup(sig os.Signal) { p.killGroup() }

func (p *Proc) killGroup() {
	_ = exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(p.pgid)).Run()
}

func (p *Proc) groupGone() bool {
	select {
	case <-p.done:
		return true
	default:
		return false
	}
}

func ParseSignal(name string) (os.Signal, error) {
	switch strings.ToUpper(strings.TrimPrefix(strings.ToUpper(name), "SIG")) {
	case "TERM", "INT", "KILL":
		return os.Kill, nil
	}
	return nil, fmt.Errorf("unknown signal %q", name)
}
