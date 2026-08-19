//go:build windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A command may quote an argument that contains a space, and that argument has
// to reach the program as one argument. os/exec escapes every argument it
// composes into a Windows command line, so handing the whole command to cmd.exe
// as an argument leaves the quotes literal and the program sees a quote
// character inside its own argument -- which is how `go build -gcflags="all=-N
// -l"` failed on Windows while working on Unix from the same config.
//
// printf prints one argument per line, so a value that survived as a single
// argument appears on a single line.
func TestShellCommandKeepsAQuotedValueAsOneArgument(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out.txt")

	p, err := StartProc(`printf '[%s]
' -gcflags="all=-N -l" > "` + out + `"`)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Stop(os.Kill, 5*time.Second)
	<-p.done

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read shell output: %v", err)
	}
	got := strings.TrimSpace(string(data))
	if want := `[-gcflags=all=-N -l]`; got != want {
		t.Fatalf("program received %q, want %q as a single argument", got, want)
	}
}

// A relative command path is the other half of the same problem: cmd.exe reads
// the leading ./ as a command named "." because / introduces a switch, so a
// run command like ./build/app fails outright.
func TestShellCommandRunsARelativePath(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "hello.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho RELATIVE_OK\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out.txt")

	p, err := StartProc(`cd "` + dir + `" && sh ./hello.sh > "` + out + `"`)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Stop(os.Kill, 5*time.Second)
	<-p.done

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read shell output: %v", err)
	}
	if got := strings.TrimSpace(string(data)); got != "RELATIVE_OK" {
		t.Fatalf("output = %q, want RELATIVE_OK", got)
	}
}
