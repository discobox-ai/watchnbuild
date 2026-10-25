package main

import (
	"bytes"
	"io"
	"strings"
	"sync"
)

const (
	// outputTailLines is how much of a command's output is kept for a
	// failure report. Older lines are dropped and counted.
	outputTailLines = 1000
	// maxLineBytes bounds one kept line, so a single enormous line
	// (minified JS, a binary dumped to the terminal) can't make the tail
	// unbounded. Longer lines are split into chunks of this size.
	maxLineBytes = 8 << 10
)

// outputTail keeps the last outputTailLines lines a command wrote to stdout
// and stderr combined, in the order they were completed. It exists only so a
// failure report can include what the command said; the terminal still gets
// everything, as soon as the command writes it to the pipe.
type outputTail struct {
	mu      sync.Mutex
	lines   []string // ring buffer once full
	next    int      // ring index of the oldest line when full
	dropped int
}

func (t *outputTail) add(line string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.lines) < outputTailLines {
		t.lines = append(t.lines, line)
		return
	}
	t.lines[t.next] = line
	t.next = (t.next + 1) % outputTailLines
	t.dropped++
}

// Snapshot returns the kept lines, oldest first, and how many earlier lines
// were dropped to stay within outputTailLines.
func (t *outputTail) Snapshot() (lines []string, dropped int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	lines = make([]string, 0, len(t.lines))
	lines = append(lines, t.lines[t.next:]...)
	lines = append(lines, t.lines[:t.next]...)
	return lines, t.dropped
}

// lineSplitter assembles one stream's bytes into lines for an outputTail.
// Each stream gets its own, so a partial line on stdout never gets glued to
// one on stderr. Not safe for concurrent use: one copier goroutine owns it.
type lineSplitter struct {
	tail    *outputTail
	partial []byte
}

func (s *lineSplitter) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 {
		i := bytes.IndexByte(p, '\n')
		if i < 0 {
			s.partial = append(s.partial, p...)
			for len(s.partial) >= maxLineBytes {
				s.tail.add(string(s.partial[:maxLineBytes]))
				s.partial = append(s.partial[:0], s.partial[maxLineBytes:]...)
			}
			break
		}
		s.partial = append(s.partial, p[:i]...)
		s.emit()
		p = p[i+1:]
	}
	return n, nil
}

// Flush emits a final line that had no trailing newline.
func (s *lineSplitter) Flush() {
	if len(s.partial) > 0 {
		s.emit()
	}
}

func (s *lineSplitter) emit() {
	line := strings.TrimSuffix(string(s.partial), "\r")
	for len(line) > maxLineBytes {
		s.tail.add(line[:maxLineBytes])
		line = line[maxLineBytes:]
	}
	s.tail.add(line)
	s.partial = s.partial[:0]
}

// copyOutput drains r into both dst (the terminal) and the tail until EOF.
// It deliberately ignores write errors on dst: a closed terminal must not
// stop the draining, or the child would block on a full pipe.
func copyOutput(dst io.Writer, r io.Reader, s *lineSplitter) {
	buf := make([]byte, 32<<10)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			_, _ = dst.Write(buf[:n])
			_, _ = s.Write(buf[:n])
		}
		if err != nil {
			s.Flush()
			return
		}
	}
}
