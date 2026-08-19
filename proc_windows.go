//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// shellCommand runs command through a shell, preferring a POSIX one.
//
// A config's command is a shell snippet, and the snippets people write are
// POSIX: "./build/app", "cd ../server && go build -gcflags=\"all=-N -l\"". Neither
// survives cmd.exe. It reads a leading ./ as a command named "." followed by
// switches, because / introduces one; and os/exec composes a Windows command
// line by escaping each argument, so a command carrying its own quotes reaches
// the program with those quotes intact and is rejected.
//
// Preferring a POSIX shell -- Git Bash ships one, and $SHELL names it when set
// -- lets a single config work on every platform, which is the point of putting
// the command in a config file rather than a script per OS. cmd.exe remains the
// fallback for machines without one, now given its command line verbatim so at
// least quoting survives.
func shellCommand(command string) *exec.Cmd {
	if shell := posixShell(); shell != "" {
		cmd := exec.Command(shell, "-c", command)
		cmd.Env = shellToolsOnPath(os.Environ(), filepath.Dir(shell))
		return cmd
	}
	shell := os.Getenv("COMSPEC")
	if shell == "" {
		shell = "cmd.exe"
	}
	cmd := exec.Command(shell)
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: syscall.EscapeArg(shell) + " /c " + command}
	return cmd
}

// posixShell locates a POSIX shell, or returns "" when the machine has none.
//
// PATH alone is not enough. A PowerShell or cmd session does not have Git's
// usr\bin on PATH even when Git Bash is installed, so "sh" is missing there and
// present in a Git Bash session -- the same machine would behave differently
// depending on which terminal started the watcher. Deriving the shell from
// git.exe, which those sessions do have, closes that gap.
//
// "bash" is deliberately not searched for: on Windows it resolves to
// system32\bash.exe, the WSL launcher, which would run the build inside Linux
// against Linux paths rather than on the host.
func posixShell() string {
	if named := strings.TrimSpace(os.Getenv("SHELL")); named != "" {
		if path, err := exec.LookPath(named); err == nil {
			return path
		}
	}
	if path, err := exec.LookPath("sh"); err == nil {
		return path
	}
	if git, err := exec.LookPath("git"); err == nil {
		// <install>\cmd\git.exe and <install>\usr\bin\sh.exe.
		candidate := filepath.Join(filepath.Dir(filepath.Dir(git)), "usr", "bin", "sh.exe")
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return ""
}

// shellToolsOnPath puts the shell's own directory first on PATH.
//
// A shell found by deriving it from git.exe is started outside the environment
// Git Bash would have set up, so its PATH carries none of the tools a command
// may reasonably use -- even uname is missing. Prepending the directory it
// lives in makes the command behave the same whichever terminal launched it.
func shellToolsOnPath(env []string, dir string) []string {
	out := make([]string, 0, len(env))
	replaced := false
	for _, entry := range env {
		if name, value, ok := strings.Cut(entry, "="); ok && strings.EqualFold(name, "PATH") {
			out = append(out, name+"="+dir+string(os.PathListSeparator)+value)
			replaced = true
			continue
		}
		out = append(out, entry)
	}
	if !replaced {
		out = append(out, "PATH="+dir)
	}
	return out
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
