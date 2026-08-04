package main

import "time"

// batchEvents implements watchexec's throttle_collect semantics: the first
// event opens a fixed debounce window; everything arriving inside the window
// is coalesced (deduplicated, order preserved) into one batch that fires
// when the window closes. A fixed window — rather than a sliding one — keeps
// latency bounded under a steady stream of events.
//
// Reads from in until it is closed, then closes out.
func batchEvents(in <-chan string, out chan<- []string, window time.Duration) {
	defer close(out)
	for {
		first, ok := <-in
		if !ok {
			return
		}

		seen := map[string]bool{first: true}
		batch := []string{first}
		timer := time.NewTimer(window)

	collect:
		for {
			select {
			case path, ok := <-in:
				if !ok {
					timer.Stop()
					out <- batch
					return
				}
				if !seen[path] {
					seen[path] = true
					batch = append(batch, path)
				}
			case <-timer.C:
				break collect
			}
		}
		out <- batch
	}
}
