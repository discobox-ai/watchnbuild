package main

import (
	"fmt"
	"strings"
	"testing"
)

func TestOutputTailKeepsLastLines(t *testing.T) {
	tail := &outputTail{}
	s := &lineSplitter{tail: tail}
	for i := 0; i < outputTailLines+5; i++ {
		fmt.Fprintf(s, "line %d\n", i)
	}
	lines, dropped := tail.Snapshot()
	if dropped != 5 || len(lines) != outputTailLines {
		t.Fatalf("dropped=%d len=%d", dropped, len(lines))
	}
	if lines[0] != "line 5" || lines[len(lines)-1] != fmt.Sprintf("line %d", outputTailLines+4) {
		t.Fatalf("first=%q last=%q", lines[0], lines[len(lines)-1])
	}
}

func TestLineSplitter(t *testing.T) {
	tail := &outputTail{}
	s := &lineSplitter{tail: tail}
	s.Write([]byte("hel"))
	s.Write([]byte("lo\r\nwor"))
	s.Write([]byte("ld\n" + strings.Repeat("x", maxLineBytes+1) + "\nno newline"))
	s.Flush()
	lines, _ := tail.Snapshot()
	want := []string{"hello", "world", strings.Repeat("x", maxLineBytes), "x", "no newline"}
	if fmt.Sprint(lines) != fmt.Sprint(want) {
		t.Fatalf("lines = %q", lines)
	}
}
