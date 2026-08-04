// watchnbuild: watch files, run a build command, run the result.
//
// Rebuilds cancel any in-flight build (the whole process group, so no
// orphaned compilers), and the running process is only restarted after a
// build succeeds — a failed build keeps the previous process serving.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
)

var version = "dev"

func main() {
	log.SetFlags(0)

	configPath := flag.String("config", "", "config file (default: first of .wnb.yaml, .wnb.yml, .wnb.json)")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println("watchnbuild", version)
		return
	}

	cfg, err := LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("[wnb] %v", err)
	}

	if cfg.Watch.ExtensionsDefaulted() {
		log.Printf("[wnb] triggering on %d built-in source file types; override with watch.extensions (use [] for every file), and name extensionless files like Makefile in watch.include",
			len(cfg.Watch.Extensions))
	}

	watcher, err := NewWatcher(cfg)
	if err != nil {
		log.Fatalf("[wnb] %v", err)
	}
	defer watcher.Close()

	batches := make(chan []string)
	go batchEvents(watcher.Events, batches, cfg.Debounce.D())

	// Signals bypass the debounce queue (watchexec treats them as urgent):
	// the engine selects on this channel directly.
	signals := make(chan os.Signal, 2)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)

	os.Exit(NewEngine(cfg, batches, signals).Run())
}
