package main

import (
	"bytes"
	"log"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/fsnotify/fsnotify"
)

func TestWatcherRetainsObservedOperations(t *testing.T) {
	cfg := &Config{Watch: WatchConfig{Extensions: []string{"go"}}}
	w := &Watcher{cfg: cfg, Events: make(chan WatchEvent, 4)}
	for _, op := range []fsnotify.Op{fsnotify.Write, fsnotify.Remove, fsnotify.Rename, fsnotify.Write | fsnotify.Chmod} {
		w.handle(fsnotify.Event{Name: "source.go", Op: op})
		if got := <-w.Events; got.Path != "source.go" || got.Op != op {
			t.Fatalf("event lost: %v", got)
		}
	}
	w.handle(fsnotify.Event{Name: "source.go", Op: fsnotify.Chmod})
	w.handle(fsnotify.Event{Name: "ignored.txt", Op: fsnotify.Write})
	if len(w.Events) != 0 {
		t.Fatal("metadata or excluded file triggered")
	}
}

func TestTriggerLoggingKeepsCompleteReasons(t *testing.T) {
	var buf syncBuffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)
	reasons := []WatchEvent{{Path: "startup", Op: fsnotify.Write}, {Path: "retry after failure", Op: fsnotify.Create}, {Path: "a.go", Op: fsnotify.Remove}, {Path: "b.go", Op: fsnotify.Rename}, {Path: "c.go", Op: fsnotify.Write}, {Path: "last\nfile.go", Op: fsnotify.Create}, {Path: overflowMarker}}
	original := append([]WatchEvent(nil), reasons...)
	e := &Engine{cfg: &Config{Build: BuildConfig{Command: "exit 0"}}, reportDir: t.TempDir(), verbose: true}
	p := e.startBuild(reasons)
	if p == nil {
		t.Fatal("build did not start")
	}
	<-p.Done()
	if !reflect.DeepEqual(reasons, original) {
		t.Fatal("summary mutated reasons")
	}
	for _, reason := range reasons {
		if !strings.Contains(buf.String(), "rebuild triggered: "+reason.String()) {
			t.Errorf("verbose missing %v: %s", reason, buf.String())
		}
		if !strings.Contains(e.buildTrigger, reason.String()) {
			t.Errorf("report missing %v", reason)
		}
	}
	if !strings.Contains(e.buildSummary, "+2 events; use -verbose") || strings.Contains(e.buildSummary, "last") {
		t.Fatalf("summary = %s", e.buildSummary)
	}
	if strings.Contains(buf.String(), "last\nfile") {
		t.Fatal("path injected log line")
	}
	buf = syncBuffer{}
	e.verbose = false
	p = e.startBuild([]WatchEvent{{Path: "startup"}})
	if p == nil {
		t.Fatal("build did not start")
	}
	<-p.Done()
	if strings.Contains(buf.String(), "rebuild triggered:") || !strings.Contains(buf.String(), "building (startup)") {
		t.Fatal(buf.String())
	}
	merged := mergeBatch(reasons[:1], []WatchEvent{reasons[0], {Path: "startup", Op: fsnotify.Remove}})
	if len(merged) != 2 {
		t.Fatalf("backoff merge lost event types: %v", merged)
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
