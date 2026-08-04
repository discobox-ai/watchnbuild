package main

import (
	"testing"
	"time"
)

func TestBatchCoalescesAndDedupes(t *testing.T) {
	in := make(chan string, 16)
	out := make(chan []string, 4)
	go batchEvents(in, out, 100*time.Millisecond)

	in <- "a.go"
	in <- "b.go"
	in <- "a.go"

	select {
	case batch := <-out:
		if len(batch) != 2 || batch[0] != "a.go" || batch[1] != "b.go" {
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
	in := make(chan string)
	out := make(chan []string, 1)
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
			case in <- "x.go":
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
