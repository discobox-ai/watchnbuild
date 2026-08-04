package main

import (
	"errors"
	"io/fs"
	"log"
	"os"
	"path/filepath"

	"github.com/fsnotify/fsnotify"
)

// Watcher recursively watches the configured roots and emits relative paths
// of files whose changes pass the config filters. Directories are watched
// (not individual files) so editor atomic saves — write temp, rename over —
// still surface as events. New directories are watched as they appear.
type Watcher struct {
	fs     *fsnotify.Watcher
	cfg    *Config
	Events chan string
}

func NewWatcher(cfg *Config) (*Watcher, error) {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	w := &Watcher{fs: fsw, cfg: cfg, Events: make(chan string, 256)}

	for _, root := range cfg.Watch.Paths {
		if err := w.addRecursive(root); err != nil {
			fsw.Close()
			return nil, err
		}
	}
	go w.run()
	return w, nil
}

func (w *Watcher) Close() error { return w.fs.Close() }

func (w *Watcher) addRecursive(root string) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if !d.IsDir() {
			return nil
		}
		rel := relPath(path)
		if rel != "." && w.cfg.ExcludesDir(rel) {
			return filepath.SkipDir
		}
		if err := w.fs.Add(path); err != nil {
			log.Printf("[wnb] warning: cannot watch %s: %v", path, err)
		}
		return nil
	})
}

func (w *Watcher) run() {
	for {
		select {
		case ev, ok := <-w.fs.Events:
			if !ok {
				close(w.Events)
				return
			}
			w.handle(ev)
		case err, ok := <-w.fs.Errors:
			if !ok {
				close(w.Events)
				return
			}
			if errors.Is(err, fsnotify.ErrEventOverflow) {
				// The kernel dropped events; we cannot know what
				// changed, so trigger a rebuild to be safe.
				log.Printf("[wnb] watch queue overflowed, forcing rebuild")
				w.Events <- overflowMarker
				continue
			}
			log.Printf("[wnb] watch error: %v", err)
		}
	}
}

// overflowMarker is emitted when the OS event queue overflowed and a rebuild
// should happen even though no specific file is known to have changed.
const overflowMarker = "\x00overflow"

func (w *Watcher) handle(ev fsnotify.Event) {
	// Chmod-only events are metadata noise (editors and touch fire these
	// constantly); watchexec's filters drop them too.
	if ev.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Remove|fsnotify.Rename) == 0 {
		return
	}

	rel := relPath(ev.Name)

	if ev.Op&fsnotify.Create != 0 {
		if fi, err := os.Stat(ev.Name); err == nil && fi.IsDir() {
			if !w.cfg.ExcludesDir(rel) {
				if err := w.addRecursive(ev.Name); err != nil {
					log.Printf("[wnb] warning: cannot watch new dir %s: %v", ev.Name, err)
				}
			}
			return
		}
	}

	if w.cfg.Matches(rel) {
		w.Events <- rel
	}
}

var workDir, _ = os.Getwd()

func relPath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	rel, err := filepath.Rel(workDir, abs)
	if err != nil {
		return filepath.Clean(path)
	}
	return rel
}
