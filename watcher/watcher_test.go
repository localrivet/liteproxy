package watcher

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestWatch(t *testing.T) {
	// Create a temp file
	dir := t.TempDir()
	file := filepath.Join(dir, "test.yaml")
	if err := os.WriteFile(file, []byte("initial"), 0644); err != nil {
		t.Fatal(err)
	}

	// Track callback invocations
	var called atomic.Int32

	// Start watching
	stop, err := Watch(file, func() {
		called.Add(1)
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stop()

	// Give watcher time to start
	time.Sleep(100 * time.Millisecond)

	// Modify the file
	if err := os.WriteFile(file, []byte("modified"), 0644); err != nil {
		t.Fatal(err)
	}

	// Wait for debounce (500ms) + some buffer
	time.Sleep(700 * time.Millisecond)

	// Callback should have been called
	if called.Load() == 0 {
		t.Error("callback was not called after file modification")
	}
}

func TestWatchDebounce(t *testing.T) {
	// Create a temp file
	dir := t.TempDir()
	file := filepath.Join(dir, "test.yaml")
	if err := os.WriteFile(file, []byte("initial"), 0644); err != nil {
		t.Fatal(err)
	}

	var called atomic.Int32

	stop, err := Watch(file, func() {
		called.Add(1)
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stop()

	time.Sleep(100 * time.Millisecond)

	// Rapid modifications should be debounced to a single callback
	for i := 0; i < 5; i++ {
		if err := os.WriteFile(file, []byte("mod"+string(rune('0'+i))), 0644); err != nil {
			t.Fatal(err)
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Wait for debounce
	time.Sleep(700 * time.Millisecond)

	// Should have been called only once (or twice at most due to timing)
	count := called.Load()
	if count > 2 {
		t.Errorf("callback called %d times, expected 1-2 (debounced)", count)
	}
}

func TestWatchStop(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "test.yaml")
	if err := os.WriteFile(file, []byte("initial"), 0644); err != nil {
		t.Fatal(err)
	}

	var called atomic.Int32

	stop, err := Watch(file, func() {
		called.Add(1)
	})
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(100 * time.Millisecond)

	// Stop watching
	stop()

	// Give it time to stop
	time.Sleep(100 * time.Millisecond)

	// Modify the file after stopping
	if err := os.WriteFile(file, []byte("after-stop"), 0644); err != nil {
		t.Fatal(err)
	}

	// Wait for potential callback
	time.Sleep(700 * time.Millisecond)

	// Callback should not have been called
	if called.Load() > 0 {
		t.Error("callback was called after stop()")
	}
}

func TestWatchNonExistentFile(t *testing.T) {
	_, err := Watch("/nonexistent/path/file.yaml", func() {})
	if err == nil {
		t.Error("expected error for non-existent file")
	}
}
