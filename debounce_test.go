package main

import (
	"reflect"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
)

func TestBatchCoalescesAndDedupes(t *testing.T) {
	in := make(chan WatchEvent, 16)
	out := make(chan []WatchEvent, 4)
	go batchEvents(in, out, 100*time.Millisecond)

	in <- WatchEvent{Path: "a.go"}
	in <- WatchEvent{Path: "b.go"}
	in <- WatchEvent{Path: "a.go"}

	select {
	case batch := <-out:
		if len(batch) != 2 || batch[0].Path != "a.go" || batch[1].Path != "b.go" {
			t.Fatalf("unexpected batch: %v", batch)
		}
	case <-time.After(time.Second):
		t.Fatal("no batch within 1s")
	}
	close(in)
	if _, ok := <-out; ok {
		t.Fatal("expected out to close after in closes")
	}
}

func TestBatchWindowIsFixedNotSliding(t *testing.T) {
	in := make(chan WatchEvent)
	out := make(chan []WatchEvent, 1)
	go batchEvents(in, out, 150*time.Millisecond)

	// A steady drip faster than the window must not starve the batch: it
	// should fire ~one window after the first event.
	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-stop:
				close(in)
				return
			case in <- WatchEvent{Path: "x.go"}:
				time.Sleep(30 * time.Millisecond)
			}
		}
	}()

	start := time.Now()
	select {
	case <-out:
		if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
			t.Fatalf("batch starved for %v under steady events", elapsed)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("batch never fired under steady event stream")
	}
	close(stop)
}

func TestBatchRetainsDistinctOperationsAndOverflow(t *testing.T) {
	in := make(chan WatchEvent, 8)
	out := make(chan []WatchEvent, 1)
	want := []WatchEvent{{Path: "same.go", Op: fsnotify.Write}, {Path: "same.go", Op: fsnotify.Remove}, {Path: overflowMarker}, {Path: "same.go", Op: fsnotify.Create | fsnotify.Write}}
	for _, ev := range want {
		in <- ev
		in <- ev
	}
	close(in)
	go batchEvents(in, out, time.Second)
	if got := <-out; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if _, ok := <-out; ok {
		t.Fatal("output not closed")
	}
}
