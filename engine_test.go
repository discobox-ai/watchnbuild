//go:build unix

package main

import (
	"bytes"
	"log"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestBackoffDelay(t *testing.T) {
	cases := map[int]time.Duration{
		3: time.Second,
		4: 2 * time.Second,
		5: 4 * time.Second,
		6: 8 * time.Second,
		9: 8 * time.Second,
	}
	for cancels, want := range cases {
		if got := backoffDelay(cancels); got != want {
			t.Errorf("backoffDelay(%d) = %v, want %v", cancels, got, want)
		}
	}
}

func TestRetryDelay(t *testing.T) {
	initial, max := 15*time.Second, 10*time.Minute
	cases := map[int]time.Duration{
		1:  15 * time.Second,
		2:  30 * time.Second,
		3:  time.Minute,
		4:  2 * time.Minute,
		5:  4 * time.Minute,
		6:  8 * time.Minute,
		7:  10 * time.Minute,
		50: 10 * time.Minute,
	}
	for failures, want := range cases {
		if got := retryDelay(initial, max, failures); got != want {
			t.Errorf("retryDelay(%d) = %v, want %v", failures, got, want)
		}
	}
}

func TestEngineRetriesFailedBuild(t *testing.T) {
	var buf syncBuffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	cfg := &Config{
		Build: BuildConfig{Command: "exit 1", Grace: Duration(time.Second)},
		Run:   RunConfig{StopSignal: "TERM", Grace: Duration(time.Second)},
		Retry: RetryConfig{Initial: Duration(50 * time.Millisecond), Max: Duration(100 * time.Millisecond)},
	}
	batches := make(chan []string)
	signals := make(chan os.Signal, 1)
	exited := make(chan int, 1)
	go func() { exited <- NewEngine(cfg, batches, signals).Run() }()

	// No file change is ever sent: the rebuilds must come from the retry
	// timer alone, and the delay must grow with consecutive failures.
	deadline := time.Now().Add(5 * time.Second)
	for strings.Count(buf.String(), "build failed") < 3 {
		if time.Now().After(deadline) {
			t.Fatalf("build was not retried; log:\n%s", buf.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(buf.String(), "retrying in 50ms (failure 1)") ||
		!strings.Contains(buf.String(), "retrying in 100ms (failure 2)") {
		t.Errorf("retry delay did not back off; log:\n%s", buf.String())
	}

	signals <- os.Interrupt
	select {
	case <-exited:
	case <-time.After(10 * time.Second):
		t.Fatal("engine did not exit after signal")
	}
}

func TestEngineRetriesCrashedProcess(t *testing.T) {
	var buf syncBuffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	cfg := &Config{
		Build: BuildConfig{Command: "true", Grace: Duration(time.Second)},
		Run:   RunConfig{Command: "exit 3", StopSignal: "TERM", Grace: Duration(time.Second)},
		Retry: RetryConfig{Initial: Duration(50 * time.Millisecond), Max: Duration(50 * time.Millisecond)},
	}
	batches := make(chan []string)
	signals := make(chan os.Signal, 1)
	exited := make(chan int, 1)
	go func() { exited <- NewEngine(cfg, batches, signals).Run() }()

	deadline := time.Now().Add(5 * time.Second)
	for strings.Count(buf.String(), "process exited:") < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("crashed process was not retried; log:\n%s", buf.String())
		}
		time.Sleep(20 * time.Millisecond)
	}

	signals <- os.Interrupt
	select {
	case <-exited:
	case <-time.After(10 * time.Second):
		t.Fatal("engine did not exit after signal")
	}
}

func TestEngineRetryDisabled(t *testing.T) {
	var buf syncBuffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	off := false
	cfg := &Config{
		Build: BuildConfig{Command: "exit 1", Grace: Duration(time.Second)},
		Run:   RunConfig{StopSignal: "TERM", Grace: Duration(time.Second)},
		Retry: RetryConfig{Enabled: &off, Initial: Duration(20 * time.Millisecond), Max: Duration(20 * time.Millisecond)},
	}
	batches := make(chan []string)
	signals := make(chan os.Signal, 1)
	exited := make(chan int, 1)
	go func() { exited <- NewEngine(cfg, batches, signals).Run() }()

	time.Sleep(300 * time.Millisecond)
	if n := strings.Count(buf.String(), "build failed"); n != 1 {
		t.Errorf("built %d times with retry disabled, want 1; log:\n%s", n, buf.String())
	}

	signals <- os.Interrupt
	select {
	case <-exited:
	case <-time.After(10 * time.Second):
		t.Fatal("engine did not exit after signal")
	}
}

// A shell that ignores the stop signal and never exits on its own, so the
// grace period is actually exercised instead of being skipped.
const stubbornProcess = `trap '' TERM INT; while :; do sleep 0.2; done`

func TestSecondSignalKillsDuringGrace(t *testing.T) {
	var buf syncBuffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	off := false
	cfg := &Config{
		Build: BuildConfig{Command: "true", Grace: Duration(time.Second)},
		// A grace this long is the point: air's disco2 config used 10m.
		// The user must never have to wait it out.
		Run:   RunConfig{Command: stubbornProcess, StopSignal: "TERM", Grace: Duration(10 * time.Minute)},
		Retry: RetryConfig{Enabled: &off, Initial: Duration(time.Second), Max: Duration(time.Second)},
	}
	batches := make(chan []string)
	signals := make(chan os.Signal, 2)
	exited := make(chan int, 1)
	go func() { exited <- NewEngine(cfg, batches, signals).Run() }()

	waitForLog(t, &buf, "starting process")

	signals <- os.Interrupt
	waitForLog(t, &buf, "press again to kill immediately")

	// The 10m grace is still running: the engine must not have exited, but
	// it must also still be listening.
	select {
	case code := <-exited:
		t.Fatalf("engine exited (%d) during grace instead of waiting; log:\n%s", code, buf.String())
	case <-time.After(300 * time.Millisecond):
	}

	signals <- os.Interrupt
	select {
	case code := <-exited:
		if code != 130 {
			t.Errorf("exit code = %d, want 130", code)
		}
	case <-time.After(15 * time.Second):
		t.Fatalf("second signal did not force an exit; log:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "killing now") {
		t.Errorf("no forced-kill log; log:\n%s", buf.String())
	}
}

func TestSingleSignalExitsCleanlyWithZero(t *testing.T) {
	var buf syncBuffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	cfg := &Config{
		Build: BuildConfig{Command: "true", Grace: Duration(time.Second)},
		Run:   RunConfig{Command: "sleep 60", StopSignal: "TERM", Grace: Duration(10 * time.Second)},
	}
	batches := make(chan []string)
	signals := make(chan os.Signal, 2)
	exited := make(chan int, 1)
	go func() { exited <- NewEngine(cfg, batches, signals).Run() }()

	waitForLog(t, &buf, "starting process")
	signals <- os.Interrupt

	select {
	case code := <-exited:
		if code != 0 {
			t.Errorf("exit code = %d, want 0 for a process that stops on request", code)
		}
	case <-time.After(15 * time.Second):
		t.Fatalf("engine did not exit; log:\n%s", buf.String())
	}
}

// TestSignalDuringRestartGrace covers the other blocking stop: swapping the
// run process after a successful rebuild, not shutting down.
func TestSignalDuringRestartGrace(t *testing.T) {
	var buf syncBuffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	cfg := &Config{
		Build: BuildConfig{Command: "true", Grace: Duration(time.Second)},
		Run:   RunConfig{Command: stubbornProcess, StopSignal: "TERM", Grace: Duration(10 * time.Minute)},
	}
	batches := make(chan []string)
	signals := make(chan os.Signal, 2)
	exited := make(chan int, 1)
	go func() { exited <- NewEngine(cfg, batches, signals).Run() }()

	waitForLog(t, &buf, "starting process")
	batches <- []string{"main.go"} // rebuild -> stop the stubborn process
	waitForLog(t, &buf, "stopping process")

	signals <- os.Interrupt
	waitForLog(t, &buf, "press again to kill immediately")
	signals <- os.Interrupt

	select {
	case code := <-exited:
		if code != 130 {
			t.Errorf("exit code = %d, want 130", code)
		}
	case <-time.After(15 * time.Second):
		t.Fatalf("signals during a restart stop did not exit; log:\n%s", buf.String())
	}
	if strings.Contains(buf.String(), "starting process\n[wnb] starting process") {
		t.Error("a replacement process was started during shutdown")
	}
}

func waitForLog(t *testing.T, buf *syncBuffer, want string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(buf.String(), want) {
		if time.Now().After(deadline) {
			t.Fatalf("never logged %q; log:\n%s", want, buf.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestMergeBatch(t *testing.T) {
	got := mergeBatch([]string{"a", "b"}, []string{"b", "c"})
	if len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Fatalf("mergeBatch = %v", got)
	}
}

func TestHotSummaryOrdersByFrequency(t *testing.T) {
	s := hotSummary(map[string]int{"a.log": 5, "b.go": 2, "c.go": 2})
	if !strings.HasPrefix(s, "a.log x5") {
		t.Fatalf("hotSummary = %q", s)
	}
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestEngineBacksOffAfterRepeatedCancels(t *testing.T) {
	var buf syncBuffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	cfg := &Config{
		Build: BuildConfig{Command: "sleep 0.4", Grace: Duration(time.Second)},
		Run:   RunConfig{StopSignal: "TERM", Grace: Duration(time.Second)},
	}
	batches := make(chan []string)
	signals := make(chan os.Signal, 1)
	exited := make(chan int, 1)
	go func() { exited <- NewEngine(cfg, batches, signals).Run() }()

	// Each batch lands while the (fresh) 0.4s build is still running, so
	// every one is a cancel; the third must trip the backoff warning.
	for i := 0; i < 4; i++ {
		time.Sleep(120 * time.Millisecond)
		batches <- []string{"hot.txt"}
	}

	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(buf.String(), "Backing off") {
		if time.Now().After(deadline) {
			t.Fatalf("no backoff warning; log:\n%s", buf.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !strings.Contains(buf.String(), "hot.txt") {
		t.Errorf("warning does not name the hot file; log:\n%s", buf.String())
	}

	signals <- os.Interrupt
	select {
	case <-exited:
	case <-time.After(10 * time.Second):
		t.Fatal("engine did not exit after signal")
	}
}
