package watcher

import (
	"log"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Watch watches a file for changes and calls the callback on change
// Returns a stop function to stop watching
func Watch(path string, onChange func()) (stop func(), err error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	if err := w.Add(path); err != nil {
		w.Close()
		return nil, err
	}

	done := make(chan struct{})

	go func() {
		// Debounce: wait for writes to settle
		var debounce <-chan time.Time

		for {
			select {
			case <-done:
				return
			case event, ok := <-w.Events:
				if !ok {
					return
				}
				if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) {
					// Debounce: wait 500ms after last write before reloading
					debounce = time.After(500 * time.Millisecond)
				}
			case <-debounce:
				log.Printf("file changed, reloading: %s", path)
				onChange()
			case err, ok := <-w.Errors:
				if !ok {
					return
				}
				log.Printf("watcher error: %v", err)
			}
		}
	}()

	return func() {
		close(done)
		w.Close()
	}, nil
}
